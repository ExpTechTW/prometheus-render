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
	built, err := params.Build(g.Values(g.Ranges[0], "", g.Theme, VariantPlain), params.Defaults{}, time.Now())
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

// Each timescale peaks at the resolution of the one below it. A fixed
// resolution would make the yearly graph ask for a hundred thousand points per
// series, which real servers refuse.
func TestPeaksLadderDownTheTimescales(t *testing.T) {
	c := parse(t, `
defaults:
  peak: true
  ranges:
    - {name: 1d, from: -1d,   step: 5m}
    - {name: 1w, from: -7d,   step: 30m}
    - {name: 1m, from: -30d,  step: 2h}
    - {name: 1y, from: -365d, step: 1d}
graphs:
  - name: a
    series: [{expr: up, legend: x}]
`)
	g := c.Graphs[0]
	want := []string{"5m", "5m", "30m", "2h"}
	for i, r := range g.Ranges {
		if r.PeakStep != want[i] {
			t.Errorf("%s peaks over %q, want %q", r.Name, r.PeakStep, want[i])
		}
	}

	// And that resolution is what reaches the query.
	last := g.Ranges[3]
	targets := g.Values(last, "", g.Theme, VariantPeak)["target"]
	if len(targets) != 2 {
		t.Fatalf("targets = %d, want the average and its peak", len(targets))
	}
	if got := targets[1]; got != "max_over_time((up)[1d:2h])" {
		t.Errorf("peak target = %q", got)
	}
	// The plain variant asks for the averages alone.
	if plain := g.Values(last, "", g.Theme, VariantPlain)["target"]; len(plain) != 1 {
		t.Errorf("plain targets = %v, want just the average", plain)
	}
}

// A window written into an expression once flattens every timescale that
// borrows it: a five-second burst read through rate(x[5m]) arrives sixty times
// smaller than it was. $step is how one line of config follows the timescale
// it is drawn at instead.
func TestStepFollowsTheTimescale(t *testing.T) {
	c := parse(t, `
source:
  resolution: 5s
defaults:
  peak: true
  ranges:
    - {name: 1d, from: -1d,   step: 5m}
    - {name: 1w, from: -7d,   step: 30m}
    - {name: 1y, from: -365d, step: 1d}
graphs:
  - name: a
    series: [{expr: 'sum(rate(rx[$step])) * 8', legend: RX}]
`)
	g := c.Graphs[0]
	want := []string{
		"sum(rate(rx[5m])) * 8",
		"sum(rate(rx[30m])) * 8",
		"sum(rate(rx[1d])) * 8",
	}
	for i, r := range g.Ranges {
		if got := g.Values(r, "", g.Theme, VariantPlain)["target"][0]; got != want[i] {
			t.Errorf("%s averages over %q, want %q", r.Name, got, want[i])
		}
	}

	// The peak is the point of the exercise: its inner samples are taken at
	// the step it is sampled at, not the bucket they are reduced into. Reading
	// the same 5m window inside a 5m bucket is what made the peak trace a copy
	// of the average.
	daily := g.Values(g.Ranges[0], "", g.Theme, VariantPeak)["target"]
	if len(daily) != 2 {
		t.Fatalf("targets = %d, want the average and its peak", len(daily))
	}
	if got := daily[1]; got != "max_over_time((sum(rate(rx[10s])) * 8)[5m:10s])" {
		t.Errorf("peak target = %q", got)
	}
	if daily[0] == daily[1] {
		t.Error("the peak reads exactly what the average does")
	}
}

// ${step} is spelt both ways, as the region placeholder is, and the two
// substitutions do not tread on each other.
func TestStepAndRegionShareOneExpression(t *testing.T) {
	c := parse(t, `
regions:
  label: instance
defaults:
  ranges: [{name: 1d, from: -1d, step: 5m}]
graphs:
  - name: a
    series: [{expr: 'rate(rx{instance="$instance"}[${step}])'}]
`)
	g := c.Graphs[0]
	got := g.Values(g.Ranges[0], "node-1", g.Theme, VariantPlain)["target"][0]
	if want := `rate(rx{instance="node-1"}[5m])`; got != want {
		t.Errorf("target = %q, want %q", got, want)
	}
}

