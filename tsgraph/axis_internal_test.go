package tsgraph

import (
	"fmt"
	"math"
	"testing"
	"time"
)

func TestNiceNum(t *testing.T) {
	// Heckbert's rounding: every result is 1, 2, 5 or 10 times a power of ten.
	for _, x := range []float64{0.037, 1, 3.2, 7.9, 41.05, 124, 8600, 4.3e7} {
		for _, round := range []bool{false, true} {
			got := niceNum(x, round)
			f := got / math.Pow(10, math.Floor(math.Log10(got)))
			if d := math.Min(math.Min(math.Abs(f-1), math.Abs(f-2)), math.Abs(f-5)); d > 1e-9 {
				t.Errorf("niceNum(%g, %v) = %g, mantissa %g is not 1, 2 or 5", x, round, got, f)
			}
			if !round && got < x {
				t.Errorf("niceNum(%g, false) = %g, which is smaller", x, got)
			}
		}
	}
}

func TestResolveYRoundsOutwards(t *testing.T) {
	for _, tc := range []struct{ vals []float64 }{
		{[]float64{7.54, 41.05}}, {[]float64{0, 124.22}}, {[]float64{3.91, 12.28}},
		{[]float64{0.002, 0.037}}, {[]float64{1e6, 4.3e7}},
	} {
		y := resolveY(tc.vals, nil, nil, 1000, exponentUnset, 150, 8)
		lo, hi := tc.vals[0], tc.vals[len(tc.vals)-1]
		if y.Min > lo || y.Max < hi {
			t.Errorf("%v -> %g..%g, which clips the data", tc.vals, y.Min, y.Max)
		}
		if y.GridStep <= 0 {
			t.Fatalf("%v -> grid step %g", tc.vals, y.GridStep)
		}
		// The ends sit on whole steps, so the labels read as round numbers.
		for _, v := range []float64{y.Min, y.Max} {
			if r := math.Abs(math.Remainder(v, y.GridStep)); r > y.GridStep*1e-9 {
				t.Errorf("%v -> bound %g is not a multiple of the step %g", tc.vals, v, y.GridStep)
			}
		}
		if n := (y.Max - y.Min) / (y.GridStep * float64(y.LabFact)); n < 2 || n > 12 {
			t.Errorf("%v -> %.1f labels", tc.vals, n)
		}
	}
}

func TestResolveYBaselinesAtZero(t *testing.T) {
	// A series that never goes negative reads against zero, not its own floor.
	y := resolveY([]float64{7.54, 41.05}, nil, nil, 1000, exponentUnset, 150, 8)
	if y.Min != 0 {
		t.Errorf("min = %g, want 0", y.Min)
	}
	// Unless the caller pinned it, or the data goes below zero.
	pin := 5.0
	if y := resolveY([]float64{7.54, 41.05}, &pin, nil, 1000, exponentUnset, 150, 8); y.Min != 5 {
		t.Errorf("pinned min = %g, want 5", y.Min)
	}
	if y := resolveY([]float64{-3, 10}, nil, nil, 1000, exponentUnset, 150, 8); y.Min >= 0 {
		t.Errorf("negative data got min %g", y.Min)
	}
}

func TestMagnitude(t *testing.T) {
	for _, tc := range []struct {
		max     float64
		base    int
		wantF   float64
		wantSym rune
	}{
		{41.05, 1000, 1, ' '},
		{4.1e7, 1000, 1e6, 'M'},
		{2.0e10, 1024, 1073741824, 'G'},
		{0.5, 1000, 0.001, 'm'},
	} {
		f, sym := magnitude(0, tc.max, tc.base, exponentUnset)
		if math.Abs(f-tc.wantF)/tc.wantF > 1e-9 || sym != tc.wantSym {
			t.Errorf("magnitude(0..%g, base %d) = %g %q, want %g %q",
				tc.max, tc.base, f, sym, tc.wantF, tc.wantSym)
		}
	}
}

