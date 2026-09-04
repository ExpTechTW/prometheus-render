package tsgraph

import "math"

// The vertical scale uses Heckbert's nice-number method (Paul S. Heckbert,
// "Nice Numbers for Graph Labels", Graphics Gems, 1990): round the range to a
// number of the form 1, 2 or 5 times a power of ten, pick a step the same way,
// then snap the ends outwards to whole steps. It is the textbook approach and
// owes nothing to any particular tool.

// siSymbol is the magnitude suffix ladder, centred on the base unit.
var siSymbol = []rune{
	'y', 'z', 'a', 'f', 'p', 'n', 'u', 'm', ' ',
	'k', 'M', 'G', 'T', 'P', 'E', 'Z', 'Y',
}

const siSymbolBase = 8 // index of ' ' above

// exponentUnset asks for the magnitude to be chosen from the data.
const exponentUnset = 9999

// targetGridLines is how many gridlines a full-height axis aims for. The step
// is then widened until the labels have room for the font.
const targetGridLines = 5

// niceNum returns a "round" number near x: one of 1, 2, 5 or 10 times a power
// of ten. round picks the nearest such number, otherwise the next one up.
func niceNum(x float64, round bool) float64 {
	if x <= 0 || math.IsNaN(x) || math.IsInf(x, 0) {
		return 1
	}
	exp := math.Floor(math.Log10(x))
	f := x / math.Pow(10, exp) // 1 <= f < 10
	var nf float64
	switch {
	case round && f < 1.5:
		nf = 1
	case round && f < 3:
		nf = 2
	case round && f < 7:
		nf = 5
	case round:
		nf = 10
	case f <= 1:
		nf = 1
	case f <= 2:
		nf = 2
	case f <= 5:
		nf = 5
	default:
		nf = 10
	}
	return nf * math.Pow(10, exp)
}

// magnitude returns the power of base the values are shown in, and its suffix.
func magnitude(minv, maxv float64, base int, unitsExponent int) (factor float64, symbol rune) {
	b := float64(base)
	peak := math.Max(math.Abs(minv), math.Abs(maxv))
	if peak == 0 || math.IsNaN(peak) || math.IsInf(peak, 0) {
		return 1, ' '
	}

	digits := math.Floor(math.Log(peak) / math.Log(b))
	if unitsExponent != exponentUnset {
		digits = math.Floor(float64(unitsExponent) / 3)
	}

	symbol = '?'
	if i := int(digits) + siSymbolBase; i >= 0 && i < len(siSymbol) {
		symbol = siSymbol[i]
	}
	return math.Pow(b, digits), symbol
}

// yAxis is the resolved vertical scale.
type yAxis struct {
	Min, Max float64
	Factor   float64 // divide a value by this to get the displayed number
	Symbol   rune    // suffix for the displayed number
	GridStep float64 // spacing between gridlines, in data units
	LabFact  int     // a label every LabFact gridlines
}

// resolveY produces the vertical scale for a set of values. Pinned limits are
// honoured; anything left open is rounded outwards to a whole step.
func resolveY(values []float64, pinMin, pinMax *float64, base, unitsExponent, ysize int, fontSize float64) yAxis {
	minv, maxv := math.Inf(1), math.Inf(-1)
	for _, v := range values {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			continue
		}
		minv = math.Min(minv, v)
		maxv = math.Max(maxv, v)
	}
	if math.IsInf(minv, 0) { // nothing to plot
		minv, maxv = 0, 1
	}
	// A series that never goes negative reads better against a zero baseline.
	if minv > 0 && pinMin == nil {
		minv = 0
	}
	if pinMin != nil {
		minv = *pinMin
	}
	if pinMax != nil {
		maxv = *pinMax
	}
	if maxv <= minv {
		maxv = minv + 1
	}

	step := niceNum(niceNum(maxv-minv, false)/float64(targetGridLines-1), true)
	if pinMin == nil {
		minv = math.Floor(minv/step) * step
	}
	if pinMax == nil {
		maxv = math.Ceil(maxv/step) * step
	}

	// Label every gridline unless they would collide, then every second or
	// fifth, so the axis never prints text on top of itself.
	labFact := 1
	if ysize > 0 {
		perLine := float64(ysize) * step / (maxv - minv)
		for _, f := range []int{1, 2, 5, 10} {
			labFact = f
			if perLine*float64(f) >= 1.8*fontSize {
				break
			}
		}
	}

	factor, symbol := magnitude(minv, maxv, base, unitsExponent)
	return yAxis{Min: minv, Max: maxv, Factor: factor, Symbol: symbol, GridStep: step, LabFact: labFact}
}
