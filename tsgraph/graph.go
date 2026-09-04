package tsgraph

import (
	"bytes"
	"fmt"
	"image/color"
	"image/png"
	"math"
	"time"

	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gomono"
	"golang.org/x/image/font/opentype"
)

// Coverage of each line where it crosses the data. Opaque lines over a filled
// area cut it into bands, so they are blended instead. The labelled lines are
// laid on more heavily than the unlabelled ones: those are the ones a value is
// actually read against.
const (
	minorAlpha = 0.45
	majorAlpha = 0.65
	ruleAlpha  = 0.80
)

// Dash patterns, in on:off pixels. The grid is dashed so it reads as a
// reference rather than as part of the plot.
const (
	gridDashOn, gridDashOff = 1, 1
	ruleDashOn, ruleDashOff = 2, 3
)

// Kind is how one series is drawn.
type Kind int

const (
	// Line draws the series as a stroked polyline.
	Line Kind = iota
	// Area fills between the series and the bottom of the scale.
	Area
	// Stack fills between the running total of the series before it and that
	// total plus this one.
	Stack
)

// Series is one metric to plot: samples on a fixed grid, with NaN marking a
// gap, plus how to draw them.
//
// Values[i] is the sample at Start + i*Step. The series ends at
// Start + len(Values)*Step.
type Series struct {
	Name   string
	Start  int64 // Unix seconds of the first sample
	Step   int64 // seconds between samples
	Values []float64

	Colour color.RGBA
	Kind   Kind
	Width  float64 // line width in nominal pixels; zero means one
}

// Stop returns the end of the series, one step past the last sample.
func (s Series) Stop() int64 { return s.Start + int64(len(s.Values))*s.Step }

// Options configures one graph.
type Options struct {
	Title      string
	VLabel     string
	Width      int // plot canvas, as rrdtool counts it
	Height     int
	Theme      Theme
	Location   *time.Location
	Base       int // 1000 or 1024
	YMin       *float64
	YMax       *float64
	HideLegend bool
	HideStats  bool
	StatUnit   string // suffix appended to the statistics, e.g. "" or "%s"

	// BehindFrom draws the series from this index onwards first, so they sit
	// behind the earlier ones while colours and the legend still follow the
	// list order. Zero or out of range leaves the order alone.
	BehindFrom int

	// Zoom renders at a multiple of the nominal size. Everything scales with
	// it -- canvas, font, line widths, dashes -- so the result is genuinely
	// higher resolution rather than an upscaled copy.
	Zoom float64
}

// layout holds the resolved geometry, the equivalent of rrdtool's
// graph_size_location.
type layout struct {
	imgW, imgH            int
	plotX0, plotY0        int
	plotX1, plotY1        int
	legendY               int
	titleH, labelH, lineH int
}

func computeLayout(o Options, face font.Face, rows int, z float64) layout {
	sc := func(v int) int { return int(float64(v)*z + 0.5) }
	lineH := face.Metrics().Height.Ceil() + sc(3)
	titleH := 0
	if o.Title != "" {
		titleH = lineH + sc(8)
	}
	left := sc(52)
	if o.VLabel != "" {
		left += lineH
	}
	l := layout{
		plotX0: left,
		plotY0: titleH + sc(8),
		lineH:  lineH,
	}
	l.plotX1 = l.plotX0 + sc(o.Width)
	l.plotY1 = l.plotY0 + sc(o.Height)
	l.labelH = lineH + sc(4)
	l.legendY = l.plotY1 + l.labelH + lineH
	l.imgW = l.plotX1 + sc(34)
	l.imgH = l.legendY + rows*lineH + sc(6)
	return l
}

