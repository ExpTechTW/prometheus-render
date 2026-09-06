package site

import (
	"bytes"
	"embed"
	"encoding/json"
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

	// Version identifies the pass that drew this page. It is appended to every
	// image URL, so a browser fetches a picture again exactly when there is a
	// new one -- rather than on a timer, which would spend the cache on files
	// that had not changed.
	Version int64

	// Interval is how often the site is redrawn, in seconds, for the page to
	// count down to. Zero means it is drawn once and not again.
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
	Title   string
	Sub     string
	Href    string
	Key     string // identifies the drawing to the variant switch
	Base    string // image path up to the palette, ready for the page to finish
	Range   string
	Peak    bool
	Version int64 // the pass that drew it, so the URL changes only when it does
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
	now := time.Now().In(s.Cfg.Location()).Format("2006-01-02 15:04:05 MST")
	title := s.Cfg.Output.Title

	version := s.version()
	interval := int(s.Cfg.Output.Interval.Duration().Seconds())
	chrome := func(name, pageTitle string) page {
		return page{
			Title: pageTitle, SiteTitle: title, Now: now, Up: upTo(name),
			Version: version, Interval: interval,
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

	if err := s.writeVersion(version, interval, now); err != nil {
		return err
	}
	return s.writePage("index.html", "index.html", idx)
}

// writeVersion publishes what a page polls to learn that a new pass has
// happened: a few bytes, so checking often costs nothing.
func (s *Site) writeVersion(version int64, interval int, now string) error {
	b, err := json.Marshal(struct {
		Version  int64  `json:"version"`
		Interval int    `json:"interval"`
		Updated  string `json:"updated"`
	}{version, interval, now})
	if err != nil {
		return err
	}
	return writeAtomic(filepath.Join(s.Cfg.Output.Dir, "version.json"), b)
}

// card describes one drawing as seen from the page at from.
func (s *Site) card(g *config.Graph, region config.Region, from string) card {
	up := upTo(from)
	base := imageBase(region.Name, g.Name)
	return card{
		Title:   g.Title,
		Sub:     g.VLabel,
		Href:    up + detailPath(region.Name, g.Name),
		Key:     base,
		Base:    up + base,
		Range:   g.Ranges[0].Name,
		Peak:    g.Peaks(),
		Version: s.version(),
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
