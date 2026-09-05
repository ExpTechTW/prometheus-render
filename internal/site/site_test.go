package site

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ExpTechTW/prometheus-render/internal/config"
	"github.com/ExpTechTW/prometheus-render/internal/promapi"
)

// countingSource is a Prometheus stand-in that records how many queries it is
// answering at once. That count is how these tests observe the worker pool
// from outside, without reaching into it.
type countingSource struct {
	*httptest.Server
	mu   sync.Mutex
	now  int
	peak int
	fail bool
}

func newSource(t *testing.T) *countingSource {
	t.Helper()
	s := &countingSource{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.enter()
		// Held briefly so overlapping work genuinely overlaps in time.
		time.Sleep(20 * time.Millisecond)
		defer s.leave()

		if s.fail {
			http.Error(w, "upstream is down", http.StatusBadGateway)
			return
		}
		_ = r.ParseForm()
		start, _ := strconv.ParseFloat(r.FormValue("start"), 64)
		end, _ := strconv.ParseFloat(r.FormValue("end"), 64)
		step, _ := strconv.ParseFloat(r.FormValue("step"), 64)

		var values [][2]any
		for i, ts := 0, start; ts <= end; i, ts = i+1, ts+step {
			values = append(values, [2]any{ts, strconv.Itoa(i % 50)})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": "success",
			"data": map[string]any{
				"resultType": "matrix",
				"result": []any{map[string]any{
					"metric": map[string]string{"__name__": "up", "instance": "a:9100"},
					"values": values,
				}},
			},
		})
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *countingSource) enter() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.now++
	if s.now > s.peak {
		s.peak = s.now
	}
}

func (s *countingSource) leave() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.now--
}

func (s *countingSource) maxConcurrent() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.peak
}

// build wires a site against the fake source, writing into a temp directory.
func build(t *testing.T, src *countingSource, workers, limit int, extra string) *Site {
	t.Helper()
	dir := t.TempDir()
	cfg, err := config.Parse([]byte(`
source:
  url: ` + src.URL + `
output:
  dir: ` + dir + `
  title: Test Site
  workers: ` + strconv.Itoa(workers) + `
defaults:
  width: 300
  height: 120
  ranges:
    - {name: 1d, title: "Daily (5 min average)", from: -1d, step: 30m}
    - {name: 1w, title: "Weekly (30 min average)", from: -7d, step: 6h}
graphs:
  - name: traffic
    title: eth0 traffic
    vtitle: bits/sec
    series:
      - {expr: "rate(node_network_receive_bytes_total[5m])", legend: inbound}
      - {expr: "rate(node_network_transmit_bytes_total[5m])", legend: outbound}
  - name: load
    title: Load average
    series:
      - {expr: node_load1, legend: "{{instance}}"}
` + extra))
	if err != nil {
		t.Fatalf("config: %v", err)
	}

	c := promapi.NewClient(cfg.Source.URL, 10*time.Second)
	if limit > 0 {
		c.Limit = make(chan struct{}, limit)
	}
	return &Site{Cfg: cfg, Client: c}
}

func read(t *testing.T, s *Site, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(s.Cfg.Output.Dir, name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(b)
}

func TestRenderWritesAnImagePerGraphAndTimescale(t *testing.T) {
	s := build(t, newSource(t), 4, 0, "")
	if err := s.Render(context.Background()); err != nil {
		t.Fatalf("Render: %v", err)
	}

	for _, name := range []string{
		"traffic/1d.png", "traffic/1w.png", "load/1d.png", "load/1w.png",
		"index.html", "traffic.html", "load.html",
	} {
		info, err := os.Stat(filepath.Join(s.Cfg.Output.Dir, name))
		if err != nil {
			t.Errorf("missing %s: %v", name, err)
			continue
		}
		if info.Size() == 0 {
			t.Errorf("%s is empty", name)
		}
	}

	// Nothing may be left behind by the write-then-rename.
	leftover, _ := filepath.Glob(filepath.Join(s.Cfg.Output.Dir, "*", "*.tmp"))
	if len(leftover) != 0 {
		t.Errorf("temporary files left behind: %v", leftover)
	}

	if b := read(t, s, "traffic/1d.png"); !strings.HasPrefix(b, "\x89PNG") {
		t.Error("traffic/1d.png is not a PNG")
	}
}

func TestPagesLinkEveryImageAndTheRepository(t *testing.T) {
	s := build(t, newSource(t), 2, 0, "")
	if err := s.Render(context.Background()); err != nil {
		t.Fatalf("Render: %v", err)
	}

	const repo = "https://github.com/ExpTechTW/prometheus-render"
	index := read(t, s, "index.html")
	if !strings.Contains(index, repo) {
		t.Error("index.html does not link the repository")
	}
	if !strings.Contains(index, "<svg viewBox=\"0 0 16 16\"") {
		t.Error("index.html carries no GitHub mark")
	}
	if !strings.Contains(index, "Test Site") {
		t.Error("index.html does not carry the configured title")
	}
	// The front page shows the finest timescale and links through, as MRTG does.
	for _, want := range []string{`src="traffic/1d.png"`, `href="traffic.html"`,
		`src="load/1d.png"`, `href="load.html"`} {
		if !strings.Contains(index, want) {
			t.Errorf("index.html is missing %s", want)
		}
	}
	if strings.Contains(index, "1w.png") {
		t.Error("index.html should show one timescale per graph, not all of them")
	}

	page := read(t, s, "traffic.html")
	if !strings.Contains(page, repo) {
		t.Error("traffic.html does not link the repository")
	}
	for _, want := range []string{
		`src="traffic/1d.png"`, `src="traffic/1w.png"`,
		"Daily (5 min average)", "Weekly (30 min average)",
		`href="index.html"`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("traffic.html is missing %s", want)
		}
	}
}

