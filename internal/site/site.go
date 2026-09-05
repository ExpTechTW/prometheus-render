// Package site draws a whole config's worth of graphs and writes the pages
// that present them, in the shape MRTG made familiar: one row per graph on the
// front page, and a page per graph showing the same data over widening
// timescales.
//
// The work is spread across cores. One image is one job, and the jobs are
// independent, so a site of eight graphs over four timescales is thirty-two
// pieces of work rather than one long serial pass.
package site

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/ExpTechTW/prometheus-render/internal/config"
	"github.com/ExpTechTW/prometheus-render/internal/params"
	"github.com/ExpTechTW/prometheus-render/internal/promapi"
	"github.com/ExpTechTW/prometheus-render/internal/render"
)

// Site draws the graphs described by a config into a directory.
type Site struct {
	Cfg    *config.Config
	Client *promapi.Client
	Log    *log.Logger
}

// job is one image: one graph at one timescale.
type job struct {
	graph *config.Graph
	rng   config.Range
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

// pass draws every image once and reports how it went.
func (s *Site) pass(ctx context.Context) error {
	start := time.Now()
	err := s.Render(ctx)
	took := time.Since(start).Round(time.Millisecond)
	switch {
	case ctx.Err() != nil:
		return nil
	case err != nil:
		s.logf("drew %d images with errors in %s: %v", len(s.jobs()), took, err)
	default:
		s.logf("drew %d images in %s", len(s.jobs()), took)
	}
	return err
}

// Render draws every graph at every timescale and writes the pages.
func (s *Site) Render(ctx context.Context) error {
	jobs := s.jobs()
	if len(jobs) == 0 {
		return errors.New("nothing to draw")
	}
	if err := os.MkdirAll(s.Cfg.Output.Dir, 0o755); err != nil {
		return err
	}
	for _, g := range s.Cfg.Graphs {
		if err := os.MkdirAll(filepath.Join(s.Cfg.Output.Dir, g.Name), 0o755); err != nil {
			return err
		}
	}

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

// jobs lists every image the config asks for, in config order.
func (s *Site) jobs() []job {
	var out []job
	for _, g := range s.Cfg.Graphs {
		for _, r := range g.Ranges {
			out = append(out, job{graph: g, rng: r})
		}
	}
	return out
}

// draw resolves one job through the same params layer the CLI and the HTTP
// endpoint use, then writes the image.
func (s *Site) draw(ctx context.Context, j job) error {
	g, err := params.Build(j.graph.Values(j.rng), params.Defaults{}, time.Now())
	if err != nil {
		return fmt.Errorf("%s/%s: %w", j.graph.Name, j.rng.Name, err)
	}
	img, err := render.Draw(ctx, s.Client, g)
	if err != nil {
		return fmt.Errorf("%s/%s: %w", j.graph.Name, j.rng.Name, err)
	}
	return writeAtomic(filepath.Join(s.Cfg.Output.Dir, imagePath(j.graph.Name, j.rng.Name)), img)
}

// imagePath is where one graph's timescale lives, relative to the output
// directory. It doubles as the src the pages use.
func imagePath(graph, rng string) string { return graph + "/" + rng + ".png" }

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

func (s *Site) logf(format string, args ...any) {
	if s.Log != nil {
		s.Log.Printf(format, args...)
	}
}
