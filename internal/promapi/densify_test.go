package promapi

import (
	"math"
	"testing"
	"time"
)

func TestDensifyPlacesPointsInSlots(t *testing.T) {
	q := RangeQuery{Expr: "up", Start: time.Unix(1000, 0), End: time.Unix(1300, 0), Step: 60 * time.Second}

	// Points at 1000, 1060 and 1180: the slot for 1120 must become a NaN gap.
	raw := []rawSeries{{
		labels: map[string]string{"__name__": "up", "instance": "a"},
		points: []point{{t: 1000, v: 1}, {t: 1060, v: 2}, {t: 1180, v: 4}},
	}}

	got := densify(raw, q)
	if len(got) != 1 {
		t.Fatalf("got %d series, want 1", len(got))
	}
	d := got[0]

	if d.Start != 1000 || d.Stop != 1300 || d.Step != 60 {
		t.Errorf("got start=%d stop=%d step=%d, want 1000/1300/60", d.Start, d.Stop, d.Step)
	}
	if want := int((d.Stop - d.Start) / d.Step); len(d.Values) != want {
		t.Fatalf("got %d values, want %d", len(d.Values), want)
	}
	if d.Values[0] != 1 || d.Values[1] != 2 || d.Values[3] != 4 {
		t.Errorf("points landed in the wrong slots: %v", d.Values)
	}
	for _, slot := range []int{2, 4} {
		if !math.IsNaN(d.Values[slot]) {
			t.Errorf("slot %d should be NaN, got %v", slot, d.Values[slot])
		}
	}
	if d.Labels["instance"] != "a" {
		t.Errorf("labels not carried through: %v", d.Labels)
	}
}

func TestDensifyFillsPartialTrailingSlot(t *testing.T) {
	// An end that is not a whole number of steps past start must still produce a
	// complete final slot rather than truncating it.
	q := RangeQuery{Expr: "up", Start: time.Unix(1000, 0), End: time.Unix(1310, 0), Step: 60 * time.Second}
	d := densify([]rawSeries{{points: []point{{t: 1000, v: 7}}}}, q)[0]

	if (d.Stop-d.Start)%d.Step != 0 {
		t.Errorf("window %d..%d is not a whole number of %ds steps", d.Start, d.Stop, d.Step)
	}
	if len(d.Values) != int((d.Stop-d.Start)/d.Step) {
		t.Errorf("got %d values for window %d..%d", len(d.Values), d.Start, d.Stop)
	}
	if d.Values[0] != 7 {
		t.Errorf("sample at start should land in slot 0, got %v", d.Values)
	}
}

func TestDensifyDropsOutOfRangePoints(t *testing.T) {
	q := RangeQuery{Expr: "up", Start: time.Unix(1000, 0), End: time.Unix(1120, 0), Step: 60 * time.Second}
	d := densify([]rawSeries{{points: []point{{t: 100, v: 1}, {t: 99999, v: 2}}}}, q)[0]
	for i, v := range d.Values {
		if !math.IsNaN(v) {
			t.Errorf("slot %d should be NaN, got %v", i, v)
		}
	}
}