// Render draws the graph and returns a PNG.
func Render(all []Series, o Options) ([]byte, error) {
	if len(all) == 0 {
		return nil, fmt.Errorf("no series to graph")
	}
	if o.Location == nil {
		o.Location = time.Local
	}
	if o.Base == 0 {
		o.Base = 1000
	}

	fnt, err := opentype.Parse(gomono.TTF)
	if err != nil {
		return nil, err
	}
	z := o.Zoom
	if z <= 0 {
		z = 1
	}
	sc := func(v int) int { return int(float64(v)*z + 0.5) }
	scf := func(v float64) float64 { return v * z }

	face, err := opentype.NewFace(fnt, &opentype.FaceOptions{
		Size: 10 * z, DPI: 72, Hinting: font.HintingFull,
	})
	if err != nil {
		return nil, err
	}

	rows := len(all)
	if o.HideLegend {
		rows = 0
	}
	l := computeLayout(o, face, rows, z)
	th := o.Theme
	plotW, plotH := l.plotX1-l.plotX0, l.plotY1-l.plotY0

	var flat []float64
	for _, s := range all {
		flat = append(flat, s.Values...)
	}
	y := resolveY(flat, o.YMin, o.YMax, o.Base, exponentUnset, o.Height, 8)

	c := newCanvas(l.imgW, l.imgH)
	c.rect(0, 0, l.imgW, l.imgH, th.Back)
	// rrdtool's bevel: light along the top and left, dark along the other two.
	bevel := sc(2)
	c.rect(0, 0, l.imgW, bevel, th.ShadeA)
	c.rect(0, 0, bevel, l.imgH, th.ShadeA)
	c.rect(0, l.imgH-bevel, l.imgW, l.imgH, th.ShadeB)
	c.rect(l.imgW-bevel, 0, l.imgW, l.imgH, th.ShadeB)
	c.rect(l.plotX0, l.plotY0, l.plotX1, l.plotY1, th.Canvas)

	yToPx := func(v float64) float32 {
		return float32(l.plotY1) - float32((v-y.Min)/(y.Max-y.Min))*float32(plotH)
	}

	// Horizontal grid. rrdtool steps whole multiples of the grid spacing away
	// from zero, and labels every LabFact-th of those, so the labels land on
	// round numbers rather than on wherever the axis happens to start. The
	// positions are collected here; the lines themselves go over the data.
	type hline struct {
		py    int
		col   color.RGBA
		alpha float64
	}
	var hgrid []hline
	if y.GridStep > 0 && !math.IsNaN(y.GridStep) {
		for i := int(y.Min/y.GridStep) - 1; i <= int(y.Max/y.GridStep)+1; i++ {
			v := y.GridStep * float64(i)
			if v < y.Min-y.GridStep*1e-9 || v > y.Max+y.GridStep*1e-9 {
				continue
			}
			py := int(yToPx(v))
			col, alpha := th.Grid, minorAlpha
			if i%y.LabFact == 0 {
				col, alpha = th.MGrid, majorAlpha
				lbl := formatValue(v/y.Factor, y.Symbol)
				c.text(face, lbl, l.plotX0-sc(6)-textWidth(face, lbl), py+sc(4), th.Font)
			}
			hgrid = append(hgrid, hline{py, col, alpha})
		}
	}

	// Vertical grid and time labels, from the interval ladder.
	start, end := all[0].Start, all[0].Stop()
	xToPx := func(t int64) int {
		return l.plotX0 + int(float64(t-start)/float64(end-start)*float64(plotW))
	}
	// Time labels now; their gridlines go over the data with the rest.
	ticks := timeTicks(start, end, o.Width, o.Location)
	for _, tk := range ticks {
		if tk.Label == "" {
			continue
		}
		px := xToPx(tk.At.Unix())
		if px > l.plotX0 && px < l.plotX1 {
			c.text(face, tk.Label, px-textWidth(face, tk.Label)/2, l.plotY1+l.lineH, th.Font)
		}
	}

	// The series, in draw order. A stacked series sits on the running total of
	// the ones stacked before it, so the baseline is carried between them.
	var baseline []float64
	for _, idx := range drawOrder(len(all), o.BehindFrom) {
		s := all[idx]
		n := len(s.Values)
		if baseline == nil {
			baseline = make([]float64, n)
		}
		xAt := func(i int) float32 {
			return float32(l.plotX0) + float32(i)/float32(n)*float32(plotW)
		}

		if s.Kind == Stack {
			// Walk each unbroken run, filling between the old and new tops.
			for i := 0; i < n; {
				if math.IsNaN(s.Values[i]) {
					i++
					continue
				}
				j := i
				for j < n && !math.IsNaN(s.Values[j]) {
					j++
				}
				var top, bottom [][2]float32
				for k := i; k < j; k++ {
					base := baseline[k]
					top = append(top, [2]float32{xAt(k), yToPx(base + s.Values[k])})
					bottom = append([][2]float32{{xAt(k), yToPx(base)}}, bottom...)
				}
				if len(top) > 1 {
					c.fill(append(top, bottom...), s.Colour)
				}
				i = j
			}
			for i, v := range s.Values {
				if !math.IsNaN(v) {
					baseline[i] += v
				}
			}
			continue
		}
		for i, v := range s.Values {
			if math.IsNaN(v) {
				baseline[i] = 0
			} else {
				baseline[i] = v
			}
		}

		pts := make([][2]float32, 0, n)
		for i, v := range s.Values {
			if math.IsNaN(v) {
				continue
			}
			pts = append(pts, [2]float32{xAt(i), yToPx(v)})
		}
		if len(pts) < 2 {
			continue
		}
		if s.Kind == Area {
			poly := append([][2]float32{{pts[0][0], yToPx(y.Min)}}, pts...)
			poly = append(poly, [2]float32{pts[len(pts)-1][0], yToPx(y.Min)})
			c.fill(poly, s.Colour)
		} else {
			c.stroke(pts, float32(scf(orFloat(s.Width, 1))), s.Colour)
		}
	}

	// The grid sits over the data so a value can still be read off a filled
	// area, and the labelled time rules sit over the grid. Both are thin, and
	// the rules are dashed, so the series stays legible underneath.
	lw := sc(1) // grid thickness, scaled like everything else
	for _, hl := range hgrid {
		c.dashedH(hl.py, l.plotX0, l.plotX1, sc(gridDashOn), sc(gridDashOff), lw, hl.col, hl.alpha)
	}
	for _, tk := range ticks {
		px := xToPx(tk.At.Unix())
		if px <= l.plotX0 || px >= l.plotX1 {
			continue
		}
		switch {
		case tk.Label != "":
			// The rule marks a tick that carries a label. Anything else stays
			// grid, so a red line never lands where there is nothing to read.
			c.dashedV(px, l.plotY0, l.plotY1, sc(ruleDashOn), sc(ruleDashOff), lw, th.TimeRule, ruleAlpha)
		case tk.Major:
			c.dashedV(px, l.plotY0, l.plotY1, sc(gridDashOn), sc(gridDashOff), lw, th.MGrid, majorAlpha)
		default:
			c.dashedV(px, l.plotY0, l.plotY1, sc(gridDashOn), sc(gridDashOff), lw, th.Grid, minorAlpha)
		}
	}

	// Axis lines and the arrows rrdtool puts at the ends.
	aw := sc(1)
	c.rect(l.plotX0-aw, l.plotY0, l.plotX0, l.plotY1+aw, th.Axis)
	c.rect(l.plotX0-aw, l.plotY1, l.plotX1, l.plotY1+aw, th.Axis)
	for i := 0; i < sc(4); i++ {
		c.rect(l.plotX1+i, l.plotY1-i, l.plotX1+i+aw, l.plotY1+i+aw, th.Arrow)
		c.rect(l.plotX0-aw-i, l.plotY0-sc(4)+i, l.plotX0+i, l.plotY0-sc(4)+i+aw, th.Arrow)
	}

	if o.Title != "" {
		c.text(face, o.Title, (l.imgW-textWidth(face, o.Title))/2, l.lineH+sc(6), th.Font)
	}
	if o.VLabel != "" {
		c.textRotated(face, o.VLabel, sc(14), (l.plotY0+l.plotY1)/2, th.Font)
	}

	// Legend: a bordered swatch, the name, then the statistics.
	if o.HideLegend {
		return encode(c)
	}
	nameW := 0
	for _, s := range all {
		if n := len([]rune(s.Name)); n > nameW {
			nameW = n
		}
	}
	for i, s := range all {
		ty := l.legendY + i*l.lineH
		c.rect(sc(15), ty-sc(9), sc(24), ty, th.Font)
		c.rect(sc(16), ty-sc(8), sc(23), ty-sc(1), s.Colour)
		line := s.Name
		if !o.HideStats {
			mn, avg, mx, cur := stats(s.Values)
			line = fmt.Sprintf("%-*s Min:%s Avg:%s Max:%s Cur:%s", nameW, s.Name,
				statValue(mn, o.Base, o.StatUnit), statValue(avg, o.Base, o.StatUnit),
				statValue(mx, o.Base, o.StatUnit), statValue(cur, o.Base, o.StatUnit))
		}
		c.text(face, line, sc(30), ty, th.Font)
	}

	return encode(c)
}

