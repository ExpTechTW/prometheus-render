// Package config reads the YAML file that describes a site: where the samples
// come from, which graphs to draw, and over which timescales.
//
// A graph here does not carry its own rendering semantics. Every field is
// flattened back into the same key/value form the CLI flags already use, and
// handed to the params package, so a setting means the same thing whether it
// arrives on the command line or in a file.
package config

import (
	"bytes"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/ExpTechTW/prometheus-render/internal/promapi"
	"github.com/ExpTechTW/prometheus-render/internal/query"
)

// stepPlaceholder is the name an expression writes to be given the sampling
// interval of the trace being drawn: $step, or ${step}.
const stepPlaceholder = "step"

// maxSubqueryPoints is how many points a source will walk for one subquery per
// series -- VictoriaMetrics spells it -search.maxPointsSubqueryPerTimeseries.
// A peak step fine enough to pass it is refused at load, rather than turning
// into a failed query hours later on a graph nobody is watching.
const maxSubqueryPoints = 100000

// Config is a whole site.
type Config struct {
	Source   Source   `yaml:"source"`
	Output   Output   `yaml:"output"`
	Regions  Regions  `yaml:"regions"`
	Defaults Defaults `yaml:"defaults"`
	Graphs   []*Graph `yaml:"graphs"`
}

// Regions splits the site along the values of one label, so the same graphs
// can be read either way round: everything about one place, or one thing
// across every place.
//
// The values are discovered from the data rather than listed here, so a node
// arriving or leaving changes the pages without anyone editing a file.
type Regions struct {
	// Label is split on, and names the placeholder the expressions use: a site
	// split by "region" writes $region in its queries.
	Label string `yaml:"label"`

	// Match restricts discovery to the series matching a selector, e.g.
	// `{job="nginx"}`. Without it every value of the label is used.
	Match string `yaml:"match"`

	// Titles gives friendlier names. A value not listed keeps its own.
	Titles map[string]string `yaml:"titles"`
}

// Split reports whether the site is divided by region at all.
func (c *Config) Split() bool { return c.Regions.Label != "" }

// Region is one value the site is split by, in its two guises: the label value
// the queries need, and the name the config gives it. The name is what appears
// on the pages, in the paths and on the drawings; the value stays behind in
// the query, where the source knows it.
type Region struct {
	Value string
	Name  string
}

// Region pairs a discovered label value with the name to present it under.
func (c *Config) Region(value string) Region {
	if t, ok := c.Regions.Titles[value]; ok && t != "" {
		return Region{Value: value, Name: t}
	}
	return Region{Value: value, Name: value}
}

// Location is the timezone the site reads in: the one its graphs are drawn in,
// so the time under a page agrees with the time along its axes.
func (c *Config) Location() *time.Location {
	if c.Defaults.TZ == "" {
		return time.Local
	}
	loc, err := time.LoadLocation(c.Defaults.TZ)
	if err != nil {
		return time.Local
	}
	return loc
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

	// Resolution is how often this source scrapes, and it is what $step is
	// measured against. rate() needs two samples to report anything, so the
	// shortest window an expression can usefully ask for is twice this, and
	// the finest timescale takes its peaks there rather than at its own step.
	// Left unset, none of that applies and the peaks ladder as they always
	// have.
	Resolution Duration `yaml:"resolution"`
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
	// Peak adds a second trace per series showing the highest value in each
	// sample bucket rather than the mean, the way MRTG draws its peaks.
	Peak *bool `yaml:"peak"`

	// PeakStep is the resolution those peaks are taken at. It defaults to the
	// step of the timescale one finer, so the weekly graph peaks at the daily
	// graph's resolution -- again as MRTG does it -- and the finest one peaks
	// at the source's own scrape interval.
	PeakStep string `yaml:"peak_step"`

	// Theme is the light one; DarkTheme is what the page switches to. Both are
	// drawn from the same samples, so the second costs no extra query.
	Theme     string `yaml:"theme"`
	DarkTheme string `yaml:"dark_theme"`

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

	// PeakStep is the resolution this timescale's peaks are taken at. Left
	// unset it becomes the step of the timescale one finer, which is what
	// MRTG does: the yearly graph peaks at the monthly graph's resolution.
	// The finest timescale has nothing below it and peaks at two scrapes of
	// source.resolution instead, or at its own step when that is unset.
	//
	// That also keeps the subquery bounded. A year of daily buckets sampled
	// every five minutes is a hundred thousand points per series, which real
	// servers refuse.
	PeakStep string `yaml:"peak_step"`

	// Every is how often this timescale is redrawn. One plot column is one
	// step, so a graph averaging in eight-hour buckets gains a column every
	// eight hours: redrawing it every fifteen seconds spends a query to change
	// nothing anybody can see. Left unset it becomes the step itself, which is
	// the widest interval at which every pass still shows something new.
	//
	// It is rounded up to a whole multiple of output.interval, because a pass
	// is the only moment anything is redrawn. That also lets a page work out
	// the same boundaries from the same number, as it already does for the
	// interval.
	Every Duration `yaml:"every"`
}

