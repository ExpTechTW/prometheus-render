// Package config reads the YAML file that describes a site: where the samples
// come from, which graphs to draw, and over which timescales.
//
// A graph here does not carry its own rendering semantics. Every field is
// flattened back into the same key/value form the CLI flags and the HTTP
// parameters already use, and handed to the params package, so a setting means
// the same thing however it arrives.
package config

import (
	"bytes"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/ExpTechTW/prometheus-render/internal/promapi"
)

// Config is a whole site.
type Config struct {
	Source   Source   `yaml:"source"`
	Output   Output   `yaml:"output"`
	Defaults Defaults `yaml:"defaults"`
	Graphs   []*Graph `yaml:"graphs"`
}

// Source is the Prometheus-compatible endpoint the samples are read from.
type Source struct {
	URL      string            `yaml:"url"`
	Timeout  Duration          `yaml:"timeout"`
	User     string            `yaml:"user"` // "name:password"
	Headers  map[string]string `yaml:"headers"`
	Insecure bool              `yaml:"insecure"`

	// MaxQueries bounds how many queries are in flight at once. A render fans
	// out over every graph and timescale together, which would otherwise reach
	// the server as one burst.
	MaxQueries int `yaml:"max_queries"`
}

// Output is where the site is written and how often it is redrawn.
type Output struct {
	Dir   string `yaml:"dir"`
	Title string `yaml:"title"`

	// Listen serves the directory over HTTP as well as writing it. Empty
	// writes the files and nothing more, for serving with nginx or similar.
	Listen string `yaml:"listen"`

	// Interval redraws the site on a timer. Zero draws once and exits, which
	// is the shape cron wants.
	Interval Duration `yaml:"interval"`

	// Workers is how many graphs are drawn at once. Zero means one per CPU.
	Workers int `yaml:"workers"`
}

// Defaults are inherited by every graph, and overridden by any graph that sets
// the same field.
type Defaults struct {
	Theme  string  `yaml:"theme"`
	Width  int     `yaml:"width"`
	Height int     `yaml:"height"`
	Area   string  `yaml:"area"`
	Zoom   float64 `yaml:"zoom"`
	TZ     string  `yaml:"tz"`
	Base   int     `yaml:"base"`
	Ranges []Range `yaml:"ranges"`
}

// Range is one timescale a graph is drawn over: MRTG's daily, weekly, monthly
// and yearly row.
type Range struct {
	Name  string `yaml:"name"`  // names the file, so it must be path-safe
	Title string `yaml:"title"` // heading above the image
	From  string `yaml:"from"`
	Until string `yaml:"until"`
	Step  string `yaml:"step"`
}

// Graph is one drawing, rendered once per timescale.
type Graph struct {
	Name   string   `yaml:"name"` // names the page and the files
	Title  string   `yaml:"title"`
	VLabel string   `yaml:"vtitle"`
	Series []Series `yaml:"series"`

	// Anything below overrides Defaults for this graph alone.
	Theme  string   `yaml:"theme"`
	Width  int      `yaml:"width"`
	Height int      `yaml:"height"`
	Area   string   `yaml:"area"`
	Zoom   float64  `yaml:"zoom"`
	TZ     string   `yaml:"tz"`
	Base   int      `yaml:"base"`
	YMin   *float64 `yaml:"y_min"`
	YMax   *float64 `yaml:"y_max"`
	Ranges []Range  `yaml:"ranges"`
}

// Series is one expression drawn on a graph.
type Series struct {
	Expr   string `yaml:"expr"`
	Legend string `yaml:"legend"` // {{label}} placeholders, as on the CLI
}

// DefaultRanges are MRTG's four timescales, in the order it draws them.
var DefaultRanges = []Range{
	{Name: "1d", Title: "Daily (5 min average)", From: "-1d", Step: "5m"},
	{Name: "1w", Title: "Weekly (30 min average)", From: "-7d", Step: "30m"},
	{Name: "1m", Title: "Monthly (2 hour average)", From: "-30d", Step: "2h"},
	{Name: "1y", Title: "Yearly (1 day average)", From: "-365d", Step: "1d"},
}

// Duration is a time.Duration written the way YAML users expect: "30s", "5m".
type Duration time.Duration

// UnmarshalYAML accepts a duration string, or a bare number of seconds.
func (d *Duration) UnmarshalYAML(n *yaml.Node) error {
	var s string
	if err := n.Decode(&s); err != nil {
		return err
	}
	if v, err := time.ParseDuration(s); err == nil {
		*d = Duration(v)
		return nil
	}
	// A bare number is read as seconds, which is what "300" ought to mean.
	if secs, err := strconv.Atoi(s); err == nil {
		*d = Duration(time.Duration(secs) * time.Second)
		return nil
	}
	return fmt.Errorf("invalid duration %q", s)
}

// Duration returns the value as a time.Duration.
func (d Duration) Duration() time.Duration { return time.Duration(d) }

