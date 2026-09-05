package site

import (
	"bytes"
	"embed"
	"html/template"
	"path/filepath"
	"time"
)

//go:embed templates
var templateFS embed.FS

var pages = template.Must(template.ParseFS(templateFS, "templates/*.html"))

// page is the chrome every template shares.
type page struct {
	Title string
	Now   string
}

type indexPage struct {
	page
	Graphs []card
}

// card is one graph as it appears on the front page.
type card struct {
	Title string
	Sub   string
	Href  string
	Img   string
}

type graphPage struct {
	page
	SiteTitle string
	Images    []rangeImage
}

type rangeImage struct {
	Title string
	Src   string
}

// writePages writes the front page and one page per graph. It is called after
// the images, and separately from them, so the pages still describe the config
// even when a query failed and an image is stale.
func (s *Site) writePages() error {
	now := time.Now().Format("2006-01-02 15:04:05 MST")

	idx := indexPage{page: page{Title: s.Cfg.Output.Title, Now: now}}
	for _, g := range s.Cfg.Graphs {
		if len(g.Ranges) == 0 {
			continue
		}
		// The front page carries the finest timescale, the way MRTG shows the
		// daily graph, and links through to the rest.
		idx.Graphs = append(idx.Graphs, card{
			Title: g.Title,
			Sub:   g.VLabel,
			Href:  g.Name + ".html",
			Img:   imagePath(g.Name, g.Ranges[0].Name),
		})

		gp := graphPage{
			page:      page{Title: g.Title, Now: now},
			SiteTitle: s.Cfg.Output.Title,
		}
		for _, r := range g.Ranges {
			gp.Images = append(gp.Images, rangeImage{Title: r.Title, Src: imagePath(g.Name, r.Name)})
		}
		if err := s.writePage(g.Name+".html", "graph.html", gp); err != nil {
			return err
		}
	}
	return s.writePage("index.html", "index.html", idx)
}

func (s *Site) writePage(name, tmpl string, data any) error {
	var buf bytes.Buffer
	if err := pages.ExecuteTemplate(&buf, tmpl, data); err != nil {
		return err
	}
	return writeAtomic(filepath.Join(s.Cfg.Output.Dir, name), buf.Bytes())
}
