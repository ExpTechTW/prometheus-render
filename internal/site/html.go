package site

import (
	"bytes"
	"embed"
	"html/template"
	"path/filepath"
	"time"

	"github.com/ExpTechTW/prometheus-render/internal/config"
)

//go:embed templates
var templateFS embed.FS

var pages = template.Must(template.ParseFS(templateFS, "templates/*.html"))

// page is the chrome every template shares.
//
// Up is the prefix back to the site root. Every link is written relative to
// the page holding it, because the site is usually served under a path prefix
// where a root-relative link would point outside it.
type page struct {
	Title     string
	SiteTitle string
	Now       string
	Up        string
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
	now := time.Now().Format("2006-01-02 15:04:05 MST")
	title := s.Cfg.Output.Title

	chrome := func(name, pageTitle string) page {
		return page{Title: pageTitle, SiteTitle: title, Now: now, Up: upTo(name)}
	}

	idx := indexPage{page: chrome("index.html", title)}

	// Everything that is not split by region sits on the index itself, and
	// each drawing gets its own page of timescales.
	for _, g := range s.Cfg.Graphs {
		for _, region := range s.scope(g) {
			if !s.has(region, g) {
				continue
			}
			name := detailPath(region, g.Name)
			dp := detailPage{
				page:   chrome(name, g.Title),
				Key:    imageBase(region, g.Name),
				Base:   upTo(name) + imageBase(region, g.Name),
				Peak:   g.Peaks(),
				Images: rangeImages(g),
			}
			if region != "" {
				dp.Title = g.Title + " — " + s.Cfg.Title(region)
			}
			if err := s.writePage(name, "detail.html", dp); err != nil {
				return err
			}
			if region == "" {
				idx.Cards = append(idx.Cards, s.card(g, region, "index.html"))
			}
		}
	}

	if s.Cfg.Split() {
		// One page per region: everything about one place.
		for _, region := range s.regionList() {
			name := "region/" + region + ".html"
			lp := listPage{page: chrome(name, s.Cfg.Title(region))}
			for _, g := range s.Cfg.Graphs {
				if !g.Global && s.has(region, g) {
					lp.Cards = append(lp.Cards, s.card(g, region, name))
				}
			}
			if len(lp.Cards) == 0 {
				continue
			}
			if err := s.writePage(name, "list.html", lp); err != nil {
				return err
			}
			idx.Regions = append(idx.Regions, link{Title: s.Cfg.Title(region), Href: name})
		}

		// One page per graph: the same thing across every place.
		for _, g := range s.Cfg.Graphs {
			if g.Global {
				continue
			}
			name := "graph/" + g.Name + ".html"
			lp := listPage{page: chrome(name, g.Title)}
			for _, region := range s.regionList() {
				if g.InRegion(region) && s.has(region, g) {
					c := s.card(g, region, name)
					c.Title = s.Cfg.Title(region)
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
func (s *Site) card(g *config.Graph, region, from string) card {
	up := upTo(from)
	base := imageBase(region, g.Name)
	return card{
		Title: g.Title,
		Sub:   g.VLabel,
		Href:  up + detailPath(region, g.Name),
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

func (s *Site) regionList() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.regions...)
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
