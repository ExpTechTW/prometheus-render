package tsgraph

import (
	"image/color"
	"sort"
)

// Theme is the surrounding colour scheme. The field names follow rrdtool's own
// colour slots, so a scheme written for one carries over to the other.
type Theme struct {
	Back   color.RGBA // the page behind the graph
	Canvas color.RGBA // the plot area
	ShadeA color.RGBA // top and left of the bevel
	ShadeB color.RGBA // bottom and right of the bevel
	Grid   color.RGBA // unlabelled gridlines
	MGrid  color.RGBA // gridlines that carry a label
	Font   color.RGBA
	Axis   color.RGBA
	Arrow  color.RGBA

	// TimeRule is drawn at the labelled ticks on the time axis, over the data.
	TimeRule color.RGBA

	// Palette is the series colour cycle. Following MRTG, the order is input,
	// output, peak of the input, peak of the output.
	Palette []color.RGBA
}

func hex(v uint32) color.RGBA {
	return color.RGBA{uint8(v >> 16), uint8(v >> 8), uint8(v), 0xFF}
}

func palette(vs ...uint32) []color.RGBA {
	out := make([]color.RGBA, len(vs))
	for i, v := range vs {
		out[i] = hex(v)
	}
	return out
}

// themes are the presets. MRTG's own four colours are kept in both, since they
// are what makes a graph read as MRTG; only the surroundings change.
var themes = map[string]Theme{
	"mrtg": {
		Back: hex(0xF5F5F5), Canvas: hex(0xFFFFFF),
		ShadeA: hex(0xDDDDDD), ShadeB: hex(0x999999),
		Grid: hex(0xB0B0B0), MGrid: hex(0x606060),
		Font: hex(0x202020), Axis: hex(0x404040), Arrow: hex(0xA00000),
		TimeRule: hex(0xA00000),
		Palette:  palette(0x00CC00, 0x0000FF, 0x006600, 0xFF00FF),
	},
	"dark": {
		Back: hex(0x161B22), Canvas: hex(0x0D1117),
		ShadeA: hex(0x161B22), ShadeB: hex(0x161B22),
		Grid: hex(0x4A5563), MGrid: hex(0x8B949E),
		Font: hex(0xC9D1D9), Axis: hex(0x484F58), Arrow: hex(0x6E7681),
		TimeRule: hex(0xF0883E),
		// On a dark canvas a peak has to sit lighter than its own average to
		// separate from it, the opposite of MRTG's darker-is-peak on white.
		Palette: palette(0x00CC00, 0x58A6FF, 0xA5F3A5, 0xFF7BD5),
	},
	"munin": {
		Back: hex(0xF5F5F5), Canvas: hex(0xFFFFFF),
		ShadeA: hex(0xDDDDDD), ShadeB: hex(0x999999),
		Grid: hex(0xB0B0B0), MGrid: hex(0x606060),
		Font: hex(0x202020), Axis: hex(0x404040), Arrow: hex(0xA00000),
		TimeRule: hex(0xA00000),
		Palette: palette(0x00CC00, 0x0066B3, 0xFF8000, 0xFFCC00,
			0x330099, 0x990099, 0xCCFF00, 0xFF0000),
	},
}

// LookupTheme returns a preset by name, falling back to mrtg.
func LookupTheme(name string) Theme {
	if t, ok := themes[name]; ok {
		return t
	}
	return themes["mrtg"]
}

// ThemeNames lists the presets, sorted.
func ThemeNames() []string {
	out := make([]string, 0, len(themes))
	for name := range themes {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// Colour returns the palette entry for series i, cycling if there are more
// series than colours.
func (t Theme) Colour(i int) color.RGBA {
	p := t.Palette
	if len(p) == 0 {
		p = themes["mrtg"].Palette
	}
	return p[i%len(p)]
}
