package site

import (
	"bytes"
	"embed"
	"html/template"
	"os"
	"path/filepath"

	"github.com/ExpTechTW/prometheus-render/internal/config"
)

//go:embed templates
var templateFS embed.FS

// The page font travels with the pages, so a reader without it installed still
// sees what the drawings are lettered in. See tsgraph/fonts for the cut.
//
//go:embed fonts/*.woff2
var fontFS embed.FS

var pages = template.Must(template.ParseFS(templateFS, "templates/*.html"))

// page is the chrome every template shares.
//
// Up is the prefix back to the site root. Every link is written relative to
// the page holding it, because the site is usually served under a path prefix
// where a root-relative link would point outside it.
type page struct {
	Title     string
	SiteTitle string
	Up        string

	// Interval is how often the site is redrawn, in seconds. A page counts
	// down to the next wall-clock boundary of it and rebuilds its images
	// there, which is the whole of the arrangement: both sides work it out
	// from this one number and never ask each other. Zero means the site is
	// drawn once and not again.
	Interval int
}

// link is an entry in one of the index's two lists.
type link struct {
	Title string
	Href  string
}

// card is one drawing as it appears in a list: the finest timescale, linking
// through to every timescale of it.
type card struct {
	Title string
	Sub   string
	Href  string
	Key   string // identifies the drawing to the variant switch
	Base  string // image path up to the palette, ready for the page to finish
	Range string
	Peak  bool
}

type indexPage struct {
	page
	Regions []link
	Graphs  []link
	Cards   []card // the graphs that are not split by region
}

type listPage struct {
	page
	Cards []card
}

type rangeImage struct {
	Title string
	Range string
}

type detailPage struct {
	page
	Key    string
	Base   string
	Peak   bool
	Images []rangeImage
}

// writePages writes the index, the two ways into it, and a page per drawing.
// It runs after the images and separately from them, so the pages still
// describe the config when a query failed and an image is stale.
func (s *Site) writePages() error {
	if err := s.writeFonts(); err != nil {
		return err
	}
	title := s.Cfg.Output.Title

	interval := int(s.Cfg.Output.Interval.Duration().Seconds())
	chrome := func(name, pageTitle string) page {
		return page{
			Title: pageTitle, SiteTitle: title, Up: upTo(name),
			Interval: interval,
		}
	}

	idx := indexPage{page: chrome("index.html", title)}

	// Everything that is not split by region sits on the index itself, and
	// each drawing gets its own page of timescales.
	for _, g := range s.Cfg.Graphs {
		for _, region := range s.scope(g) {
			if !s.has(region.Name, g) {
				continue
			}
			name := detailPath(region.Name, g.Name)
			dp := detailPage{
				page:   chrome(name, g.Title),
				Key:    imageBase(region.Name, g.Name),
				Base:   upTo(name) + imageBase(region.Name, g.Name),
				Peak:   g.Peaks(),
				Images: rangeImages(g),
			}
			if region.Name != "" {
				dp.Title = g.Title + " — " + region.Name
			}
			if err := s.writePage(name, "detail.html", dp); err != nil {
				return err
			}
			if region.Name == "" {
				idx.Cards = append(idx.Cards, s.card(g, region, "index.html"))
			}
		}
	}

	if s.Cfg.Split() {
		// One page per region: everything about one place.
		for _, region := range s.regionList() {
			name := "region/" + region.Name + ".html"
			lp := listPage{page: chrome(name, region.Name)}
			for _, g := range s.Cfg.Graphs {
				if !g.Global && s.has(region.Name, g) {
					lp.Cards = append(lp.Cards, s.card(g, region, name))
				}
			}
			if len(lp.Cards) == 0 {
				continue
			}
			if err := s.writePage(name, "list.html", lp); err != nil {
				return err
			}
			idx.Regions = append(idx.Regions, link{Title: region.Name, Href: name})
		}

		// One page per graph: the same thing across every place.
		for _, g := range s.Cfg.Graphs {
			if g.Global {
				continue
			}
			name := "graph/" + g.Name + ".html"
			lp := listPage{page: chrome(name, g.Title)}
			for _, region := range s.regionList() {
				if g.InRegion(region.Value) && s.has(region.Name, g) {
					c := s.card(g, region, name)
					c.Title = region.Name
					lp.Cards = append(lp.Cards, c)
				}
			}
			if len(lp.Cards) == 0 {
				continue
			}
			if err := s.writePage(name, "list.html", lp); err != nil {
				return err
			}
			idx.Graphs = append(idx.Graphs, link{Title: g.Title, Href: name})
		}
	}

	return s.writePage("index.html", "index.html", idx)
}

// card describes one drawing as seen from the page at from.
func (s *Site) card(g *config.Graph, region config.Region, from string) card {
	up := upTo(from)
	base := imageBase(region.Name, g.Name)
	return card{
		Title: g.Title,
		Sub:   g.VLabel,
		Href:  up + detailPath(region.Name, g.Name),
		Key:   base,
		Base:  up + base,
		Range: g.Ranges[0].Name,
		Peak:  g.Peaks(),
	}
}

func rangeImages(g *config.Graph) []rangeImage {
	out := make([]rangeImage, 0, len(g.Ranges))
	for _, r := range g.Ranges {
		out = append(out, rangeImage{Title: r.Title, Range: r.Name})
	}
	return out
}

func (s *Site) regionList() []config.Region {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]config.Region(nil), s.regions...)
}

// writeFonts puts the font files beside the pages, and only when they are
// missing or differ. Rewriting them each pass would move their timestamps
// every interval and cost every reader a revalidation of a file that never
// changes.
func (s *Site) writeFonts() error {
	entries, err := fontFS.ReadDir("fonts")
	if err != nil {
		return err
	}
	dir := filepath.Join(s.Cfg.Output.Dir, "fonts")
	if err := ensureDir(dir); err != nil {
		return err
	}
	for _, e := range entries {
		want, err := fontFS.ReadFile("fonts/" + e.Name())
		if err != nil {
			return err
		}
		path := filepath.Join(dir, e.Name())
		if have, err := os.ReadFile(path); err == nil && bytes.Equal(have, want) {
			continue
		}
		if err := writeAtomic(path, want); err != nil {
			return err
		}
	}
	return nil
}

func (s *Site) writePage(name, tmpl string, data any) error {
	var buf bytes.Buffer
	if err := pages.ExecuteTemplate(&buf, tmpl, data); err != nil {
		return err
	}
	path := filepath.Join(s.Cfg.Output.Dir, filepath.FromSlash(name))
	if err := ensureDir(filepath.Dir(path)); err != nil {
		return err
	}
	return writeAtomic(path, buf.Bytes())
}
