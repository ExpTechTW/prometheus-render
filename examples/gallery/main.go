// Command gallery renders the example images in out/ from the sample database.
//
// It is a separate module so the library itself keeps its single dependency,
// and it doubles as a worked example: nothing here touches Prometheus, only
// tsgraph and a table of samples.
package main

import (
	"database/sql"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"

	"github.com/ExpTechTW/prometheus-render/tsgraph"
)

// tiers name the four timescales, in the order they are drawn.
var tiers = []string{"5m", "30m", "2h", "1d"}

// tierTitle is what the heading calls each one, following MRTG.
var tierTitle = map[string]string{
	"5m":  "Daily (5 min average)",
	"30m": "Weekly (30 min average)",
	"2h":  "Monthly (2 hour average)",
	"1d":  "Yearly (1 day average)",
}

// metric describes one family of graphs.
type metric struct {
	vlabel string
	area   bool // fill the first series
}

var metrics = map[string]metric{
	"traffic": {vlabel: "Mbps", area: true},
	"pps":     {vlabel: "packets/sec", area: true},
	"rps":     {vlabel: "req/sec"},
}

type row struct {
	name   string
	ord    int
	start  int64
	step   int64
	values []float64
}

func main() {
	dbPath := flag.String("db", "testdata/sample.db", "sample database")
	outDir := flag.String("out", "out", "directory to write into")
	zoom := flag.Float64("zoom", 2, "render at this multiple of the nominal size")
	tz := flag.String("tz", "Asia/Taipei", "timezone for the time axis")
	flag.Parse()

	loc, err := time.LoadLocation(*tz)
	if err != nil {
		log.Fatalf("timezone: %v", err)
	}
	db, err := sql.Open("sqlite", *dbPath)
	if err != nil {
		log.Fatalf("open %s: %v", *dbPath, err)
	}
	defer db.Close()

	written := 0
	for _, spec := range list(db) {
		for _, theme := range []string{"light", "dark"} {
			for _, variant := range []string{"peak", "plain"} {
				if err := draw(db, *outDir, spec, theme, variant, loc, *zoom); err != nil {
					log.Fatalf("%s/%s/%s: %v", spec.metric, spec.host, spec.tier, err)
				}
				written++
			}
		}
	}
	fmt.Printf("wrote %d images under %s\n", written, *outDir)
}

type spec struct{ metric, host, tier string }

func list(db *sql.DB) []spec {
	rows, err := db.Query(`SELECT DISTINCT metric, host, tier FROM series ORDER BY metric, host, tier`)
	if err != nil {
		log.Fatal(err)
	}
	defer rows.Close()
	var out []spec
	for rows.Next() {
		var s spec
		if err := rows.Scan(&s.metric, &s.host, &s.tier); err != nil {
			log.Fatal(err)
		}
		out = append(out, s)
	}
	return out
}

// load reads one graph's series, newest ordering first. A NULL point is a gap,
// which the renderer draws as a break rather than as a zero.
func load(db *sql.DB, s spec) ([]row, error) {
	rows, err := db.Query(`
		SELECT id, name, ord, start, step FROM series
		WHERE metric = ? AND host = ? AND tier = ? ORDER BY ord`, s.metric, s.host, s.tier)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []row
	var ids []int64
	for rows.Next() {
		var r row
		var id int64
		if err := rows.Scan(&id, &r.name, &r.ord, &r.start, &r.step); err != nil {
			return nil, err
		}
		out = append(out, r)
		ids = append(ids, id)
	}
	for i, id := range ids {
		pts, err := db.Query(`SELECT value FROM points WHERE series_id = ? ORDER BY idx`, id)
		if err != nil {
			return nil, err
		}
		for pts.Next() {
			var v sql.NullFloat64
			if err := pts.Scan(&v); err != nil {
				pts.Close()
				return nil, err
			}
			if v.Valid {
				out[i].values = append(out[i].values, v.Float64)
			} else {
				out[i].values = append(out[i].values, nan())
			}
		}
		pts.Close()
	}
	return out, nil
}

func draw(db *sql.DB, outDir string, s spec, theme, variant string, loc *time.Location, zoom float64) error {
	rows, err := load(db, s)
	if err != nil {
		return err
	}
	m := metrics[s.metric]

	// The peak traces are the trailing series; the plain variant drops them.
	base := rows
	if variant == "plain" {
		base = nil
		for _, r := range rows {
			if !isPeak(r.name) {
				base = append(base, r)
			}
		}
	}
	if len(base) == 0 {
		return nil
	}

	th := tsgraph.LookupTheme(map[string]string{"light": "mrtg", "dark": "dark"}[theme])
	series := make([]tsgraph.Series, len(base))
	for i, r := range base {
		kind := tsgraph.Line
		if m.area && i == 0 {
			kind = tsgraph.Area
		}
		series[i] = tsgraph.Series{
			Name: r.name, Start: r.start, Step: r.step, Values: r.values,
			Colour: th.Colour(r.ord), Kind: kind, Width: 2,
		}
	}

	// Peaks are drawn first so the averages sit on top of them.
	behind := 0
	for i, r := range base {
		if isPeak(r.name) {
			behind = i
			break
		}
	}

	img, err := tsgraph.Render(series, tsgraph.Options{
		Title:      fmt.Sprintf("%s %s - %s", s.host, s.metric, tierTitle[s.tier]),
		VLabel:     m.vlabel,
		Width:      500,
		Height:     150,
		Theme:      th,
		Location:   loc,
		Zoom:       zoom,
		BehindFrom: behind,
	})
	if err != nil {
		return err
	}

	dir := filepath.Join(outDir, s.metric, s.host, theme, variant)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, s.tier+".png"), img, 0o644)
}

func isPeak(name string) bool {
	return len(name) >= 4 && name[len(name)-4:] == "peak"
}

func nan() float64 { var z float64; return z / z }
