// Package site draws a whole config's worth of graphs and writes the pages
// that present them, in the shape MRTG made familiar: widening timescales for
// one thing, and an index onto everything.
//
// A site split by region is readable both ways round -- everything about one
// place, or one thing across every place -- and every drawing exists in a
// light and a dark palette, with and without MRTG's peak traces, so the page
// can offer those as buttons rather than as separate sites.
//
// The work is spread across cores. One fetch is one job, and the jobs are
// independent, so a site of many graphs is many pieces of work rather than one
// long serial pass. Each fetch yields several images, because a palette and a
// peak trace change how samples are drawn, not which samples are read.
package site

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ExpTechTW/prometheus-render/internal/config"
	"github.com/ExpTechTW/prometheus-render/internal/params"
	"github.com/ExpTechTW/prometheus-render/internal/promapi"
	"github.com/ExpTechTW/prometheus-render/internal/query"
	"github.com/ExpTechTW/prometheus-render/internal/render"
)

// Site draws the graphs a config describes into a directory.
type Site struct {
	Cfg    *config.Config
	Client *promapi.Client
	Log    *log.Logger

	// regions is what the last pass discovered to split by; drawn records the
	// pairs that produced images, so the pages list only what exists.
	mu      sync.Mutex
	regions []config.Region
	drawn   map[string]bool
	noData  map[string]bool
	failed  map[string]bool
}

// job is one fetch: one graph, in one region, at one timescale.
type job struct {
	region config.Region // zero for a graph that is not split
	graph  *config.Graph
	rng    config.Range
}

func (j job) String() string {
	if j.region.Name == "" {
		return j.graph.Name + "/" + j.rng.Name
	}
	return j.region.Name + "/" + j.graph.Name + "/" + j.rng.Name
}

// Run draws the site once, then again on every tick until ctx is cancelled.
// An interval of zero draws once and returns, which is the shape cron wants;
// there the error is passed back so the exit status reflects the drawing.
func (s *Site) Run(ctx context.Context) error {
	err := s.pass(ctx)
	every := s.Cfg.Output.Interval.Duration()
	if every <= 0 {
		return err
	}

	s.logf("redrawing every %s", every)
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			// A Ticker holds at most one pending tick, so a pass that overruns
			// its interval loses the ticks it ran through instead of queueing
			// them up behind itself.
			//
			// A failed pass is not fatal once on a timer: the source may be
			// briefly unreachable, and the next tick tries again.
			_ = s.pass(ctx)
		}
	}
}

// pass draws everything once and reports how it went.
func (s *Site) pass(ctx context.Context) error {
	start := time.Now()
	err := s.Render(ctx)
	took := time.Since(start).Round(time.Millisecond)
	switch {
	case ctx.Err() != nil:
		return nil
	case err != nil:
		s.logf("drew with errors in %s: %v", took, err)
	default:
		s.logf("drew %d images from %d queries in %s", s.imageCount(), len(s.jobs()), took)
	}
	return err
}

// Render draws everything the config asks for and writes the pages.
func (s *Site) Render(ctx context.Context) error {
	if s.Cfg.Split() {
		found, err := s.discover(ctx)
		if err != nil {
			return fmt.Errorf("discovering %s: %w", s.Cfg.Regions.Label, err)
		}
		s.mu.Lock()
		s.regions = found
		s.mu.Unlock()
	}

	jobs := s.jobs()
	if len(jobs) == 0 {
		return errors.New("nothing to draw")
	}
	for _, j := range jobs {
		for _, t := range j.graph.Themes() {
			for _, v := range j.graph.Variants() {
				dir := filepath.Join(s.Cfg.Output.Dir, filepath.FromSlash(imageDir(j.region.Name, j.graph.Name, t.Name, v)))
				if err := os.MkdirAll(dir, 0o755); err != nil {
					return err
				}
			}
		}
	}

	s.mu.Lock()
	s.drawn = map[string]bool{}
	s.noData = map[string]bool{}
	s.failed = map[string]bool{}
	s.mu.Unlock()

	// Errors are collected by index rather than through a channel, so a
	// failing graph does not stop the others from being drawn.
	errs := make([]error, len(jobs))

	workers := s.Cfg.Output.Workers
	if workers <= 0 {
		workers = runtime.NumCPU()
	}
	if workers > len(jobs) {
		workers = len(jobs)
	}

	queue := make(chan int)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for k := range queue {
				errs[k] = s.draw(ctx, jobs[k])
			}
		}()
	}
	for k := range jobs {
		select {
		case queue <- k:
		case <-ctx.Done():
			close(queue)
			wg.Wait()
			return ctx.Err()
		}
	}
	close(queue)
	wg.Wait()

	// The pages are written even when some images failed, so the site still
	// reflects the config instead of vanishing on a transient query error.
	if err := s.writePages(); err != nil {
		return err
	}
	return errors.Join(errs...)
}

