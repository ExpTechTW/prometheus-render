package promapi

import (
	"testing"
	"time"
)

var refNow = time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)

func TestParseTime(t *testing.T) {
	tests := []struct {
		in   string
		want time.Time
	}{
		{"now", refNow},
		{"-1h", refNow.Add(-time.Hour)},
		{"now-1h", refNow.Add(-time.Hour)},
		{"now -90min", refNow.Add(-90 * time.Minute)},
		{"-7d", refNow.AddDate(0, 0, -7)},
		{"-2w", refNow.AddDate(0, 0, -14)},
		{"+30m", refNow.Add(30 * time.Minute)},
		{"1735689600", time.Unix(1735689600, 0)},
		{"2026-01-02T03:04:05Z", time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)},
		{"2026-01-02 03:04:05", time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)},
		{"2026-01-02", time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)},
	}
	for _, tc := range tests {
		got, err := ParseTime(tc.in, refNow)
		if err != nil {
			t.Errorf("ParseTime(%q): unexpected error: %v", tc.in, err)
			continue
		}
		if !got.Equal(tc.want) {
			t.Errorf("ParseTime(%q) = %s, want %s", tc.in, got, tc.want)
		}
	}
}

func TestParseTimeErrors(t *testing.T) {
	for _, in := range []string{"", "bogus", "-1parsec", "yesterday"} {
		if _, err := ParseTime(in, refNow); err == nil {
			t.Errorf("ParseTime(%q): expected an error", in)
		}
	}
}

func TestParseStep(t *testing.T) {
	tests := map[string]time.Duration{
		"60":    time.Minute,
		"60s":   time.Minute,
		"5min":  5 * time.Minute,
		"1h":    time.Hour,
		"1d":    24 * time.Hour,
		"+300s": 5 * time.Minute,
	}
	for in, want := range tests {
		got, err := ParseStep(in)
		if err != nil {
			t.Errorf("ParseStep(%q): unexpected error: %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("ParseStep(%q) = %s, want %s", in, got, want)
		}
	}
	for _, in := range []string{"", "-60", "abc"} {
		if _, err := ParseStep(in); err == nil {
			t.Errorf("ParseStep(%q): expected an error", in)
		}
	}
}

func TestAutoStep(t *testing.T) {
	start := refNow.Add(-24 * time.Hour)
	step := AutoStep(start, refNow, 600)
	if step <= 0 {
		t.Fatalf("AutoStep returned %s", step)
	}
	// Roughly one sample per pixel, never fewer points than pixels by much.
	// Rounding down to a friendly interval must never leave a graph with fewer
	// points than it has pixels.
	if n := int(refNow.Sub(start) / step); n < 600 || n > 1800 {
		t.Errorf("AutoStep(24h, width 600) = %s giving %d points, want >= 600", step, n)
	}
	if AutoStep(refNow, refNow, 600) <= 0 {
		t.Error("AutoStep must return a positive step for an empty range")
	}
}
