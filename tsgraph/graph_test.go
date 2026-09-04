package tsgraph_test

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"math"
	"testing"
	"time"

	"github.com/ExpTechTW/prometheus-render/tsgraph"
)

const step = 1800

func series(name string, n int, f func(i int) float64) tsgraph.Series {
	v := make([]float64, n)
	for i := range v {
		v[i] = f(i)
	}
	return tsgraph.Series{Name: name, Start: 1787799600, Step: step, Values: v}
}

func decode(t *testing.T, raw []byte) image.Image {
	t.Helper()
	img, err := png.Decode(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	return img
}

// topOf finds, per column, the first row holding col. It reads the rendered
// pixels rather than trusting the renderer's own account of what it drew.
func topOf(img image.Image, col color.RGBA) []int {
	b := img.Bounds()
	out := make([]int, 0, b.Dx())
	for x := b.Min.X; x < b.Max.X; x++ {
		row := -1
		for y := b.Min.Y; y < b.Max.Y; y++ {
			r, g, bl, _ := img.At(x, y).RGBA()
			if uint8(r>>8) == col.R && uint8(g>>8) == col.G && uint8(bl>>8) == col.B {
				row = y
				break
			}
		}
		out = append(out, row)
	}
	return out
}

func firstDrawn(v []int) int {
	for i, x := range v {
		if x >= 0 {
			return i
		}
	}
	return -1
}

func render(t *testing.T, all []tsgraph.Series, o tsgraph.Options) image.Image {
	t.Helper()
	raw, err := tsgraph.Render(all, o)
	if err != nil {
		t.Fatal(err)
	}
	return decode(t, raw)
}

func TestRenderRejectsNoSeries(t *testing.T) {
	if _, err := tsgraph.Render(nil, tsgraph.Options{}); err == nil {
		t.Error("expected an error with no series")
	}
}

func TestStackedBandSitsOnTheOneBelow(t *testing.T) {
	th := tsgraph.LookupTheme("mrtg")
	a, b := series("a", 60, func(int) float64 { return 10 }), series("b", 60, func(int) float64 { return 20 })
	a.Colour, a.Kind = th.Colour(0), tsgraph.Area
	b.Colour, b.Kind = th.Colour(1), tsgraph.Stack

	lo, hi := 0.0, 40.0
	img := render(t, []tsgraph.Series{a, b}, tsgraph.Options{
		Width: 300, Height: 150, Theme: th, Location: time.UTC,
		HideLegend: true, YMin: &lo, YMax: &hi,
	})

	base, stacked := topOf(img, th.Colour(0)), topOf(img, th.Colour(1))
	i := firstDrawn(base)
	if i < 0 || firstDrawn(stacked) < 0 {
		t.Fatal("a band is missing from the plot")
	}
	if stacked[i] >= base[i] {
		t.Fatalf("the stacked band is not above the first: base row %d, stacked row %d", base[i], stacked[i])
	}
	// 10 of 40 units for the first band and 30 for the stacked top, so the gap
	// between their tops is half the plot.
	if span := float64(base[i]-stacked[i]) / float64(img.Bounds().Dy()); span < 0.2 || span > 0.7 {
		t.Errorf("stacked band spans %.2f of the image, want about half", span)
	}
}

func TestGapBreaksTheLineRatherThanReadingAsZero(t *testing.T) {
	th := tsgraph.LookupTheme("mrtg")
	s := series("rx", 60, func(i int) float64 {
		if i >= 25 && i < 35 {
			return math.NaN()
		}
		return 20
	})
	s.Colour, s.Kind = th.Colour(0), tsgraph.Area

	img := render(t, []tsgraph.Series{s}, tsgraph.Options{
		Width: 300, Height: 150, Theme: th, Location: time.UTC, HideLegend: true,
	})

	top := topOf(img, th.Colour(0))
	drawn, blank := 0, 0
	for _, v := range top {
		if v >= 0 {
			drawn++
		} else {
			blank++
		}
	}
	if drawn == 0 {
		t.Fatal("nothing was drawn")
	}
	// A sixth of the series is missing, so a comparable slice of the plot has
	// no fill. Drawing NaN as zero would fill every column.
	if blank < 20 {
		t.Errorf("only %d blank columns for a gap of 10 of 60 samples", blank)
	}
}

func TestZoomScalesTheWholeImage(t *testing.T) {
	th := tsgraph.LookupTheme("mrtg")
	s := series("rx", 120, func(i int) float64 { return 20 + 10*math.Sin(float64(i)/9) })
	s.Colour, s.Kind = th.Colour(0), tsgraph.Area
	o := tsgraph.Options{Width: 300, Height: 150, Theme: th, Location: time.UTC, Title: "t"}

	one := render(t, []tsgraph.Series{s}, o)
	o.Zoom = 2
	two := render(t, []tsgraph.Series{s}, o)

	if two.Bounds().Dx() < 2*one.Bounds().Dx()-8 || two.Bounds().Dy() < 2*one.Bounds().Dy()-8 {
		t.Errorf("zoom 2 gave %v, want about twice %v", two.Bounds(), one.Bounds())
	}
	// The fill has to keep its share of the image, or the graph washes out as
	// the resolution rises.
	share := func(img image.Image) float64 {
		b, n := img.Bounds(), 0
		for y := b.Min.Y; y < b.Max.Y; y++ {
			for x := b.Min.X; x < b.Max.X; x++ {
				r, g, bl, _ := img.At(x, y).RGBA()
				if uint8(r>>8) == th.Colour(0).R && uint8(g>>8) == th.Colour(0).G && uint8(bl>>8) == th.Colour(0).B {
					n++
				}
			}
		}
		return float64(n) / float64(b.Dx()*b.Dy())
	}
	if a, b := share(one), share(two); math.Abs(a-b) > 0.05 {
		t.Errorf("fill covers %.3f at zoom 1 and %.3f at zoom 2", a, b)
	}
}

func TestThemesAreDistinctAndComplete(t *testing.T) {
	names := tsgraph.ThemeNames()
	if len(names) < 2 {
		t.Fatalf("themes: %v", names)
	}
	seen := map[color.RGBA]string{}
	for _, n := range names {
		th := tsgraph.LookupTheme(n)
		if len(th.Palette) == 0 {
			t.Errorf("theme %q has no palette", n)
		}
		if th.Canvas == (color.RGBA{}) || th.Font == (color.RGBA{}) {
			t.Errorf("theme %q is missing a colour", n)
		}
		if prev, dup := seen[th.Back]; dup && prev != n {
			t.Logf("themes %q and %q share a background", prev, n)
		}
		seen[th.Back] = n
		// The cycle must wrap rather than run off the end.
		if th.Colour(len(th.Palette)+1) != th.Colour(1) {
			t.Errorf("theme %q palette does not cycle", n)
		}
	}
	// An unknown name still returns something usable.
	if len(tsgraph.LookupTheme("nope").Palette) == 0 {
		t.Error("the fallback theme has no palette")
	}
}