// discover finds the values the site is split by. A value that could reshape a
// query is dropped with a note rather than used.
func (s *Site) discover(ctx context.Context) ([]config.Region, error) {
	found, err := s.Client.LabelValues(ctx, s.Cfg.Regions.Label, s.Cfg.Regions.Match, time.Time{}, time.Time{})
	if err != nil {
		return nil, err
	}
	out := make([]config.Region, 0, len(found))
	for _, v := range found {
		r := s.Cfg.Region(v)
		// A value the config names is safe by construction: its name was
		// checked at load. One it does not name has to stand on its own.
		if !config.SafeRegion(v) || (r.Name == v && !config.SafePath(v)) {
			s.logf("ignoring %s=%q: not usable as a name; give it one under regions.titles",
				s.Cfg.Regions.Label, v)
			continue
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// jobs lists every fetch the config asks for, in config order.
func (s *Site) jobs() []job {
	var out []job
	for _, g := range s.Cfg.Graphs {
		for _, region := range s.scope(g) {
			for _, r := range g.Ranges {
				out = append(out, job{region: region, graph: g, rng: r})
			}
		}
	}
	return out
}

// scope lists the regions a graph is drawn for. A global graph, or any graph
// on a site that is not split, is drawn once with no region.
func (s *Site) scope(g *config.Graph) []config.Region {
	if g.Global || !s.Cfg.Split() {
		return []config.Region{{}}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []config.Region
	for _, r := range s.regions {
		if g.InRegion(r.Value) {
			out = append(out, r)
		}
	}
	return out
}

// imageCount is how many files a full pass writes.
func (s *Site) imageCount() int {
	n := 0
	for _, j := range s.jobs() {
		n += len(j.graph.Themes()) * len(j.graph.Variants())
	}
	return n
}

// draw runs one job's query and writes every image it feeds.
func (s *Site) draw(ctx context.Context, j job) error {
	// The peaks are separate targets that follow the averages, so one fetch of
	// the fullest variant covers both: the plain one is the leading groups.
	widest := config.VariantPlain
	if j.graph.Peaks() {
		widest = config.VariantPeak
	}
	fetchWith, err := params.Build(
		j.graph.Values(j.rng, j.region.Value, j.graph.Theme, widest), params.Defaults{}, time.Now())
	if err != nil {
		return fmt.Errorf("%s: %w", j, err)
	}

	grouped, err := render.Fetch(ctx, s.Client, fetchWith)
	switch {
	case errors.Is(err, query.ErrNoSeries):
		// A graph can legitimately have nothing in one region: a sensor that
		// lives in one place, a node only just added. Say so and carry on
		// rather than failing the pass, and leave it off the pages.
		s.logf("no data: %s", j)
		s.mark(&s.noData, j.region.Name, j.graph.Name)
		return nil
	case err != nil:
		s.mark(&s.failed, j.region.Name, j.graph.Name)
		return fmt.Errorf("%s: %w", j, err)
	}

	plainGroups := min(len(j.graph.Series), len(grouped))
	for _, v := range j.graph.Variants() {
		data := grouped
		if v == config.VariantPlain {
			data = grouped[:plainGroups]
		}
		flat := query.Flatten(data)
		if len(flat) == 0 {
			continue
		}
		for _, t := range j.graph.Themes() {
			g, err := params.Build(j.graph.Values(j.rng, j.region.Value, t.Theme, v), params.Defaults{}, time.Now())
			if err != nil {
				return fmt.Errorf("%s: %w", j, err)
			}
			img, err := render.Draw(g, flat)
			if err != nil {
				return fmt.Errorf("%s: %w", j, err)
			}
			p := filepath.Join(s.Cfg.Output.Dir,
				filepath.FromSlash(imagePath(j.region.Name, j.graph.Name, t.Name, v, j.rng.Name)))
			if err := writeAtomic(p, img); err != nil {
				return err
			}
		}
	}

	s.mark(&s.drawn, j.region.Name, j.graph.Name)
	return nil
}

func (s *Site) mark(set *map[string]bool, region, graph string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	(*set)[region+"\x00"+graph] = true
}

// has reports whether a pair has anything to show.
//
// Images left by an earlier pass count, and so does a query that errored: an
// error means the answer is unknown, so the graph stays listed and its stale
// or missing image is itself the signal. A pair the source answered for with
// nothing does not count -- that is a graph which has genuinely gone, not one
// that failed to refresh.
func (s *Site) has(region string, g *config.Graph) bool {
	key := region + "\x00" + g.Name
	s.mu.Lock()
	drawn, gone, failed := s.drawn[key], s.noData[key], s.failed[key]
	s.mu.Unlock()
	if drawn || failed {
		return true
	}
	if gone || len(g.Ranges) == 0 {
		return false
	}
	p := filepath.Join(s.Cfg.Output.Dir,
		filepath.FromSlash(imagePath(region, g.Name, "light", config.VariantPlain, g.Ranges[0].Name)))
	_, err := os.Stat(p)
	return err == nil
}

// imageBase is the part of an image's path that a page holds on to. The page
// appends the palette, the variant and the timescale itself, which is how a
// button can switch between them without another page load.
func imageBase(region, graph string) string {
	if region == "" {
		return graph
	}
	return region + "/" + graph
}

func imageDir(region, graph, theme, variant string) string {
	return imageBase(region, graph) + "/" + theme + "/" + variant
}

func imagePath(region, graph, theme, variant, rng string) string {
	return imageDir(region, graph, theme, variant) + "/" + rng + ".png"
}

// detailPath is the page showing every timescale of one drawing.
func detailPath(region, graph string) string {
	return imageBase(region, graph) + ".html"
}

// upTo is the prefix a page at the given path needs to reach the site root.
func upTo(page string) string {
	return strings.Repeat("../", strings.Count(page, "/"))
}

// writeAtomic writes through a temporary file in the same directory. A browser
// fetching the site mid-render then gets either the previous image or the new
// one, never half of either.
func writeAtomic(path string, b []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// ensureDir makes a page's directory, which the image directories may not have
// created -- region/ and graph/ hold no images.
func ensureDir(dir string) error { return os.MkdirAll(dir, 0o755) }

func (s *Site) logf(format string, args ...any) {
	if s.Log != nil {
		s.Log.Printf(format, args...)
	}
}
