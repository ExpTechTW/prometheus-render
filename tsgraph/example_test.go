package tsgraph_test

import (
	"fmt"
	"math"
	"time"

	"github.com/ExpTechTW/prometheus-render/tsgraph"
)

// Rendering two series, one filled and one over it as a line.
func ExampleRender() {
	const step = 300
	start := time.Date(2026, 9, 4, 0, 0, 0, 0, time.UTC).Unix()

	rx := make([]float64, 288)
	tx := make([]float64, 288)
	for i := range rx {
		rx[i] = 30 + 20*math.Sin(float64(i)/40)
		tx[i] = 8 + 3*math.Sin(float64(i)/25)
	}
	rx[100] = math.NaN() // a gap breaks the line rather than reading as zero

	theme := tsgraph.LookupTheme("mrtg")
	png, err := tsgraph.Render([]tsgraph.Series{
		{Name: "rx", Start: start, Step: step, Values: rx, Colour: theme.Colour(0), Kind: tsgraph.Area},
		{Name: "tx", Start: start, Step: step, Values: tx, Colour: theme.Colour(1), Kind: tsgraph.Line, Width: 2},
	}, tsgraph.Options{
		Title: "eth0", VLabel: "Mbps",
		Width: 500, Height: 150,
		Theme: theme, Location: time.UTC,
	})
	if err != nil {
		panic(err)
	}
	fmt.Println(string(png[1:4]))
	// Output: PNG
}
