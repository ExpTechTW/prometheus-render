package tsgraph

import (
	"strconv"
	"strings"
	"time"
)

// strftime renders the C-style format strings the interval ladder carries. Only
// the directives the ladder uses are supported; anything else passes through so
// a typo shows up in the label rather than being silently dropped.
func strftime(t time.Time, format string) string {
	var b strings.Builder
	for i := 0; i < len(format); i++ {
		if format[i] != '%' || i+1 >= len(format) {
			b.WriteByte(format[i])
			continue
		}
		i++
		switch format[i] {
		case 'H':
			b.WriteString(pad2(t.Hour()))
		case 'M':
			b.WriteString(pad2(t.Minute()))
		case 'S':
			b.WriteString(pad2(t.Second()))
		case 'a':
			b.WriteString(t.Format("Mon"))
		case 'A':
			b.WriteString(t.Format("Monday"))
		case 'd':
			b.WriteString(pad2(t.Day()))
		case 'e':
			b.WriteString(strconv.Itoa(t.Day()))
		case 'b':
			b.WriteString(t.Format("Jan"))
		case 'B':
			b.WriteString(t.Format("January"))
		case 'm':
			b.WriteString(pad2(int(t.Month())))
		case 'Y':
			b.WriteString(strconv.Itoa(t.Year()))
		case 'y':
			b.WriteString(pad2(t.Year() % 100))
		case 'V':
			_, wk := t.ISOWeek()
			b.WriteString(pad2(wk))
		case 'g':
			yr, _ := t.ISOWeek()
			b.WriteString(pad2(yr % 100))
		case 'j':
			b.WriteString(strconv.Itoa(t.YearDay()))
		case 'p':
			if t.Hour() < 12 {
				b.WriteString("AM")
			} else {
				b.WriteString("PM")
			}
		case '%':
			b.WriteByte('%')
		default:
			b.WriteByte('%')
			b.WriteByte(format[i])
		}
	}
	return b.String()
}

func pad2(n int) string {
	if n < 10 && n >= 0 {
		return "0" + strconv.Itoa(n)
	}
	return strconv.Itoa(n)
}
