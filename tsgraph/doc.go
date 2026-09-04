// Package tsgraph draws time series as PNG, in the style of RRDtool and MRTG.
//
// It takes samples on a fixed time grid and returns an image: no external
// process, no cairo, no cgo, so it cross-compiles wherever Go does. The axis
// algorithms are its own; see DESIGN.md.
//
//	theme := tsgraph.LookupTheme("mrtg")
//	png, err := tsgraph.Render([]tsgraph.Series{{
//		Name:   "rx",
//		Start:  start.Unix(),
//		Step:   300,
//		Values: values, // NaN marks a gap
//		Colour: theme.Colour(0),
//		Kind:   tsgraph.Area,
//	}}, tsgraph.Options{
//		Title:  "eth0",
//		VLabel: "Mbps",
//		Width:  500,
//		Height: 150,
//		Theme:  theme,
//	})
//
// What makes a graph read as RRD is specific and reproduced here: the
// Cur/Min/Avg/Max legend table, the bevelled frame, a grey page around a white
// canvas, and gridlines chosen by how many seconds a pixel covers.
package tsgraph