func encode(c canvas) ([]byte, error) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, c.RGBA); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// drawOrder lists series indices in the order they should be drawn. Everything
// from behind onwards goes first, so it ends up underneath.
func drawOrder(n, behind int) []int {
	out := make([]int, 0, n)
	if behind <= 0 || behind >= n {
		for i := 0; i < n; i++ {
			out = append(out, i)
		}
		return out
	}
	for i := behind; i < n; i++ {
		out = append(out, i)
	}
	for i := 0; i < behind; i++ {
		out = append(out, i)
	}
	return out
}

func stats(v []float64) (mn, avg, mx, cur float64) {
	mn, mx, sum, n := math.Inf(1), math.Inf(-1), 0.0, 0
	cur = math.NaN()
	for _, x := range v {
		if math.IsNaN(x) || math.IsInf(x, 0) {
			continue
		}
		mn, mx = math.Min(mn, x), math.Max(mx, x)
		sum += x
		n++
		cur = x
	}
	if n == 0 {
		return math.NaN(), math.NaN(), math.NaN(), math.NaN()
	}
	return mn, sum / float64(n), mx, cur
}

// statValue renders one figure the way GPRINT's "%6.2lf%s" does.
func statValue(v float64, base int, unit string) string {
	if math.IsNaN(v) {
		return "      nan"
	}
	if unit == "" {
		return fmt.Sprintf("%7.2f", v)
	}
	factor, sym := magnitude(v, v, base, exponentUnset)
	return fmt.Sprintf("%7.2f%c", v/factor, sym)
}

func formatValue(v float64, sym rune) string {
	s := fmt.Sprintf("%.1f", v)
	if sym != ' ' && sym != 0 {
		return s + " " + string(sym)
	}
	return s
}

func orFloat(v, def float64) float64 {
	if v <= 0 {
		return def
	}
	return v
}
