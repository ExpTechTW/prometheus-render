// Package params turns a flat set of key/value settings into a query request
// and render options. The CLI and the HTTP server both go through it, so a flag
// and its URL parameter cannot drift apart.
package params

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/ExpTechTW/prometheus-render/internal/promapi"
	"github.com/ExpTechTW/prometheus-render/internal/query"
	"github.com/ExpTechTW/prometheus-render/tsgraph"
)

// Defaults fill in for settings that were not supplied.
type Defaults struct {
	Theme     string
	Width     int
	Height    int
	From      string
	Until     string
	MaxPoints int
}

// Graph is everything needed to draw one graph: what to fetch, how to draw it,
// and the per-series presentation the renderer does not decide for itself.
type Graph struct {
	Request *query.Request
	Options tsgraph.Options
	Kinds   []tsgraph.Kind
	Widths  []float64
}

// Build resolves settings into a query and the options to render its result.
func Build(v url.Values, d Defaults, now time.Time) (*Graph, error) {
	targets := v["target"]
	if len(targets) == 0 {
		return nil, fmt.Errorf("no query given")
	}

	from, err := promapi.ParseTime(str(v, "from", d.From, "-1h"), now)
	if err != nil {
		return nil, fmt.Errorf("from: %w", err)
	}
	until, err := promapi.ParseTime(str(v, "until", d.Until, "now"), now)
	if err != nil {
		return nil, fmt.Errorf("until: %w", err)
	}

	var step time.Duration
	if s := v.Get("step"); s != "" {
		if step, err = promapi.ParseStep(s); err != nil {
			return nil, fmt.Errorf("step: %w", err)
		}
	}

	area, err := parseArea(v.Get("area"))
	if err != nil {
		return nil, err
	}
	loc := time.Local
	if tz := v.Get("tz"); tz != "" {
		if loc, err = time.LoadLocation(tz); err != nil {
			return nil, fmt.Errorf("tz: %w", err)
		}
	}

	width := num(v, "width", d.Width, 400)
	height := num(v, "height", d.Height, 175)
	n := len(targets)

	g := &Graph{
		Request: &query.Request{
			Targets:   pairTargets(targets, v["legend"]),
			From:      from,
			Until:     until,
			Step:      step,
			Width:     width,
			MaxPoints: num(v, "maxPoints", d.MaxPoints, query.DefaultMaxPoints),
		},
		Options: tsgraph.Options{
			Title:      v.Get("title"),
			VLabel:     v.Get("vtitle"),
			Width:      width,
			Height:     height,
			Theme:      tsgraph.LookupTheme(str(v, "theme", d.Theme, "mrtg")),
			Location:   loc,
			YMin:       optFloat(v.Get("yMin")),
			YMax:       optFloat(v.Get("yMax")),
			HideLegend: truthy(v, "hideLegend"),
			HideStats:  truthy(v, "hideStats"),
			BehindFrom: atoiOr(v.Get("behindFrom")),
			Zoom:       f64(v.Get("zoom")),
		},
		Kinds:  make([]tsgraph.Kind, n),
		Widths: make([]float64, n),
	}
	if v.Get("base") == "1024" {
		g.Options.Base = 1024
	}
	lineWidth := f64(v.Get("lineWidth"))
	for i := range g.Kinds {
		g.Kinds[i] = seriesKind(area, i)
		g.Widths[i] = lineWidth
	}
	return g, nil
}

// seriesKind applies the area mode to one series by index.
func seriesKind(area string, i int) tsgraph.Kind {
	switch area {
	case "all":
		return tsgraph.Area
	case "stacked":
		if i == 0 {
			return tsgraph.Area
		}
		return tsgraph.Stack
	case "first":
		if i == 0 {
			return tsgraph.Area
		}
	}
	return tsgraph.Line
}

func parseArea(s string) (string, error) {
	switch s {
	case "", "none", "first", "all", "stacked":
		return s, nil
	}
	return "", fmt.Errorf("unknown area mode %q: want none, first, all or stacked", s)
}

// pairTargets pairs each query with its legend format. A single legend applies
// to every query, which is the common case for a one-expression graph.
func pairTargets(queries, legends []string) []query.Target {
	out := make([]query.Target, 0, len(queries))
	for i, q := range queries {
		legend := ""
		switch {
		case len(legends) == 1:
			legend = legends[0]
		case i < len(legends):
			legend = legends[i]
		}
		out = append(out, query.Target{Expr: q, Legend: legend})
	}
	return out
}

func str(v url.Values, key, def, fallback string) string {
	for _, s := range []string{v.Get(key), def, fallback} {
		if s != "" {
			return s
		}
	}
	return ""
}

func num(v url.Values, key string, def, fallback int) int {
	if n, err := strconv.Atoi(v.Get(key)); err == nil && n > 0 {
		return n
	}
	if def > 0 {
		return def
	}
	return fallback
}

func atoiOr(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

func f64(s string) float64 {
	f, _ := strconv.ParseFloat(s, 64)
	return f
}

func optFloat(s string) *float64 {
	if s == "" {
		return nil
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return nil
	}
	return &f
}

func truthy(v url.Values, key string) bool {
	switch strings.ToLower(v.Get(key)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}
