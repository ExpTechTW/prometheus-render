// Package render joins a resolved query to the graph library: it fetches the
// series and hands them over with the presentation the caller asked for.
package render

import (
	"context"

	"github.com/ExpTechTW/prometheus-render/internal/params"
	"github.com/ExpTechTW/prometheus-render/internal/promapi"
	"github.com/ExpTechTW/prometheus-render/tsgraph"
)

// Draw runs the query and renders the result.
func Draw(ctx context.Context, c *promapi.Client, g *params.Graph) ([]byte, error) {
	data, err := g.Request.Build(ctx, c)
	if err != nil {
		return nil, err
	}

	theme := g.Options.Theme
	out := make([]tsgraph.Series, len(data))
	for i, s := range data {
		// Kinds and Widths are per target. A target that expanded into several
		// series shares its presentation, and the palette keeps cycling.
		k, w := tsgraph.Line, 0.0
		switch {
		case i < len(g.Kinds):
			k, w = g.Kinds[i], g.Widths[i]
		case len(g.Kinds) > 0:
			k, w = g.Kinds[len(g.Kinds)-1], g.Widths[len(g.Widths)-1]
		}
		out[i] = tsgraph.Series{
			Name:   s.Name,
			Start:  s.Start,
			Step:   s.Step,
			Values: s.Values,
			Colour: theme.Colour(i),
			Kind:   k,
			Width:  w,
		}
	}
	return tsgraph.Render(out, g.Options)
}