// The finest timescale has nothing below it to peak at. It used to fall back
// to its own step, which puts a single sample in each bucket and draws a peak
// trace identical to the average it sits behind.
func TestFinestRangePeaksAtTheSourceResolution(t *testing.T) {
	for _, tc := range []struct{ name, resolution, step, want string }{
		{"coarse buckets peak at two scrapes", "5s", "5m", "10s"},
		{"already at the source's resolution", "5s", "10s", "10s"},
		// Whole seconds, not "1m0s": a step is a single-unit offset, and a
		// derived one that cannot be read back fails the load it came from.
		{"a doubling that would print as 1m0s", "30s", "5m", "60s"},
		{"no resolution given keeps the old fallback", "", "5m", "5m"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := parse(t, "source: {resolution: "+defaulted(tc.resolution, "0")+"}\n"+`
defaults:
  peak: true
  ranges: [{name: 1d, from: -1d, step: `+tc.step+`}]
graphs:
  - name: a
    series: [{expr: up}]
`)
			if got := c.Graphs[0].Ranges[0].PeakStep; got != tc.want {
				t.Errorf("peak_step = %q, want %q", got, tc.want)
			}
		})
	}
}

// Both ceilings a step can walk into, refused while the file is being read
// rather than as an empty graph or a failed query hours later.
func TestParseRejectsAStepTheSourceCannotServe(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{
			// rate() needs two samples; a 5s window over a 5s scrape holds one.
			"a window shorter than two scrapes",
			`source: {resolution: 5s}
defaults: {ranges: [{name: fine, from: -1h, step: 5s}]}
graphs: [{name: a, series: [{expr: 'rate(rx[$step])'}]}]`,
			"two scrapes",
		},
		{
			// A year of five-second peaks is 6.3 million points per series.
			"a subquery past the source's point ceiling",
			`defaults: {peak: true, ranges: [{name: 1y, from: -365d, step: 1d, peak_step: 5s}]}
graphs: [{name: a, series: [{expr: up}]}]`,
			"subquery",
		},
		{
			// Widening this silently would leave $step describing a
			// resolution the drawing is no longer at.
			"more points than one query returns",
			`defaults: {ranges: [{name: 1y, from: -365d, step: 1m}]}
graphs: [{name: a, series: [{expr: up}]}]`,
			"one query",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(tc.src))
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not say %q", err, tc.want)
			}
		})
	}
}

// A step that short is only a problem for an expression that borrows it.
func TestAShortStepIsFineWithoutThePlaceholder(t *testing.T) {
	parse(t, `
source: {resolution: 5s}
defaults: {ranges: [{name: fine, from: -1h, step: 5s}]}
graphs: [{name: a, series: [{expr: 'rate(rx[1m])'}]}]
`)
}

// The label names a placeholder, and one name is already spoken for.
func TestRegionLabelCannotBeStep(t *testing.T) {
	if _, err := Parse([]byte("regions: {label: step}\n" + minimal)); err == nil {
		t.Error("expected an error")
	}
}

func TestPeakStepCanBeOverridden(t *testing.T) {
	c := parse(t, `
defaults:
  peak: true
  peak_step: 1m
  ranges:
    - {name: 1d, from: -1d, step: 5m}
    - {name: 1w, from: -7d, step: 30m, peak_step: 10m}
graphs:
  - name: a
    series: [{expr: up}]
`)
	g := c.Graphs[0]
	if g.Ranges[0].PeakStep != "1m" {
		t.Errorf("graph-wide override ignored: %q", g.Ranges[0].PeakStep)
	}
	if g.Ranges[1].PeakStep != "10m" {
		t.Errorf("per-range override ignored: %q", g.Ranges[1].PeakStep)
	}
}

// A drawing changes when its rightmost column does, and a column is one
// averaging step wide. So the interval a timescale is redrawn at follows its
// step: the yearly graph is not worth a query every fifteen seconds when the
// picture it would return next moves tomorrow.
func TestDrawingsAreCaptionedFromTheConfig(t *testing.T) {
	c := parse(t, `
regions:
  label: region
  titles: {tnn: core-tnn1}
defaults:
  ranges: [{name: 1d, from: -1d, step: 5m}]
graphs:
  - name: traffic
    title: HTTP traffic
    series: [{expr: 'up{region="$region"}', legend: RX}]
`)
	g := c.Graphs[0]

	named := g.Values(g.Ranges[0], "tnn", g.Theme, VariantPlain)
	if got := named.Get("title"); got != "HTTP traffic - core-tnn1" {
		t.Errorf("title = %q, want the configured name", got)
	}
	// The query still uses the label value, which is what the source knows.
	if got := named.Get("target"); got != `up{region="tnn"}` {
		t.Errorf("target = %q", got)
	}

	// A region the config does not name keeps its own.
	unnamed := g.Values(g.Ranges[0], "tyo", g.Theme, VariantPlain)
	if got := unnamed.Get("title"); got != "HTTP traffic - tyo" {
		t.Errorf("title = %q", got)
	}
}
