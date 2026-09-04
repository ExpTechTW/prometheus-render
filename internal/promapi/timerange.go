package promapi

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// relRE matches Graphite/RRDtool style relative offsets such as "-1h", "-90min",
// "now-7d" or "+30m".
var relRE = regexp.MustCompile(`^([-+])\s*(\d+)\s*([a-zA-Z]+)$`)

// unitSeconds maps the time-unit spellings accepted by rrdtool, MRTG and the
// Graphite render API onto seconds.
var unitSeconds = map[string]int64{
	"s": 1, "sec": 1, "secs": 1, "second": 1, "seconds": 1,
	"m": 60, "min": 60, "mins": 60, "minute": 60, "minutes": 60,
	"h": 3600, "hr": 3600, "hrs": 3600, "hour": 3600, "hours": 3600,
	"d": 86400, "day": 86400, "days": 86400,
	"w": 604800, "wk": 604800, "wks": 604800, "week": 604800, "weeks": 604800,
	"mon": 2592000, "month": 2592000, "months": 2592000,
	"y": 31536000, "yr": 31536000, "year": 31536000, "years": 31536000,
}

// absLayouts are tried in order when the input is neither relative nor a Unix
// timestamp.
var absLayouts = []string{
	time.RFC3339,
	"2006-01-02T15:04:05",
	"2006-01-02 15:04:05",
	"2006-01-02 15:04",
	"2006-01-02",
	"15:04_20060102",
	"20060102",
}

// ParseTime resolves one endpoint of a graph window relative to now. It accepts
// "now", relative offsets ("-1h", "now-90min"), Unix timestamps and a handful of
// absolute layouts.
func ParseTime(s string, now time.Time) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, fmt.Errorf("empty time")
	}

	base := now
	if lower := strings.ToLower(s); strings.HasPrefix(lower, "now") {
		rest := strings.TrimSpace(s[3:])
		if rest == "" {
			return base, nil
		}
		// "now-1h" -> fall through with "-1h".
		s = rest
	}

	if d, err := ParseDuration(s); err == nil {
		return base.Add(d), nil
	}

	// Bare Unix timestamp (seconds), optionally fractional.
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		sec, frac := int64(f), f-float64(int64(f))
		return time.Unix(sec, int64(frac*1e9)), nil
	}

	for _, layout := range absLayouts {
		if t, err := time.ParseInLocation(layout, s, base.Location()); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("cannot parse time %q", s)
}

// ParseDuration parses a signed, single-unit offset such as "-1h" or "+30min".
// Unlike time.ParseDuration it understands days, weeks, months and years, which
// is what the RRD-style tools use for graph windows.
func ParseDuration(s string) (time.Duration, error) {
	m := relRE.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return 0, fmt.Errorf("not a relative offset: %q", s)
	}
	n, err := strconv.ParseInt(m[2], 10, 64)
	if err != nil {
		return 0, err
	}
	secs, ok := unitSeconds[strings.ToLower(m[3])]
	if !ok {
		return 0, fmt.Errorf("unknown time unit %q", m[3])
	}
	d := time.Duration(n*secs) * time.Second
	if m[1] == "-" {
		d = -d
	}
	return d, nil
}

// ParseStep parses an unsigned resolution such as "60", "60s" or "5min".
func ParseStep(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty step")
	}
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		if n <= 0 {
			return 0, fmt.Errorf("step must be positive, got %q", s)
		}
		return time.Duration(n) * time.Second, nil
	}
	if !strings.HasPrefix(s, "+") && !strings.HasPrefix(s, "-") {
		s = "+" + s
	}
	d, err := ParseDuration(s)
	if err != nil {
		return 0, err
	}
	if d <= 0 {
		return 0, fmt.Errorf("step must be positive")
	}
	return d, nil
}

// AlignRange snaps a window outwards onto step boundaries. Sending an aligned
// window keeps Prometheus (which samples from start) and VictoriaMetrics (which
// snaps start to a step multiple) on the same grid, so returned timestamps land
// exactly on the slots the renderer is given.
func AlignRange(from, until time.Time, step time.Duration) (time.Time, time.Time) {
	secs := int64(step.Seconds())
	if secs < 1 {
		return from, until
	}
	start := from.Unix()
	if r := start % secs; r != 0 {
		if r < 0 {
			r += secs
		}
		start -= r
	}
	stop := until.Unix()
	if r := stop % secs; r != 0 {
		if r < 0 {
			r += secs
		}
		stop += secs - r
	}
	return time.Unix(start, 0), time.Unix(stop, 0)
}
