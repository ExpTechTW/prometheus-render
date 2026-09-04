// Package graph ties the Prometheus query layer to the renderer: it resolves the
// time window, fetches every target in parallel and names the resulting series.
package query

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/ExpTechTW/prometheus-render/internal/promapi"
	"github.com/ExpTechTW/prometheus-render/internal/series"
)

// DefaultMaxPoints mirrors the sample ceiling Prometheus enforces per range
// query; exceeding it makes the server reject the request outright.
const DefaultMaxPoints = 11000

// Target is one PromQL expression plus the legend format for its series.
type Target struct {
	Expr   string
	Legend string
}

// Request describes everything needed to produce one graph.
type Request struct {
	Targets   []Target
	From      time.Time
	Until     time.Time
	Step      time.Duration // zero means "derive from width"
	Width     int
	MaxPoints int
}

// ResolveStep returns the step actually used, applying the auto-resolution
// heuristic and the sample ceiling.
func (r *Request) ResolveStep() time.Duration {
	max := r.MaxPoints
	if max <= 0 {
		max = DefaultMaxPoints
	}

	step := r.Step
	if step <= 0 {
		step = promapi.AutoStep(r.From, r.Until, r.Width)
	}
	if step <= 0 {
		step = time.Minute
	}

	// Widen the step rather than let the server reject an over-long range.
	span := r.Until.Sub(r.From)
	if n := span / step; n > time.Duration(max) {
		step = (span / time.Duration(max)).Round(time.Second)
		if step <= 0 {
			step = time.Second
		}
	}
	return step
}

// Build runs every target against the API and returns the combined series in
// target order.
func (r *Request) Build(ctx context.Context, c *promapi.Client) ([]series.Series, error) {
	if len(r.Targets) == 0 {
		return nil, fmt.Errorf("no query given")
	}
	if !r.Until.After(r.From) {
		return nil, fmt.Errorf("empty time range: until (%s) must be after from (%s)",
			r.Until.Format(time.RFC3339), r.From.Format(time.RFC3339))
	}

	step := r.ResolveStep()
	from, until := promapi.AlignRange(r.From, r.Until, step)

	perTarget := make([][]series.Series, len(r.Targets))
	errs := make([]error, len(r.Targets))

	var wg sync.WaitGroup
	for i, t := range r.Targets {
		wg.Add(1)
		go func(i int, t Target) {
			defer wg.Done()
			q := promapi.RangeQuery{Expr: t.Expr, Start: from, End: until, Step: step}
			found, err := c.QueryRange(ctx, q)
			if err != nil {
				errs[i] = err
				return
			}
			for j := range found {
				found[j].Name = series.FormatLegend(t.Legend, found[j].Labels)
			}
			perTarget[i] = found
		}(i, t)
	}
	wg.Wait()

	for _, err := range errs {
		if err != nil {
			return nil, err
		}
	}

	var out []series.Series
	for _, s := range perTarget {
		out = append(out, s...)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("query returned no series")
	}
	return out, nil
}