// A graph's own targets are already fetched in parallel by the query layer, so
// the widest graph in the test config accounts for two concurrent queries on
// its own. Anything above that had to come from two jobs running at once,
// which is what the pool exists to do.
const widestGraph = 2

func TestRenderRunsJobsConcurrently(t *testing.T) {
	src := newSource(t)
	s := build(t, src, 4, 0, "")
	if err := s.Render(context.Background()); err != nil {
		t.Fatalf("Render: %v", err)
	}
	if got := src.maxConcurrent(); got <= widestGraph {
		t.Errorf("peak concurrent queries = %d, want more than %d: the graphs were drawn one after another",
			got, widestGraph)
	}
}

func TestOneWorkerDrawsOneGraphAtATime(t *testing.T) {
	src := newSource(t)
	s := build(t, src, 1, 0, "")
	if err := s.Render(context.Background()); err != nil {
		t.Fatalf("Render: %v", err)
	}
	if got := src.maxConcurrent(); got > widestGraph {
		t.Errorf("peak concurrent queries = %d, want at most %d with a single worker",
			got, widestGraph)
	}
}

// Fanning out over every graph and timescale must not reach the source as one
// burst, however many workers are drawing.
func TestQueryLimitCapsWhatReachesTheSource(t *testing.T) {
	src := newSource(t)
	s := build(t, src, 8, 2, "")
	if err := s.Render(context.Background()); err != nil {
		t.Fatalf("Render: %v", err)
	}
	if got := src.maxConcurrent(); got > 2 {
		t.Errorf("peak concurrent queries = %d, want at most the limit of 2", got)
	}
}

// A source that is briefly unreachable should leave the site describing the
// config rather than removing the pages.
func TestPagesSurviveAFailedQuery(t *testing.T) {
	src := newSource(t)
	src.fail = true
	s := build(t, src, 2, 0, "")

	if err := s.Render(context.Background()); err == nil {
		t.Error("expected Render to report the failed queries")
	}
	index := read(t, s, "index.html")
	if !strings.Contains(index, `href="traffic.html"`) {
		t.Error("index.html no longer lists the graphs")
	}
	if _, err := os.Stat(filepath.Join(s.Cfg.Output.Dir, "traffic/1d.png")); err == nil {
		t.Error("a failed query should not leave an image behind")
	}
}

// interval 0 draws once and returns, which is the shape cron wants.
func TestRunDrawsOnceWithoutAnInterval(t *testing.T) {
	s := build(t, newSource(t), 4, 0, "")
	if err := s.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if _, err := os.Stat(filepath.Join(s.Cfg.Output.Dir, "index.html")); err != nil {
		t.Errorf("nothing was drawn: %v", err)
	}
}

func TestRunRedrawsOnTheInterval(t *testing.T) {
	src := newSource(t)
	s := build(t, src, 4, 0, "")
	s.Cfg.Output.Interval = config.Duration(60 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()
	if err := s.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}

	first := read(t, s, "index.html")
	if !strings.Contains(first, "Test Site") {
		t.Error("the site was not written")
	}
	// Four images per pass; more than one pass must have happened.
	src.mu.Lock()
	defer src.mu.Unlock()
	if src.peak == 0 {
		t.Error("the timer never fired a render")
	}
}
