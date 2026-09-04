package promapi

import (
	"math"
	"time"

	"github.com/ExpTechTW/prometheus-render/internal/series"
)

// densify converts the sparse, per-series JSON the Prometheus API returns into
// the fixed-step arrays the renderer needs, with NaN marking gaps. The window is
// expected to be step-aligned already (see AlignRange).
func densify(raw []rawSeries, q RangeQuery) []series.Series {
	start := q.Start.Unix()
	stop := q.End.Unix()
	step := int64(q.Step.Seconds())
	if step < 1 {
		step = 1
	}
	if rem := (stop - start) % step; rem != 0 {
		stop += step - rem
	}
	n := int((stop - start) / step)
	if n < 1 {
		n = 1
	}

	out := make([]series.Series, 0, len(raw))
	for _, r := range raw {
		values := make([]float64, n)
		for i := range values {
			values[i] = math.NaN()
		}
		for _, p := range r.points {
			idx := int((int64(math.Round(p.t)) - start) / step)
			if idx < 0 || idx >= n {
				continue
			}
			values[idx] = p.v
		}
		out = append(out, series.Series{
			Labels: r.labels,
			Start:  start,
			Stop:   stop,
			Step:   step,
			Values: values,
		})
	}
	return out
}

// AutoStep picks a resolution that yields roughly one sample per horizontal
// pixel, the same heuristic rrdtool uses, rounded down to a friendly interval so
// a graph never ends up with fewer points than it has pixels.
func AutoStep(start, end time.Time, width int) time.Duration {
	if width < 1 {
		width = 400
	}
	span := end.Sub(start)
	if span <= 0 {
		return time.Minute
	}
	ideal := span / time.Duration(width)

	nice := []time.Duration{
		time.Second, 5 * time.Second, 10 * time.Second, 15 * time.Second, 30 * time.Second,
		time.Minute, 2 * time.Minute, 5 * time.Minute, 10 * time.Minute, 15 * time.Minute, 30 * time.Minute,
		time.Hour, 2 * time.Hour, 3 * time.Hour, 6 * time.Hour, 12 * time.Hour,
		24 * time.Hour, 48 * time.Hour, 7 * 24 * time.Hour,
	}
	step := nice[0]
	for _, d := range nice {
		if d > ideal {
			break
		}
		step = d
	}
	return step
}