func TestSelectRungGivesReadableLabels(t *testing.T) {
	// Whatever the zoom, labels must not crowd: the chosen rung has to leave
	// at least minLabelPx between them.
	for _, span := range []int64{600, 32 * 3600, 8 * 86400, 35 * 86400, 396 * 86400, 5 * 365 * 86400} {
		for _, px := range []int{300, 500, 1200} {
			r := selectRung(0, span, px)
			gap := r.approxSec * float64(px) / float64(span)
			if gap < r.needPx()*0.9 && r.approxSec < ladder[len(ladder)-1].approxSec {
				t.Errorf("span %ds at %dpx: rung %q leaves %.0fpx between labels", span, px, r.format, gap)
			}
			if r.gridEvery <= 0 {
				t.Errorf("span %ds: rung %q has no grid subdivision", span, r.format)
			}
		}
	}
}

func TestTimeTicksLandOnRoundTimes(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Taipei")
	if err != nil {
		t.Skip("tzdata unavailable")
	}
	start := time.Date(2026, 9, 3, 10, 37, 0, 0, loc).Unix()
	end := start + 8*86400

	ticks := timeTicks(start, end, 500, loc)
	if len(ticks) == 0 {
		t.Fatal("no ticks")
	}
	labelled := 0
	for _, tk := range ticks {
		if tk.At.Unix() <= start || tk.At.Unix() > end {
			t.Errorf("tick %s outside the window", tk.At)
		}
		if tk.Label != "" {
			labelled++
			if tk.At.Hour() != 0 || tk.At.Minute() != 0 {
				t.Errorf("labelled tick %s is not midnight", tk.At)
			}
		}
	}
	if labelled < 6 || labelled > 10 {
		t.Errorf("%d labels over 8 days, want about one a day", labelled)
	}
}

func TestTimeTicksSurviveADSTChange(t *testing.T) {
	// Counting calendar units rather than adding durations keeps a daily tick
	// on midnight across a clock change.
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skip("tzdata unavailable")
	}
	start := time.Date(2026, 3, 6, 0, 0, 0, 0, loc).Unix() // DST begins 8 March
	end := start + 8*86400
	for _, tk := range timeTicks(start, end, 500, loc) {
		if tk.Label != "" && (tk.At.Hour() != 0 || tk.At.Minute() != 0) {
			t.Errorf("labelled tick %s drifted off midnight", tk.At)
		}
	}
}

func TestStrftimeCoversTheLadderDirectives(t *testing.T) {
	tm := time.Date(2026, 9, 4, 7, 5, 9, 0, time.UTC)
	for format, want := range map[string]string{
		"%H:%M:%S": "07:05:09", "%H:%M": "07:05", "%a %H:%M": "Fri 07:05",
		"%d %b": "04 Sep", "%b %Y": "Sep 2026", "%Y": "2026",
		"100%% sure": "100% sure",
	} {
		if got := strftime(tm, format); got != want {
			t.Errorf("strftime(%q) = %q, want %q", format, got, want)
		}
	}
}

func TestDrawOrderPutsTrailingSeriesBehind(t *testing.T) {
	for _, tc := range []struct {
		n, behind int
		want      string
	}{
		{4, 2, "[2 3 0 1]"}, {4, 0, "[0 1 2 3]"},
		{2, 2, "[0 1]"}, {2, 5, "[0 1]"}, {3, 1, "[1 2 0]"},
	} {
		if got := fmt.Sprint(drawOrder(tc.n, tc.behind)); got != tc.want {
			t.Errorf("drawOrder(%d,%d) = %s, want %s", tc.n, tc.behind, got, tc.want)
		}
	}
}

func TestZoomKeepsProportions(t *testing.T) {
	for _, z := range []float64{1, 2, 3} {
		sc := func(v int) int { return int(float64(v)*z + 0.5) }
		on, off := sc(gridDashOn), sc(gridDashOff)
		if on == 0 || off == 0 {
			t.Errorf("zoom %g: dash pattern collapsed to %d:%d", z, on, off)
		}
		if ratio := float64(on) / float64(on+off); math.Abs(ratio-0.5) > 0.2 {
			t.Errorf("zoom %g: dash duty cycle %.2f drifted", z, ratio)
		}
	}
}