// Graph is one drawing, rendered once per timescale.
type Graph struct {
	Name   string   `yaml:"name"` // names the page and the files
	Title  string   `yaml:"title"`
	VLabel string   `yaml:"vtitle"`
	Series []Series `yaml:"series"`

	// Global keeps this graph whole instead of drawing it once per region.
	Global bool `yaml:"global"`

	// OnlyRegions limits the graph to these regions. Empty means all of them.
	// Useful for something that exists in one place, like a single sensor.
	OnlyRegions []string `yaml:"only_regions"`

	// Anything below overrides Defaults for this graph alone.
	Peak      *bool    `yaml:"peak"`
	PeakStep  string   `yaml:"peak_step"`
	Theme     string   `yaml:"theme"`
	DarkTheme string   `yaml:"dark_theme"`
	Width     int      `yaml:"width"`
	Height    int      `yaml:"height"`
	Area      string   `yaml:"area"`
	Zoom      float64  `yaml:"zoom"`
	TZ        string   `yaml:"tz"`
	Base      int      `yaml:"base"`
	YMin      *float64 `yaml:"y_min"`
	YMax      *float64 `yaml:"y_max"`
	Ranges    []Range  `yaml:"ranges"`

	// Every overrides how often one timescale of this graph is redrawn, keyed
	// by range name: {1d: 1m, 1y: 12h}. It exists so a graph worth watching
	// closely can say so without restating the whole ladder of ranges, and a
	// name it does not have is refused rather than quietly ignored.
	Every map[string]Duration `yaml:"every"`

	// label is the region label, copied in so Values can find the placeholder.
	// titles is the names to present regions under, so a drawing is captioned
	// the way the config asks rather than with the raw label value.
	label  string            `yaml:"-"`
	titles map[string]string `yaml:"-"`
}

