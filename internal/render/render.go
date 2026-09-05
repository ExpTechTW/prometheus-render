// Package render joins a resolved query to the graph library: it fetches the
// series and hands them over in the presentation the caller asked for.
//
// Fetching and drawing are separate so one set of samples can become several
// images -- a light and a dark one, with and without the peak traces -- for
// the cost of a single query.
package render

import (
	"context"

	"github.com/ExpTechTW/prometheus-render/internal/params"
	"github.com/ExpTechTW/prometheus-render/internal/promapi"
	"github.com/ExpTechTW/prometheus-render/internal/query"
	"github.com/ExpTechTW/prometheus-render/internal/series"
	"github.com/ExpTechTW/prometheus-render/tsgraph"
)

// Fetch runs the query behind a graph, keeping each target's series separate
// so a caller can draw a subset of them without querying again.
func Fetch(ctx context.Context, c *promapi.Client, g *params.Graph) ([][]series.Series, error) {
	return g.Request.BuildGrouped(ctx, c)
}

// Draw renders samples that Fetch already returned.
func Draw(g *params.Graph, data []series.Series) ([]byte, error) {
	theme := g.Options.Theme
	out := make([]tsgraph.Series, len(data))
	for i, s := range data {
		// Kinds and Widths are per target. When a target expands to several
		// series they share its presentation, while the palette keeps cycling.
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

// FetchAndDraw is the single-image path the CLI uses.
func FetchAndDraw(ctx context.Context, c *promapi.Client, g *params.Graph) ([]byte, error) {
	data, err := Fetch(ctx, c, g)
	if err != nil {
		return nil, err
	}
	return Draw(g, query.Flatten(data))
}
