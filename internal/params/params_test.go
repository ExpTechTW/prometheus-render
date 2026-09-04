package params

import (
	"net/url"
	"testing"
	"time"

	"github.com/ExpTechTW/prometheus-render/tsgraph"
)

var now = time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)

func build(t *testing.T, v url.Values, d Defaults) *Graph {
	t.Helper()
	g, err := Build(v, d, now)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return g
}

func TestBuildRequiresATarget(t *testing.T) {
	if _, err := Build(url.Values{}, Defaults{}, now); err == nil {
		t.Error("expected an error with no target")
	}
}

func TestBuildResolvesWindowAndTargets(t *testing.T) {
	g := build(t, url.Values{
		"target": {"up", "node_load1"}, "legend": {"a", "b"},
		"from": {"-6h"}, "until": {"now"}, "step": {"5min"},
	}, Defaults{})
	if got := g.Request.Until.Sub(g.Request.From); got != 6*time.Hour {
		t.Errorf("window = %s, want 6h", got)
	}
	if len(g.Request.Targets) != 2 || g.Request.Targets[1].Expr != "node_load1" ||
		g.Request.Targets[1].Legend != "b" {
		t.Errorf("targets = %+v", g.Request.Targets)
	}
	if g.Request.ResolveStep() != 5*time.Minute {
		t.Errorf("step = %s, want 5m", g.Request.ResolveStep())
	}
}

func TestBuildSharesOneLegendAcrossTargets(t *testing.T) {
	g := build(t, url.Values{"target": {"a", "b", "c"}, "legend": {"{{instance}}"}}, Defaults{})
	for i, tg := range g.Request.Targets {
		if tg.Legend != "{{instance}}" {
			t.Errorf("target %d legend = %q", i, tg.Legend)
		}
	}
}

func TestBuildMapsRenderOptions(t *testing.T) {
	g := build(t, url.Values{
		"target": {"up"}, "title": {"T"}, "vtitle": {"V"},
		"width": {"620"}, "height": {"250"}, "theme": {"dark"},
		"base": {"1024"}, "zoom": {"2"}, "yMin": {"0"}, "yMax": {"100"},
		"hideStats": {"1"}, "behindFrom": {"2"}, "tz": {"UTC"},
	}, Defaults{})
	o := g.Options
	if o.Title != "T" || o.VLabel != "V" || o.Width != 620 || o.Height != 250 {
		t.Errorf("basic fields: %+v", o)
	}
	if o.Base != 1024 || o.Zoom != 2 || o.BehindFrom != 2 || !o.HideStats {
		t.Errorf("numeric and boolean fields: %+v", o)
	}
	if o.YMin == nil || *o.YMin != 0 || o.YMax == nil || *o.YMax != 100 {
		t.Errorf("limits not mapped: %+v %+v", o.YMin, o.YMax)
	}
	if o.Location == nil || o.Location.String() != "UTC" {
		t.Errorf("timezone = %v", o.Location)
	}
	if o.Theme.Back != tsgraph.LookupTheme("dark").Back {
		t.Error("theme not applied")
	}
}

func TestBuildMapsAreaModeOntoEachSeries(t *testing.T) {
	for _, tc := range []struct {
		area string
		want []tsgraph.Kind
	}{
		{"", []tsgraph.Kind{tsgraph.Line, tsgraph.Line}},
		{"none", []tsgraph.Kind{tsgraph.Line, tsgraph.Line}},
		{"first", []tsgraph.Kind{tsgraph.Area, tsgraph.Line}},
		{"all", []tsgraph.Kind{tsgraph.Area, tsgraph.Area}},
		{"stacked", []tsgraph.Kind{tsgraph.Area, tsgraph.Stack}},
	} {
		g := build(t, url.Values{"target": {"a", "b"}, "area": {tc.area}}, Defaults{})
		for i := range tc.want {
			if g.Kinds[i] != tc.want[i] {
				t.Errorf("area %q series %d = %v, want %v", tc.area, i, g.Kinds[i], tc.want[i])
			}
		}
	}
}

func TestBuildFallsBackToDefaultsThenBuiltins(t *testing.T) {
	g := build(t, url.Values{"target": {"up"}}, Defaults{})
	if g.Options.Width != 400 || g.Options.Height != 175 {
		t.Errorf("built-in size = %dx%d, want 400x175", g.Options.Width, g.Options.Height)
	}
	if got := g.Request.Until.Sub(g.Request.From); got != time.Hour {
		t.Errorf("built-in window = %s, want 1h", got)
	}

	g = build(t, url.Values{"target": {"up"}}, Defaults{Width: 800, From: "-3h"})
	if g.Options.Width != 800 {
		t.Errorf("defaults width = %d", g.Options.Width)
	}
	if got := g.Request.Until.Sub(g.Request.From); got != 3*time.Hour {
		t.Errorf("defaults window = %s, want 3h", got)
	}

	g = build(t, url.Values{"target": {"up"}, "width": {"999"}}, Defaults{Width: 800})
	if g.Options.Width != 999 {
		t.Errorf("explicit width = %d, want 999", g.Options.Width)
	}
}

func TestBuildRejectsBadValues(t *testing.T) {
	for name, v := range map[string]url.Values{
		"from":  {"target": {"up"}, "from": {"bogus"}},
		"until": {"target": {"up"}, "until": {"bogus"}},
		"step":  {"target": {"up"}, "step": {"-5"}},
		"area":  {"target": {"up"}, "area": {"bogus"}},
		"tz":    {"target": {"up"}, "tz": {"Mars/Olympus"}},
	} {
		if _, err := Build(v, Defaults{}, now); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}