// Series is one expression drawn on a graph.
type Series struct {
	Expr   string `yaml:"expr"`
	Legend string `yaml:"legend"` // {{label}} placeholders, as on the CLI

	// PeakExpr draws this series' peak trace instead of the subquery over
	// Expr. It is written whole, max_over_time and all, so $step in it is the
	// bucket being reduced into rather than the resolution of the samples
	// inside it.
	//
	// It exists because the peak ladder runs out of budget at the long end. A
	// yearly graph peaks at forty-minute samples, since a year of ten-second
	// ones is three million points per series, so a five-second burst reaches
	// its peak trace four hundred and eighty times smaller than it was. A
	// recording rule that keeps the highest ten-second rate of every five
	// minutes has already taken that maximum at the source's own resolution,
	// and max_over_time(rule[$step]) reads it back whole at any timescale.
	PeakExpr string `yaml:"peak_expr"`

	// PeakRanges limits PeakExpr to these timescales, by range name. Empty
	// means all of them. A recording rule is usually coarser than the finest
	// timescale, so this is how the fine end goes on reading raw samples --
	// where the ladder is already at the source's resolution and has nothing
	// to gain -- while the long end reads the rule.
	PeakRanges []string `yaml:"peak_ranges"`
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

// reserved are the directories the two index views live in, so nothing else
// may claim them.
var reserved = map[string]bool{"region": true, "graph": true, "index": true}

// SafeRegion reports whether a discovered label value can be placed in a
// quoted PromQL matcher without reshaping the query around it.
//
// A value carrying a quote, a backslash, a newline or a brace is refused
// rather than escaped: real infrastructure labels never contain them, and a
// label value trying to close a string is not a region.
// SafePath reports whether a name can stand as a path segment.
func SafePath(v string) bool { return nameRE.MatchString(v) && !reserved[v] }

func SafeRegion(v string) bool {
	return v != "" && !strings.ContainsAny(v, "\"\\\n\r{}") && !reserved[v]
}

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

	if c.Defaults.TZ != "" {
		if _, err := time.LoadLocation(c.Defaults.TZ); err != nil {
			return nil, fmt.Errorf("config: defaults.tz: %w", err)
		}
	}

	if c.Regions.Label != "" && !nameRE.MatchString(c.Regions.Label) {
		return nil, fmt.Errorf("config: regions.label %q is not a label name", c.Regions.Label)
	}
	// The label names a placeholder the expressions write, and that one name
	// is already taken by the timescale's own step.
	if c.Regions.Label == stepPlaceholder {
		return nil, fmt.Errorf(
			"config: regions.label %q collides with $%s, which carries the timescale's own step",
			c.Regions.Label, stepPlaceholder)
	}
	// A region's name is what its pages are filed under, so it has to be
	// usable as a path segment. Saying so here beats a name that silently
	// becomes something else in the URL.
	for value, name := range c.Regions.Titles {
		if !nameRE.MatchString(name) || reserved[name] {
			return nil, fmt.Errorf(
				"config: regions.titles[%s] = %q: a name is used in the path, so it must be "+
					"letters, digits, dot, dash or underscore", value, name)
		}
	}

	if len(c.Graphs) == 0 {
		return nil, fmt.Errorf("config: no graphs defined")
	}
	seen := make(map[string]bool, len(c.Graphs))
	for i, g := range c.Graphs {
		g.label = c.Regions.Label
		g.titles = c.Regions.Titles
		if err := g.normalise(c.Defaults,
			time.Duration(c.Source.Resolution), time.Duration(c.Output.Interval)); err != nil {
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
// res is how often the source scrapes, zero when the config does not say: it
// is the floor every window and every peak step is measured against. every is
// the site's redraw interval, which each timescale's own is rounded up to a
// multiple of.
func (g *Graph) normalise(d Defaults, res, every time.Duration) error {
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

	if !nameRE.MatchString(g.Name) || reserved[g.Name] {
		return fmt.Errorf("name %q is reserved", g.Name)
	}
	if g.Peak == nil {
		g.Peak = d.Peak
	}
	if g.PeakStep == "" {
		g.PeakStep = d.PeakStep
	}
	if g.Theme == "" {
		g.Theme = d.Theme
	}
	if g.DarkTheme == "" {
		g.DarkTheme = d.DarkTheme
	}
	if g.DarkTheme == "" {
		g.DarkTheme = "dark"
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
		// A copy, not the slice itself. The fields filled in below are worked
		// out from the graph -- its peak step, its refresh interval -- and a
		// shared slice would let the first graph's answers land on every other
		// graph that inherited the same ranges.
		g.Ranges = append([]Range(nil), d.Ranges...)
	}
	if g.Title == "" {
		g.Title = g.Name
	}

	if g.TZ != "" {
		if _, err := time.LoadLocation(g.TZ); err != nil {
			return fmt.Errorf("tz: %w", err)
		}
	}

	for name := range g.Every {
		if !g.hasRange(name) {
			return fmt.Errorf("every: %q is not one of this graph's timescales (%s)",
				name, strings.Join(g.rangeNames(), ", "))
		}
	}

	for i, s := range g.Series {
		if s.PeakExpr == "" {
			if len(s.PeakRanges) != 0 {
				return fmt.Errorf("series %d: peak_ranges names timescales but there is no "+
					"peak_expr to put on them", i)
			}
			continue
		}
		if !g.peaks() {
			return fmt.Errorf("series %d: peak_expr on a graph that draws no peaks", i)
		}
		for _, name := range s.PeakRanges {
			if !g.hasRange(name) {
				return fmt.Errorf("series %d: peak_ranges: %q is not one of this graph's "+
					"timescales (%s)", i, name, strings.Join(g.rangeNames(), ", "))
			}
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
		from, err := promapi.ParseTime(defaulted(r.From, "-1d"), now)
		if err != nil {
			return fmt.Errorf("range %q: from: %w", r.Name, err)
		}
		until := now
		if r.Until != "" {
			if until, err = promapi.ParseTime(r.Until, now); err != nil {
				return fmt.Errorf("range %q: until: %w", r.Name, err)
			}
		}
		span := until.Sub(from)

		var step time.Duration
		if r.Step != "" {
			if step, err = promapi.ParseStep(r.Step); err != nil {
				return fmt.Errorf("range %q: step: %w", r.Name, err)
			}
		} else if g.peaks() {
			return fmt.Errorf("range %q: peak needs an explicit step", r.Name)
		}
		// Past the ceiling the query package widens the step rather than let
		// the query fail, which would leave a $step window describing a
		// resolution the drawing is no longer at. Say so here instead.
		if step > 0 && span/step > query.DefaultMaxPoints {
			return fmt.Errorf("range %q: step %s over %s is %d points, past the %d a source "+
				"returns for one query; widen the step",
				r.Name, r.Step, span, span/step, query.DefaultMaxPoints)
		}
		if err := g.tooFineFor(res, r.Name, "step", r.Step, step); err != nil {
			return err
		}

		if v, ok := g.Every[r.Name]; ok {
			r.Every = v
		}
		r.Every = Duration(refreshOf(r.Every.Duration(), step, every))

		if g.peaks() {
			// Each timescale peaks at the resolution of the one below it, so
			// a bucket holds a handful of samples rather than thousands.
			switch {
			case r.PeakStep != "":
			case g.PeakStep != "":
				r.PeakStep = g.PeakStep
			case i > 0:
				r.PeakStep = g.Ranges[i-1].Step
			case res >= time.Second && 2*res < step:
				// The finest timescale has nothing below it to peak at, so it
				// peaks at the source's own resolution -- doubled, because
				// rate() needs two samples to report anything. Peaking at its
				// own step, which is what this did before the source's
				// resolution was known, puts one sample in each bucket: a
				// peak trace identical to the average drawn over it.
				r.PeakStep = stepOf(2 * res)
			default:
				// Already drawn as finely as the source is told apart. There
				// is nothing below to peak at, and the average is the peak.
				r.PeakStep = r.Step
			}
			peak, err := promapi.ParseStep(r.PeakStep)
			if err != nil {
				return fmt.Errorf("range %q: peak_step: %w", r.Name, err)
			}
			// Only what is still drawn from the subquery is held to its
			// budget. A series reading a rule instead is reading a maximum
			// already taken somewhere else, at whatever resolution that took
			// it, and none of this applies to it.
			if g.laddersAt(r.Name) {
				if span/peak > maxSubqueryPoints {
					return fmt.Errorf("range %q: peaking over %s every %s is %d points per series, "+
						"past the %d a source returns for one subquery; widen peak_step "+
						"or give the series a peak_expr",
						r.Name, span, r.PeakStep, span/peak, maxSubqueryPoints)
				}
				if err := g.tooFineFor(res, r.Name, "peak_step", r.PeakStep, peak); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// The two ways a graph is drawn. A page offers the choice as a button, so both
// are rendered when a graph asks for peaks.
const (
	VariantPlain = "plain"
	VariantPeak  = "peak"
)

// ThemeChoice pairs the name a page switches by with the palette it draws.
type ThemeChoice struct{ Name, Theme string }

// Themes lists the palettes this graph is drawn in. Both come from the same
// samples, so the second costs no query.
func (g *Graph) Themes() []ThemeChoice {
	return []ThemeChoice{{"light", g.Theme}, {"dark", g.DarkTheme}}
}

// Variants lists the ways this graph is drawn.
func (g *Graph) Variants() []string {
	if g.peaks() {
		return []string{VariantPlain, VariantPeak}
	}
	return []string{VariantPlain}
}

// Peaks reports whether this graph carries MRTG's peak traces.
func (g *Graph) Peaks() bool { return g.peaks() }

// peaks reports whether this graph carries MRTG's peak traces.
func (g *Graph) peaks() bool { return g.Peak != nil && *g.Peak }

// InRegion reports whether this graph is drawn for the given region. A global
// graph belongs to no region and is drawn once.
func (g *Graph) InRegion(region string) bool {
	if g.Global {
		return region == ""
	}
	if region == "" {
		return false
	}
	if len(g.OnlyRegions) == 0 {
		return true
	}
	for _, r := range g.OnlyRegions {
		// Either guise will do: the label value as the data has it, or the
		// name the config gave it.
		if r == region || r == g.regionTitle(region) {
			return true
		}
	}
	return false
}

// Title is the name a region is presented under.
func (c *Config) Title(region string) string { return c.Region(region).Name }

// Values flattens one graph, in one region, at one timescale, drawn in one
// theme, into the settings the params package resolves -- the same form the CLI flags arrive
// in. Empty fields are left out so the fallbacks there still apply.
func (g *Graph) Values(r Range, region, theme, variant string) url.Values {
	v := url.Values{}

	// A legend is added for every target, empty or not, so the two lists stay
	// the same length and pair up by position.
	for _, s := range g.Series {
		v.Add("target", g.expr(s.Expr, region, r.Step))
		v.Add("legend", s.Legend)
	}
	if variant == VariantPeak && g.peaks() {
		// The peaks follow the averages, so the palette hands them its third
		// and fourth colours -- which are named for exactly this -- and the
		// legend still reads in list order.
		for _, s := range g.Series {
			v.Add("target", g.peakTarget(s, r, region))
			v.Add("legend", peakLegend(s.Legend))
		}
		// Drawing them first puts them behind, so a peak shows only where it
		// rises above the average rather than covering it.
		v.Set("behindFrom", strconv.Itoa(len(g.Series)))
	}

	set(v, "from", defaulted(r.From, "-1d"))
	set(v, "until", r.Until)
	set(v, "step", r.Step)
	set(v, "title", g.pageTitle(region))
	set(v, "vtitle", g.VLabel)
	set(v, "theme", theme)
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

// pageTitle names the drawing, saying which region it is of when there is one.
//
// The region is named the way the config asks. The raw label value belongs in
// the query and the path, not on the picture.
func (g *Graph) pageTitle(region string) string {
	if region == "" {
		return g.Title
	}
	return g.Title + " - " + g.regionTitle(region)
}

// regionTitle is the name a region is presented under, falling back to the
// label value when the config gives it none.
func (g *Graph) regionTitle(region string) string {
	if t, ok := g.titles[region]; ok && t != "" {
		return t
	}
	return region
}

// expr puts the region and the trace's own sampling interval into an
// expression. The region placeholder is the label being split on, so a site
// split by "region" writes $region or ${region}; the interval is always
// $step.
//
// $step is what lets one line of config follow the timescale: the daily graph
// reads rate(x[5m]) where the yearly one reads rate(x[1d]), instead of a
// window written once flattening all four. A peak trace is handed the step its
// inner samples are taken at rather than the bucket they are reduced into, so
// its samples are as short as the source can tell apart -- which is the whole
// reason a peak is worth drawing next to an average.
//
// Nothing is escaped here: a region value that could reshape the query is
// refused by SafeRegion before it ever reaches this point, and a step is a
// duration this package has already parsed.
func (g *Graph) expr(e, region, step string) string {
	pairs := make([]string, 0, 8)
	if g.label != "" && region != "" {
		pairs = append(pairs, "${"+g.label+"}", region, "$"+g.label, region)
	}
	if step != "" {
		pairs = append(pairs, "${"+stepPlaceholder+"}", step, "$"+stepPlaceholder, step)
	}
	if len(pairs) == 0 {
		return e
	}
	return strings.NewReplacer(pairs...).Replace(e)
}

// usesStep reports whether any of this graph's expressions asks to follow the
// timescale, which is what makes the resolution floor below worth checking.
func (g *Graph) usesStep() bool {
	for _, s := range g.Series {
		if hasStep(s.Expr) || hasStep(s.PeakExpr) {
			return true
		}
	}
	return false
}

func hasStep(e string) bool {
	return strings.Contains(e, "$"+stepPlaceholder) ||
		strings.Contains(e, "${"+stepPlaceholder+"}")
}

// peakTarget is what a peak trace is drawn from: the series' own expression
// when it names one for this timescale, and otherwise the average reduced to
// the highest sample in each bucket.
func (g *Graph) peakTarget(s Series, r Range, region string) string {
	if !s.peaksAt(r.Name) {
		return peakExpr(g.expr(s.Expr, region, r.PeakStep), r.Step, r.PeakStep)
	}
	// Given whole, and given the bucket rather than the peak step: what it
	// reads has already taken its maximum at the resolution it was recorded
	// at, and a subquery over that would step over the maxima in between --
	// sampling a five-minute rule every forty minutes keeps one value in
	// eight and discards the other seven, which is the opposite of a peak.
	return g.expr(s.PeakExpr, region, r.Step)
}

// laddersAt reports whether any series still takes its peak from the subquery
// over its average at this timescale, which is what the ladder's budget is
// spent on.
func (g *Graph) laddersAt(name string) bool {
	for _, s := range g.Series {
		if !s.peaksAt(name) {
			return true
		}
	}
	return false
}

// peaksAt reports whether this series draws its peak from PeakExpr at the
// named timescale.
func (s Series) peaksAt(name string) bool {
	if s.PeakExpr == "" {
		return false
	}
	if len(s.PeakRanges) == 0 {
		return true
	}
	for _, r := range s.PeakRanges {
		if r == name {
			return true
		}
	}
	return false
}

// tooFineFor refuses a step that would expand $step into a window the source
// cannot have put two samples in. rate() over a single sample reports nothing
// at all, so the graph comes out empty -- and empty looks like an outage, from
// nowhere near the setting that caused it.
func (g *Graph) tooFineFor(res time.Duration, rangeName, field, spelt string, d time.Duration) error {
	if res <= 0 || d <= 0 || d >= 2*res || !g.usesStep() {
		return nil
	}
	return fmt.Errorf(
		"range %q: %s: %s is under two scrapes of the source's %s, so a $%s window holds at "+
			"most one sample and rate() reports nothing; use %s or more",
		rangeName, field, spelt, stepOf(res), stepPlaceholder, stepOf(2*res))
}

// refreshOf is how often one timescale is redrawn.
//
// Unset, it is the step: one plot column is one step, so redrawing faster than
// that repaints the same picture with at most its rightmost column moved, and
// the yearly graph in particular would spend a query every interval for a
// change nobody can see until tomorrow.
//
// Whatever it comes to, it is rounded up to a whole multiple of the site's
// interval, since a pass is the only moment anything is redrawn. That keeps
// the boundaries a page counts down to exactly the boundaries a pass lands on,
// which is the arrangement the interval already has.
func refreshOf(every, step, interval time.Duration) time.Duration {
	if every <= 0 {
		every = step
	}
	if interval <= 0 {
		// Drawn once, by cron or by hand. Nothing is on a timer to skip.
		return every
	}
	if every <= interval {
		return interval
	}
	return (every + interval - 1) / interval * interval
}

func (g *Graph) hasRange(name string) bool {
	for _, r := range g.Ranges {
		if r.Name == name {
			return true
		}
	}
	return false
}

func (g *Graph) rangeNames() []string {
	out := make([]string, 0, len(g.Ranges))
	for _, r := range g.Ranges {
		out = append(out, r.Name)
	}
	return out
}

// stepOf spells a duration the way a step is written. Whole seconds is the
// form that reads back: ParseStep takes a single-unit offset, so a duration
// that prints itself as "1m30s" would not survive the round trip -- and a
// source scraping every 30s would have produced exactly that.
func stepOf(d time.Duration) string {
	return strconv.FormatInt(int64(d/time.Second), 10) + "s"
}

// peakExpr wraps an expression so it reports the highest value in each sample
// bucket rather than the mean. A subquery does that for any expression without
// having to parse it.
func peakExpr(e, step, inner string) string {
	return "max_over_time((" + e + ")[" + step + ":" + inner + "])"
}

func peakLegend(legend string) string {
	if legend == "" {
		return "peak"
	}
	return legend + " peak"
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
