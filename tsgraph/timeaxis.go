package tsgraph

import "time"

// The time axis works the way the vertical one does: walk a ladder of intervals
// people actually read time in, and take the first rung whose labels have room
// to sit side by side. Calendar units are counted, not converted to a duration,
// so months keep their own lengths and a day survives a clock change.

type unit int

const (
	secondly unit = iota
	minutely
	hourly
	daily
	weekly
	monthly
	yearly
)

// rung is one step of the ladder: how far apart the labels sit, how finely the
// grid is divided between them, and how a label is written.
type rung struct {
	labelUnit  unit
	labelEvery int
	gridUnit   unit
	gridEvery  int
	format     string
	// chars is how wide the rendered label is, in characters. A rung is only
	// taken if its labels have that much room, so a short format like "%H:%M"
	// can sit closer together than "%a %H:%M".
	chars int
	// approxSec is the rung's nominal length, used to order the ladder and to
	// estimate label spacing.
	approxSec float64
}

// ladder runs from a second to a year. Each rung divides its label interval
// into a handful of gridlines, which is what makes the grid readable without a
// second table to look them up in.
var ladder = []rung{
	{secondly, 1, secondly, 1, "%H:%M:%S", 8, 1},
	{secondly, 5, secondly, 1, "%H:%M:%S", 8, 5},
	{secondly, 15, secondly, 5, "%H:%M:%S", 8, 15},
	{secondly, 30, secondly, 10, "%H:%M:%S", 8, 30},
	{minutely, 1, secondly, 15, "%H:%M", 5, 60},
	{minutely, 5, minutely, 1, "%H:%M", 5, 300},
	{minutely, 15, minutely, 5, "%H:%M", 5, 900},
	{minutely, 30, minutely, 10, "%H:%M", 5, 1800},
	{hourly, 1, minutely, 15, "%H:%M", 5, 3600},
	{hourly, 2, minutely, 30, "%H:%M", 5, 7200},
	{hourly, 3, hourly, 1, "%a %H:%M", 9, 10800},
	{hourly, 6, hourly, 1, "%a %H:%M", 9, 21600},
	{hourly, 12, hourly, 3, "%a %H:%M", 9, 43200},
	{daily, 1, hourly, 6, "%d %b", 6, 86400},
	{daily, 2, hourly, 12, "%d %b", 6, 172800},
	{weekly, 1, daily, 1, "%d %b", 6, 604800},
	{weekly, 2, daily, 2, "%d %b", 6, 1209600},
	{monthly, 1, weekly, 1, "%b %Y", 8, 2592000},
	{monthly, 2, weekly, 1, "%b %Y", 8, 5184000},
	{monthly, 3, monthly, 1, "%b %Y", 8, 7776000},
	{monthly, 6, monthly, 1, "%b %Y", 8, 15552000},
	{yearly, 1, monthly, 3, "%Y", 4, 31536000},
	{yearly, 2, monthly, 6, "%Y", 4, 63072000},
	{yearly, 5, yearly, 1, "%Y", 4, 157680000},
}

// A label needs room for its own text plus a gap, at roughly the width of the
// 10pt monospaced face the graph is drawn with.
const (
	charPx = 6
	gapPx  = 16
)

// needPx is how much room a rung's labels need before the next one starts.
func (r rung) needPx() float64 { return float64(r.chars*charPx + gapPx) }

// selectRung takes the first rung whose labels fit, so the axis stays readable
// at any zoom without the caller choosing an interval.
func selectRung(start, end int64, xsize int) rung {
	span := float64(end - start)
	if span <= 0 || xsize <= 0 {
		return ladder[0]
	}
	perSec := float64(xsize) / span
	for _, r := range ladder {
		if r.approxSec*perSec >= r.needPx() {
			return r
		}
	}
	return ladder[len(ladder)-1]
}

// truncate rounds t down to the start of the unit it falls in.
func truncate(t time.Time, u unit) time.Time {
	y, mo, d := t.Date()
	loc := t.Location()
	switch u {
	case secondly:
		return t.Truncate(time.Second)
	case minutely:
		return time.Date(y, mo, d, t.Hour(), t.Minute(), 0, 0, loc)
	case hourly:
		return time.Date(y, mo, d, t.Hour(), 0, 0, 0, loc)
	case daily:
		return time.Date(y, mo, d, 0, 0, 0, 0, loc)
	case weekly:
		day := time.Date(y, mo, d, 0, 0, 0, 0, loc)
		return day.AddDate(0, 0, -((int(day.Weekday()) + 6) % 7)) // back to Monday
	case monthly:
		return time.Date(y, mo, 1, 0, 0, 0, 0, loc)
	case yearly:
		return time.Date(y, 1, 1, 0, 0, 0, 0, loc)
	}
	return t
}

// advance steps t forward by n units, through the calendar where that matters.
func advance(t time.Time, u unit, n int) time.Time {
	switch u {
	case secondly:
		return t.Add(time.Duration(n) * time.Second)
	case minutely:
		return t.Add(time.Duration(n) * time.Minute)
	case hourly:
		return t.Add(time.Duration(n) * time.Hour)
	case daily:
		return t.AddDate(0, 0, n)
	case weekly:
		return t.AddDate(0, 0, 7*n)
	case monthly:
		return t.AddDate(0, n, 0)
	case yearly:
		return t.AddDate(n, 0, 0)
	}
	return t
}

// align moves t to the first boundary at or after it that is a whole multiple
// of every units, so ticks land on round times rather than on the window edge.
func align(t time.Time, u unit, every int) time.Time {
	t = truncate(t, u)
	if every <= 1 {
		return t
	}
	var n int
	switch u {
	case secondly:
		n = t.Second()
	case minutely:
		n = t.Minute()
	case hourly:
		n = t.Hour()
	case daily:
		n = t.Day() - 1
	case monthly:
		n = int(t.Month()) - 1
	case yearly:
		n = t.Year()
	default:
		return t
	}
	if r := n % every; r != 0 {
		t = advance(t, u, every-r)
	}
	return t
}

// tick is one position on the time axis.
type tick struct {
	At    time.Time
	Major bool   // drawn more heavily than the plain grid
	Label string // empty when this tick carries no label
}

// timeTicks returns the gridlines and labels for a window, in loc.
func timeTicks(start, end int64, xsize int, loc *time.Location) []tick {
	r := selectRung(start, end, xsize)
	from := time.Unix(start, 0).In(loc)
	until := time.Unix(end, 0).In(loc)

	labels := map[int64]string{}
	for t := align(from, r.labelUnit, r.labelEvery); !t.After(until); t = advance(t, r.labelUnit, r.labelEvery) {
		labels[t.Unix()] = strftime(t, r.format)
	}

	var out []tick
	seen := map[int64]bool{}
	for t := align(from, r.gridUnit, r.gridEvery); !t.After(until); t = advance(t, r.gridUnit, r.gridEvery) {
		u := t.Unix()
		if u <= start {
			continue
		}
		seen[u] = true
		out = append(out, tick{At: t, Major: labels[u] != "", Label: labels[u]})
	}
	// A label can fall between two gridlines; it still gets a tick of its own.
	for u, lbl := range labels {
		if u > start && u <= end && !seen[u] {
			out = append(out, tick{At: time.Unix(u, 0).In(loc), Major: true, Label: lbl})
		}
	}
	return out
}
