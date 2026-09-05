package config

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ExpTechTW/prometheus-render/internal/params"
	"github.com/ExpTechTW/prometheus-render/tsgraph"
)

const minimal = `
graphs:
  - name: load
    series:
      - expr: node_load1
`

func parse(t *testing.T, src string) *Config {
	t.Helper()
	c, err := Parse([]byte(src))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return c
}

func TestParseFillsInTheDefaultsAConfigOmits(t *testing.T) {
	c := parse(t, minimal)
	if c.Source.URL != "http://localhost:9090" {
		t.Errorf("url = %q", c.Source.URL)
	}
	if c.Source.Timeout.Duration() != 30*time.Second {
		t.Errorf("timeout = %s", c.Source.Timeout.Duration())
	}
	if c.Source.MaxQueries != 8 {
		t.Errorf("max_queries = %d", c.Source.MaxQueries)
	}
	if c.Output.Dir != "site" {
		t.Errorf("dir = %q", c.Output.Dir)
	}
	// A graph with no title of its own is listed under its name.
	if c.Graphs[0].Title != "load" {
		t.Errorf("title = %q", c.Graphs[0].Title)
	}
}

func TestParseSuppliesTheMRTGTimescales(t *testing.T) {
	c := parse(t, minimal)
	got := c.Graphs[0].Ranges
	if len(got) != len(DefaultRanges) {
		t.Fatalf("ranges = %d, want %d", len(got), len(DefaultRanges))
	}
	for i, want := range DefaultRanges {
		if got[i].Name != want.Name || got[i].From != want.From {
			t.Errorf("range %d = %+v, want %+v", i, got[i], want)
		}
	}
}

func TestGraphOverridesDefaults(t *testing.T) {
	c := parse(t, `
defaults:
  theme: mrtg
  width: 500
  area: first
  ranges:
    - {name: 6h, from: -6h}
graphs:
  - name: a
    series: [{expr: up}]
  - name: b
    theme: munin
    width: 800
    series: [{expr: up}]
    ranges:
      - {name: 1h, from: -1h}
`)
	a, b := c.Graphs[0], c.Graphs[1]
	if a.Theme != "mrtg" || a.Width != 500 || a.Area != "first" {
		t.Errorf("inherited = %q %d %q", a.Theme, a.Width, a.Area)
	}
	if b.Theme != "munin" || b.Width != 800 {
		t.Errorf("overridden = %q %d", b.Theme, b.Width)
	}
	if b.Area != "first" {
		t.Errorf("b.Area = %q, want the inherited \"first\"", b.Area)
	}
	if len(a.Ranges) != 1 || a.Ranges[0].Name != "6h" {
		t.Errorf("a.Ranges = %+v", a.Ranges)
	}
	if len(b.Ranges) != 1 || b.Ranges[0].Name != "1h" {
		t.Errorf("b.Ranges = %+v", b.Ranges)
	}
}

// Anything that would otherwise fail inside a worker, minutes later, should
// fail at load instead.
func TestParseRejects(t *testing.T) {
	for _, tc := range []struct{ name, src string }{
		{"no graphs", `source: {url: http://x}`},
		{"no series", "graphs:\n  - name: a\n"},
		{"empty expr", "graphs:\n  - name: a\n    series: [{expr: \"\"}]\n"},
		{"unsafe name", "graphs:\n  - name: ../etc\n    series: [{expr: up}]\n"},
		{"empty name", "graphs:\n  - name: \"\"\n    series: [{expr: up}]\n"},
		{"duplicate name", "graphs:\n  - {name: a, series: [{expr: up}]}\n  - {name: a, series: [{expr: up}]}\n"},
		{"duplicate range", "graphs:\n  - name: a\n    series: [{expr: up}]\n    ranges: [{name: d, from: -1d}, {name: d, from: -2d}]\n"},
		{"bad from", "graphs:\n  - name: a\n    series: [{expr: up}]\n    ranges: [{name: d, from: yesterday}]\n"},
		{"bad step", "graphs:\n  - name: a\n    series: [{expr: up}]\n    ranges: [{name: d, from: -1d, step: soon}]\n"},
		{"bad tz", "defaults: {tz: Mars/Olympus}\ngraphs:\n  - {name: a, series: [{expr: up}]}\n"},
		{"bad duration", "output: {interval: soon}\ngraphs:\n  - {name: a, series: [{expr: up}]}\n"},
		{"misspelt key", "graphs:\n  - name: a\n    serieses: [{expr: up}]\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Parse([]byte(tc.src)); err == nil {
				t.Error("expected an error")
			}
		})
	}
}

func TestIntervalAcceptsBareSeconds(t *testing.T) {
	c := parse(t, "output: {interval: 300}\n"+minimal)
	if got := c.Output.Interval.Duration(); got != 5*time.Minute {
		t.Errorf("interval = %s, want 5m", got)
	}
}

// The config deliberately grows no semantics of its own: every field is
// flattened into the settings the params package already resolves. If that
// contract slips, a config key silently stops doing anything.
func TestValuesResolveThroughParams(t *testing.T) {
	c := parse(t, `
defaults:
  theme: munin
  width: 640
  height: 200
  area: stacked
  zoom: 2
  tz: UTC
graphs:
  - name: cpu
    title: CPU
    vtitle: cores
    base: 1024
    y_min: 0
    series:
      - {expr: "sum by (mode) (rate(node_cpu_seconds_total[5m]))", legend: "{{mode}}"}
      - {expr: node_load1, legend: load}
    ranges:
      - {name: 1d, title: "Daily", from: -1d, step: 5m}
`)
	g := c.Graphs[0]
	built, err := params.Build(g.Values(g.Ranges[0]), params.Defaults{}, time.Now())
	if err != nil {
		t.Fatalf("params.Build: %v", err)
	}

	if got := built.Options.Title; got != "CPU" {
		t.Errorf("title = %q", got)
	}
	if got := built.Options.VLabel; got != "cores" {
		t.Errorf("vtitle = %q", got)
	}
	if built.Options.Width != 640 || built.Options.Height != 200 {
		t.Errorf("size = %dx%d", built.Options.Width, built.Options.Height)
	}
	if built.Options.Base != 1024 {
		t.Errorf("base = %d", built.Options.Base)
	}
	if built.Options.Zoom != 2 {
		t.Errorf("zoom = %v", built.Options.Zoom)
	}
	if built.Options.YMin == nil || *built.Options.YMin != 0 {
		t.Errorf("yMin = %v", built.Options.YMin)
	}
	if built.Options.Location.String() != "UTC" {
		t.Errorf("tz = %s", built.Options.Location)
	}
	if !reflect.DeepEqual(built.Options.Theme, tsgraph.LookupTheme("munin")) {
		t.Errorf("theme did not reach the renderer")
	}
	// area: stacked means the first series fills and the rest stack on it.
	if built.Kinds[0] != tsgraph.Area || built.Kinds[1] != tsgraph.Stack {
		t.Errorf("kinds = %v", built.Kinds)
	}
	// Legends pair with targets by position.
	if len(built.Request.Targets) != 2 ||
		built.Request.Targets[1].Legend != "load" ||
		!strings.Contains(built.Request.Targets[0].Expr, "node_cpu_seconds_total") {
		t.Errorf("targets = %+v", built.Request.Targets)
	}
	if got := built.Request.ResolveStep(); got != 5*time.Minute {
		t.Errorf("step = %s", got)
	}
}