// nameRE guards the strings that become file names.
var nameRE = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`)

// Load reads and validates a config file.
func Load(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(b)
}

// Parse reads and validates a config. Everything that could fail later in a
// worker -- an unparsable window, an unknown area mode, a name that is not
// safe as a file -- is rejected here instead, so a typo surfaces at startup
// rather than halfway through the first render.
func Parse(b []byte) (*Config, error) {
	c := &Config{}
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true) // a misspelt key is a mistake, not a silent no-op
	if err := dec.Decode(c); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}

	if c.Source.URL == "" {
		c.Source.URL = "http://localhost:9090"
	}
	if c.Source.Timeout == 0 {
		c.Source.Timeout = Duration(30 * time.Second)
	}
	if c.Source.MaxQueries <= 0 {
		c.Source.MaxQueries = 8
	}
	if c.Output.Dir == "" {
		c.Output.Dir = "site"
	}
	if c.Output.Title == "" {
		c.Output.Title = "prometheus-render"
	}
	if len(c.Defaults.Ranges) == 0 {
		c.Defaults.Ranges = DefaultRanges
	}

	if len(c.Graphs) == 0 {
		return nil, fmt.Errorf("config: no graphs defined")
	}
	seen := make(map[string]bool, len(c.Graphs))
	for i, g := range c.Graphs {
		if err := g.normalise(c.Defaults); err != nil {
			return nil, fmt.Errorf("config: graph %d: %w", i, err)
		}
		if seen[g.Name] {
			return nil, fmt.Errorf("config: duplicate graph name %q", g.Name)
		}
		seen[g.Name] = true
	}
	return c, nil
}

// normalise fills a graph in from the defaults and checks it can be drawn.
func (g *Graph) normalise(d Defaults) error {
	if !nameRE.MatchString(g.Name) {
		return fmt.Errorf("name %q must be a path-safe name, e.g. \"traffic\"", g.Name)
	}
	if len(g.Series) == 0 {
		return fmt.Errorf("no series")
	}
	for i, s := range g.Series {
		if s.Expr == "" {
			return fmt.Errorf("series %d: empty expr", i)
		}
	}

	if g.Theme == "" {
		g.Theme = d.Theme
	}
	if g.Width == 0 {
		g.Width = d.Width
	}
	if g.Height == 0 {
		g.Height = d.Height
	}
	if g.Area == "" {
		g.Area = d.Area
	}
	if g.Zoom == 0 {
		g.Zoom = d.Zoom
	}
	if g.TZ == "" {
		g.TZ = d.TZ
	}
	if g.Base == 0 {
		g.Base = d.Base
	}
	if len(g.Ranges) == 0 {
		g.Ranges = d.Ranges
	}
	if g.Title == "" {
		g.Title = g.Name
	}

	if g.TZ != "" {
		if _, err := time.LoadLocation(g.TZ); err != nil {
			return fmt.Errorf("tz: %w", err)
		}
	}

	now := time.Now()
	seen := make(map[string]bool, len(g.Ranges))
	for i := range g.Ranges {
		r := &g.Ranges[i]
		if !nameRE.MatchString(r.Name) {
			return fmt.Errorf("range %d: name %q must be a path-safe name", i, r.Name)
		}
		if seen[r.Name] {
			return fmt.Errorf("duplicate range name %q", r.Name)
		}
		seen[r.Name] = true
		if r.Title == "" {
			r.Title = r.Name
		}
		if _, err := promapi.ParseTime(defaulted(r.From, "-1d"), now); err != nil {
			return fmt.Errorf("range %q: from: %w", r.Name, err)
		}
		if r.Until != "" {
			if _, err := promapi.ParseTime(r.Until, now); err != nil {
				return fmt.Errorf("range %q: until: %w", r.Name, err)
			}
		}
		if r.Step != "" {
			if _, err := promapi.ParseStep(r.Step); err != nil {
				return fmt.Errorf("range %q: step: %w", r.Name, err)
			}
		}
	}
	return nil
}

// Values flattens one graph at one timescale into the settings the params
// package resolves, which is the same form the CLI flags and the URL
// parameters arrive in. Empty fields are left out so the fallbacks there
// still apply.
func (g *Graph) Values(r Range) url.Values {
	v := url.Values{}
	// A legend is added for every target, empty or not, so that the two lists
	// stay the same length and pair up by position.
	for _, s := range g.Series {
		v.Add("target", s.Expr)
		v.Add("legend", s.Legend)
	}

	set(v, "from", defaulted(r.From, "-1d"))
	set(v, "until", r.Until)
	set(v, "step", r.Step)
	set(v, "title", g.Title)
	set(v, "vtitle", g.VLabel)
	set(v, "theme", g.Theme)
	set(v, "area", g.Area)
	set(v, "tz", g.TZ)
	setNum(v, "width", g.Width)
	setNum(v, "height", g.Height)
	setNum(v, "base", g.Base)
	if g.Zoom != 0 {
		v.Set("zoom", strconv.FormatFloat(g.Zoom, 'f', -1, 64))
	}
	if g.YMin != nil {
		v.Set("yMin", strconv.FormatFloat(*g.YMin, 'f', -1, 64))
	}
	if g.YMax != nil {
		v.Set("yMax", strconv.FormatFloat(*g.YMax, 'f', -1, 64))
	}
	return v
}

func set(v url.Values, key, val string) {
	if val != "" {
		v.Set(key, val)
	}
}

func setNum(v url.Values, key string, n int) {
	if n != 0 {
		v.Set(key, strconv.Itoa(n))
	}
}

func defaulted(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
