package query

import (
	"testing"
	"time"
)

func TestResolveStepHonoursExplicitStep(t *testing.T) {
	now := time.Now()
	r := &Request{From: now.Add(-time.Hour), Until: now, Step: 30 * time.Second, Width: 600}
	if got := r.ResolveStep(); got != 30*time.Second {
		t.Errorf("ResolveStep = %s, want 30s", got)
	}
}

func TestResolveStepAutoFromWidth(t *testing.T) {
	now := time.Now()
	r := &Request{From: now.Add(-6 * time.Hour), Until: now, Width: 600}
	step := r.ResolveStep()
	if step <= 0 {
		t.Fatalf("ResolveStep = %s", step)
	}
	if n := int(6 * time.Hour / step); n > 2*600 {
		t.Errorf("auto step %s yields %d points for a 600px graph", step, n)
	}
}

func TestResolveStepClampsToMaxPoints(t *testing.T) {
	now := time.Now()
	// One second over a year would be ~31.5M samples; the step must widen.
	r := &Request{From: now.AddDate(-1, 0, 0), Until: now, Step: time.Second, Width: 600, MaxPoints: 1000}
	step := r.ResolveStep()
	if n := int(now.Sub(now.AddDate(-1, 0, 0)) / step); n > 1000 {
		t.Errorf("step %s still yields %d points, want <= 1000", step, n)
	}
}

func TestResolveStepNeverZero(t *testing.T) {
	now := time.Now()
	for _, r := range []*Request{
		{From: now, Until: now, Width: 600},
		{From: now, Until: now.Add(time.Millisecond), Width: 600, MaxPoints: 10},
	} {
		if got := r.ResolveStep(); got <= 0 {
			t.Errorf("ResolveStep = %s, want > 0", got)
		}
	}
}
