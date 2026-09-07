package site

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"path/filepath"
	"regexp"
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
	mu     sync.Mutex
	now    int
	peak   int
	fail   bool
	labels []string // what discovery finds
	asked  []string // the expressions actually queried
}

func newSource(t *testing.T) *countingSource {
	t.Helper()
	s := &countingSource{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/v1/label/") {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"status": "success", "data": s.labels,
			})
			return
		}
		s.enter()
		// Held briefly so overlapping work genuinely overlaps in time.
		time.Sleep(20 * time.Millisecond)
		defer s.leave()

		if s.fail {
			http.Error(w, "upstream is down", http.StatusBadGateway)
			return
		}
		_ = r.ParseForm()
		s.mu.Lock()
		s.asked = append(s.asked, r.FormValue("query"))
		s.mu.Unlock()
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

func (s *countingSource) queries() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.asked...)
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
		"traffic/light/plain/1d.png", "traffic/dark/plain/1d.png",
		"traffic/light/plain/1w.png", "load/light/plain/1d.png", "load/dark/plain/1w.png",
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
	leftover, _ := filepath.Glob(filepath.Join(s.Cfg.Output.Dir, "*", "*", "*", "*.tmp"))
	if len(leftover) != 0 {
		t.Errorf("temporary files left behind: %v", leftover)
	}

	if b := read(t, s, "traffic/light/plain/1d.png"); !strings.HasPrefix(b, "\x89PNG") {
		t.Error("the drawn image is not a PNG")
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
	for _, want := range []string{`data-base="traffic"`, `href="traffic.html"`,
		`data-base="load"`, `href="load.html"`} {
		if !strings.Contains(index, want) {
			t.Errorf("index.html is missing %s", want)
		}
	}
	if strings.Contains(index, `data-range="1w"`) {
		t.Error("index.html should show one timescale per graph, not all of them")
	}

	page := read(t, s, "traffic.html")
	if !strings.Contains(page, repo) {
		t.Error("traffic.html does not link the repository")
	}
	for _, want := range []string{
		`data-range="1d"`, `data-range="1w"`,
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
	if _, err := os.Stat(filepath.Join(s.Cfg.Output.Dir, "traffic/light/plain/1d.png")); err == nil {
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

// What is servable is what the config drew. A request must not be able to
// choose a query, name a metric, or reach outside the output directory.
func TestHandlerServesOnlyWhatTheConfigDrew(t *testing.T) {
	s := build(t, newSource(t), 4, 0, "")
	if err := s.Render(context.Background()); err != nil {
		t.Fatalf("Render: %v", err)
	}
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	get := func(path string) (int, string) {
		t.Helper()
		resp, err := srv.Client().Get(srv.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}

	for _, path := range []string{"/", "/index.html", "/traffic.html", "/traffic/light/plain/1d.png"} {
		if code, _ := get(path); code != http.StatusOK {
			t.Errorf("GET %s = %d, want 200", path, code)
		}
	}
	if code, body := get("/healthz"); code != http.StatusOK || !strings.Contains(body, "ok") {
		t.Errorf("GET /healthz = %d %q", code, body)
	}

	// No endpoint takes a query, under any of the names one might have had.
	for _, path := range []string{
		"/render?target=node_load1&from=-1h",
		"/render",
		"/api/v1/query?query=node_load1",
		"/graph?target=up",
	} {
		if code, _ := get(path); code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404: nothing may accept a query", path, code)
		}
	}

	// Nor may a request climb out of the output directory.
	for _, path := range []string{"/../site_test.go", "/..%2fsite.go", "/traffic/../../site.go"} {
		if code, _ := get(path); code == http.StatusOK {
			t.Errorf("GET %s = 200, want it refused", path)
		}
	}
}

// buildSplit wires a site that is divided by region, discovering the values
// from the source the way a deployment does.
func buildSplit(t *testing.T, src *countingSource, extra string) *Site {
	t.Helper()
	dir := t.TempDir()
	cfg, err := config.Parse([]byte(`
source:
  url: ` + src.URL + `
output:
  dir: ` + dir + `
  title: Test Site
  workers: 4
regions:
  label: region
  titles: {tnn: core-tnn1, tyo: core-tyo1}
defaults:
  width: 300
  height: 120
  peak: true
  ranges:
    - {name: 1d, title: Daily, from: -1d, step: 30m}
graphs:
  - name: traffic
    title: HTTP traffic
    series:
      - {expr: 'sum(rate(in_total{region="$region"}[5m]))', legend: inbound}
      - {expr: 'sum(rate(out_total{region="$region"}[5m]))', legend: outbound}
  - name: lag
    title: Sensor lag
    peak: false
    only_regions: [tnn]
    series:
      - {expr: 'lag_seconds{region="$region"}', legend: lag}
  - name: total
    title: Every region
    global: true
    peak: false
    series:
      - {expr: 'sum(rate(in_total[5m]))', legend: inbound}
` + extra))
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	return &Site{Cfg: cfg, Client: promapi.NewClient(cfg.Source.URL, 10*time.Second)}
}

func TestRegionsAreDiscoveredAndSubstituted(t *testing.T) {
	src := newSource(t)
	src.labels = []string{"tnn", "tyo"}
	s := buildSplit(t, src, "")
	if err := s.Render(context.Background()); err != nil {
		t.Fatalf("Render: %v", err)
	}

	// Each region got its own query, with the placeholder filled in.
	var joined string
	for _, q := range src.queries() {
		joined += q + "\n"
	}
	for _, want := range []string{`in_total{region="tnn"}`, `in_total{region="tyo"}`} {
		if !strings.Contains(joined, want) {
			t.Errorf("no query for %s", want)
		}
	}
	if strings.Contains(joined, "$region") {
		t.Error("a placeholder reached the source unsubstituted")
	}
}

func TestBothViewsExist(t *testing.T) {
	src := newSource(t)
	src.labels = []string{"tnn", "tyo"}
	s := buildSplit(t, src, "")
	if err := s.Render(context.Background()); err != nil {
		t.Fatalf("Render: %v", err)
	}

	for _, name := range []string{
		"index.html",
		"region/core-tnn1.html", "region/core-tyo1.html", // everything about one place
		"graph/traffic.html", "graph/lag.html", // one thing across places
		"core-tnn1/traffic.html", "core-tyo1/traffic.html",
		"total.html", // a global graph keeps the unsplit layout
	} {
		if _, err := os.Stat(filepath.Join(s.Cfg.Output.Dir, name)); err != nil {
			t.Errorf("missing %s", name)
		}
	}

	index := read(t, s, "index.html")
	for _, want := range []string{"Regions", "Graphs", "core-tnn1", "core-tyo1", "HTTP traffic"} {
		if !strings.Contains(index, want) {
			t.Errorf("index.html is missing %q", want)
		}
	}
	// only_regions keeps a graph out of the regions it does not belong to.
	if _, err := os.Stat(filepath.Join(s.Cfg.Output.Dir, "core-tyo1/lag.html")); err == nil {
		t.Error("lag was drawn for a region it is not in")
	}
	if strings.Contains(read(t, s, "region/core-tyo1.html"), "Sensor lag") {
		t.Error("region/tyo.html lists a graph that is not in that region")
	}
	// A global graph belongs to no region.
	if strings.Contains(read(t, s, "region/core-tnn1.html"), "Every region") {
		t.Error("a global graph was listed under a region")
	}
}

func TestEveryThemeAndVariantIsDrawn(t *testing.T) {
	src := newSource(t)
	src.labels = []string{"tnn"}
	s := buildSplit(t, src, "")
	if err := s.Render(context.Background()); err != nil {
		t.Fatalf("Render: %v", err)
	}

	// A graph that asks for peaks gets both variants, in both palettes.
	for _, name := range []string{
		"core-tnn1/traffic/light/plain/1d.png", "core-tnn1/traffic/light/peak/1d.png",
		"core-tnn1/traffic/dark/plain/1d.png", "core-tnn1/traffic/dark/peak/1d.png",
	} {
		if _, err := os.Stat(filepath.Join(s.Cfg.Output.Dir, name)); err != nil {
			t.Errorf("missing %s", name)
		}
	}
	// One that does not keeps only the averages, still in both palettes.
	if _, err := os.Stat(filepath.Join(s.Cfg.Output.Dir, "core-tnn1/lag/light/plain/1d.png")); err != nil {
		t.Errorf("missing the plain lag image: %v", err)
	}
	if _, err := os.Stat(filepath.Join(s.Cfg.Output.Dir, "core-tnn1/lag/light/peak/1d.png")); err == nil {
		t.Error("a graph without peaks should have no peak variant")
	}
	if _, err := os.Stat(filepath.Join(s.Cfg.Output.Dir, "core-tnn1/lag/dark/plain/1d.png")); err != nil {
		t.Errorf("missing the dark lag image: %v", err)
	}

	// The peak traces are a second set of targets, and they are asked for.
	var joined string
	for _, q := range src.queries() {
		joined += q + "\n"
	}
	if !strings.Contains(joined, "max_over_time((") {
		t.Error("no peak query was issued")
	}
}

// The palette and the peak traces change how samples are drawn, not which are
// read, so a page's worth of images must not cost a query each.
func TestVariantsShareOneFetch(t *testing.T) {
	src := newSource(t)
	src.labels = []string{"tnn"}
	s := buildSplit(t, src, "")
	if err := s.Render(context.Background()); err != nil {
		t.Fatalf("Render: %v", err)
	}

	images := 0
	_ = filepath.Walk(s.Cfg.Output.Dir, func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && strings.HasSuffix(p, ".png") {
			images++
		}
		return nil
	})
	if queries := len(src.queries()); queries >= images {
		t.Errorf("%d queries for %d images: the fetch is not being shared", queries, images)
	}
}

// Whatever a page points at has to be there, in every palette and variant the
// buttons can reach.
func TestEveryImageAPageOffersExists(t *testing.T) {
	src := newSource(t)
	src.labels = []string{"tnn", "tyo"}
	s := buildSplit(t, src, "")
	if err := s.Render(context.Background()); err != nil {
		t.Fatalf("Render: %v", err)
	}

	root := s.Cfg.Output.Dir
	imgRE := regexp.MustCompile(`data-base="([^"]+)"\s+data-range="([^"]+)"`)
	hrefRE := regexp.MustCompile(`href="([^"#:]+\.html)"`)
	checked := 0

	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(p, ".html") {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		dir := path.Dir(filepath.ToSlash(rel))
		body, err := os.ReadFile(p)
		if err != nil {
			return err
		}

		for _, m := range imgRE.FindAllStringSubmatch(string(body), -1) {
			for _, theme := range []string{"light", "dark"} {
				for _, variant := range []string{"plain", "peak"} {
					u := path.Join(dir, m[1], theme, variant, m[2]+".png")
					// A graph without peaks offers no peak button.
					if variant == "peak" {
						if _, e := os.Stat(filepath.Join(root, filepath.FromSlash(
							path.Join(dir, m[1], "light", "peak", m[2]+".png")))); e != nil {
							continue
						}
					}
					checked++
					if _, e := os.Stat(filepath.Join(root, filepath.FromSlash(u))); e != nil {
						t.Errorf("%s offers %s, which is not there", rel, u)
					}
				}
			}
		}
		for _, m := range hrefRE.FindAllStringSubmatch(string(body), -1) {
			t := path.Join(dir, m[1])
			if _, e := os.Stat(filepath.Join(root, filepath.FromSlash(t))); e != nil {
				return fmt.Errorf("%s links %s, which is not there", rel, t)
			}
		}
		return nil
	})
	if err != nil {
		t.Error(err)
	}
	if checked == 0 {
		t.Fatal("no images were checked")
	}
	t.Logf("checked %d image URLs", checked)
}

// The time under a page should agree with the time along the axes above it,
// so both follow the config rather than whatever zone the container runs in.
func TestFooterFollowsTheConfiguredTimezone(t *testing.T) {
	for _, tc := range []struct{ tz, want string }{
		{"Asia/Taipei", "CST"},
		{"UTC", "UTC"},
		{"America/New_York", ""}, // EST or EDT, depending on the date
	} {
		t.Run(tc.tz, func(t *testing.T) {
			s := build(t, newSource(t), 2, 0, "")
			s.Cfg.Defaults.TZ = tc.tz
			if err := s.Render(context.Background()); err != nil {
				t.Fatalf("Render: %v", err)
			}

			index := read(t, s, "index.html")
			if !strings.Contains(index, "Updated ") {
				t.Error("the footer does not say when it was updated")
			}
			loc, err := time.LoadLocation(tc.tz)
			if err != nil {
				t.Fatalf("LoadLocation: %v", err)
			}
			want := tc.want
			if want == "" {
				want = time.Now().In(loc).Format("MST")
			}
			stamp := time.Now().In(loc).Format("2006-01-02 15:")
			if !strings.Contains(index, stamp) || !strings.Contains(index, want) {
				t.Errorf("footer is not in %s: want %q and %q", tc.tz, stamp, want)
			}
		})
	}
}

// The peak switch belongs to a drawing, not to a page: a detail page shows
// four timescales and each is toggled on its own.
func TestPeakSwitchesPerDrawing(t *testing.T) {
	src := newSource(t)
	src.labels = []string{"tnn"}
	s := buildSplit(t, src, "")
	if err := s.Render(context.Background()); err != nil {
		t.Fatalf("Render: %v", err)
	}

	page := read(t, s, "core-tnn1/traffic.html")
	buttons := strings.Count(page, `<button type="button" data-peak-toggle`)
	figures := strings.Count(page, `<figure data-key=`)
	if figures == 0 {
		t.Fatal("the detail page has no figures")
	}
	if buttons != figures {
		t.Errorf("%d buttons for %d drawings: the switch is not per drawing", buttons, figures)
	}

	// Each carries its own key, so one timescale's choice is not another's.
	for _, want := range []string{`data-key="core-tnn1/traffic/1d"`} {
		if !strings.Contains(page, want) {
			t.Errorf("page is missing %s", want)
		}
	}
	if strings.Contains(page, `<div data-key=`) {
		t.Error("a page-wide wrapper still governs every drawing at once")
	}

	// A graph without peaks offers no button at all.
	if lag := read(t, s, "core-tnn1/lag.html"); strings.Contains(lag, "data-peak-toggle") &&
		strings.Contains(lag, `<button type="button" data-peak-toggle`) {
		t.Error("a graph without peaks should offer no peak button")
	}
}

// A region is named once, in the config, and that name is what the site is
// built from: the paths, the links and the captions. The label value stays in
// the query, where the source knows it.
func TestPathsFollowTheConfiguredName(t *testing.T) {
	src := newSource(t)
	src.labels = []string{"tnn"}
	s := buildSplit(t, src, "")
	if err := s.Render(context.Background()); err != nil {
		t.Fatalf("Render: %v", err)
	}

	// Nothing is filed under the raw label value.
	for _, gone := range []string{"tnn", "region/tnn.html", "tnn/traffic.html"} {
		if _, err := os.Stat(filepath.Join(s.Cfg.Output.Dir, gone)); err == nil {
			t.Errorf("%s exists: the raw label value is still shaping the site", gone)
		}
	}
	for _, want := range []string{
		"region/core-tnn1.html", "core-tnn1/traffic.html",
		"core-tnn1/traffic/light/plain/1d.png",
	} {
		if _, err := os.Stat(filepath.Join(s.Cfg.Output.Dir, want)); err != nil {
			t.Errorf("missing %s", want)
		}
	}

	if index := read(t, s, "index.html"); strings.Contains(index, `"region/tnn.html"`) {
		t.Error("index.html links the raw label value")
	}
	// But the query still asks the source what it understands.
	var joined string
	for _, q := range src.queries() {
		joined += q + "\n"
	}
	if !strings.Contains(joined, `region="tnn"`) {
		t.Error("the query no longer uses the label value")
	}
	if strings.Contains(joined, `region="core-tnn1"`) {
		t.Error("the presentation name leaked into the query")
	}
}

// A name becomes a path segment, so one that cannot be is refused at load
// rather than turning into something else in the URL.
func TestUnusableRegionNamesAreRefused(t *testing.T) {
	for _, name := range []string{"a/b", "../x", "", "with space", "region"} {
		if _, err := config.Parse([]byte(`
regions:
  label: region
  titles: {tnn: "` + name + `"}
graphs:
  - name: a
    series: [{expr: up}]
`)); err == nil {
			t.Errorf("accepted %q as a region name", name)
		}
	}
}

// The stamp on a drawing is in the zone the graph is drawn in, and every
// drawing in a pass carries the same one.
func TestDrawingsAreStampedInTheConfiguredZone(t *testing.T) {
	s := build(t, newSource(t), 4, 0, "")
	s.Cfg.Defaults.TZ = "Asia/Taipei"
	if err := s.Render(context.Background()); err != nil {
		t.Fatalf("Render: %v", err)
	}

	got := s.stampedAt()
	want := time.Now().In(s.Cfg.Location()).Format("2006-01-02 15:")
	if !strings.HasPrefix(got, "Updated ") || !strings.Contains(got, want) {
		t.Errorf("stamp = %q, want it to carry %q in Asia/Taipei", got, want)
	}
	if !strings.Contains(got, "CST") {
		t.Errorf("stamp = %q, want the configured zone rather than the machine's", got)
	}

	// The page footer and the drawings agree to the minute.
	if minute := got[len("Updated ") : len("Updated ")+16]; !strings.Contains(read(t, s, "index.html"), minute) {
		t.Errorf("the page and the drawings disagree about when: %q", minute)
	}
}

// Passes are pinned to the clock, not to whenever the process started, and
// they begin a little early so the drawings are in place when the boundary
// arrives. A page works the same boundaries out from the same interval, which
// is what lets the two stay in step without talking.
func TestPassesArePinnedToTheClock(t *testing.T) {
	const lead = 5 * time.Second
	at := func(s string) time.Time {
		t.Helper()
		ts, err := time.Parse("15:04:05", s)
		if err != nil {
			t.Fatal(err)
		}
		return ts
	}

	for _, tc := range []struct{ every, now, want string }{
		{"5m", "10:00:00", "10:04:55"},
		{"5m", "10:04:54", "10:04:55"},
		{"5m", "10:04:55", "10:09:55"}, // on the mark, the next one is next
		{"5m", "10:07:30", "10:09:55"},
		{"1m", "10:07:30", "10:07:55"},
		{"15s", "10:07:30", "10:07:40"},
	} {
		every, err := time.ParseDuration(tc.every)
		if err != nil {
			t.Fatal(err)
		}
		got := nextRender(at(tc.now), every, lead)
		if want := at(tc.want); !got.Equal(want) {
			t.Errorf("every %s at %s: next = %s, want %s",
				tc.every, tc.now, got.Format("15:04:05"), tc.want)
		}
	}
}

// The page needs one number to work out when to look again, and no side file
// to ask.
func TestPagesCarryOnlyTheInterval(t *testing.T) {
	s := build(t, newSource(t), 4, 0, "")
	s.Cfg.Output.Interval = config.Duration(5 * time.Minute)
	if err := s.Render(context.Background()); err != nil {
		t.Fatalf("Render: %v", err)
	}

	if _, err := os.Stat(filepath.Join(s.Cfg.Output.Dir, "version.json")); err == nil {
		t.Error("version.json is still being published")
	}
	for _, name := range []string{"index.html", "traffic.html"} {
		body := read(t, s, name)
		if !strings.Contains(body, `data-interval="300"`) {
			t.Errorf("%s does not carry the interval", name)
		}
		// A query on an image URL would give every cache in front of the site
		// a fresh key on each redraw.
		if strings.Contains(body, ".png?") {
			t.Errorf("%s carries a query on an image URL", name)
		}
		// The page may fetch the drawings themselves -- that is how the
		// browser's own copy gets replaced -- but nothing else. When the next
		// pass happens is arithmetic, not a question for the server.
		if strings.Contains(body, ".json") {
			t.Errorf("%s asks for a side file it could work out itself", name)
		}
	}
}

// Cloudflare's Rocket Loader rewrites inline scripts to a type the browser
// will not run and executes them itself, later and out of order. A page whose
// timer and toggles live in those scripts simply stops working, so every one
// of them opts out.
func TestInlineScriptsOptOutOfRocketLoader(t *testing.T) {
	s := build(t, newSource(t), 2, 0, "")
	if err := s.Render(context.Background()); err != nil {
		t.Fatalf("Render: %v", err)
	}
	for _, name := range []string{"index.html", "traffic.html"} {
		body := read(t, s, name)
		tags := strings.Count(body, "<script")
		opted := strings.Count(body, `<script data-cfasync="false"`)
		if tags == 0 {
			t.Fatalf("%s has no scripts", name)
		}
		if opted != tags {
			t.Errorf("%s: %d of %d scripts opt out of Rocket Loader", name, opted, tags)
		}
	}
}

// A browser keeps a second cache for images, keyed by URL and above HTTP
// semantics, so neither a new element nor a fetch of that URL can refresh what
// is on screen. The bytes have to reach the element directly.
func TestDrawingsAreHandedToTheElementDirectly(t *testing.T) {
	s := build(t, newSource(t), 2, 0, "")
	s.Cfg.Output.Interval = config.Duration(15 * time.Second)
	if err := s.Render(context.Background()); err != nil {
		t.Fatalf("Render: %v", err)
	}
	body := read(t, s, "traffic.html")

	for _, want := range []string{
		"createObjectURL", // the bytes go to the element, not through its URL
		"revokeObjectURL", // and the one before is let go
		"cache: 'reload'", // fetched past the browser's own copy
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the page is missing %s", want)
		}
	}
	// The address on the wire must stay put, or every cache in front of the
	// site takes a fresh key on each redraw.
	if strings.Contains(body, ".png?") {
		t.Error("an image URL carries a query")
	}
}

// The pages letter themselves in the same face the drawings are lettered in,
// which only holds if the font travels with them: a reader without it
// installed has nowhere else to get it from. Writing it again on every pass
// would be its own bug -- the timestamp would move each interval and cost
// every reader a revalidation of a file that never changes.
func TestPagesCarryTheFontTheyAreLetteredIn(t *testing.T) {
	src := newSource(t)
	src.labels = []string{"tnn", "tyo"}
	s := buildSplit(t, src, "")
	if err := s.Render(context.Background()); err != nil {
		t.Fatalf("Render: %v", err)
	}

	root := s.Cfg.Output.Dir
	fontRE := regexp.MustCompile(`url\("([^"]+\.woff2)"\)`)
	seen := map[string]bool{}

	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(p, ".html") {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		dir := path.Dir(filepath.ToSlash(rel))
		body, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		found := fontRE.FindAllStringSubmatch(string(body), -1)
		if len(found) == 0 {
			return fmt.Errorf("%s asks for no font", rel)
		}
		for _, m := range found {
			u := path.Join(dir, m[1])
			seen[path.Base(u)] = true
			if _, e := os.Stat(filepath.Join(root, filepath.FromSlash(u))); e != nil {
				t.Errorf("%s asks for %s, which is not there", rel, u)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(seen) < 2 {
		t.Errorf("pages ask for %d fonts, want the regular and the bold", len(seen))
	}

	before := stamps(t, filepath.Join(root, "fonts"))
	if err := s.Render(context.Background()); err != nil {
		t.Fatalf("second Render: %v", err)
	}
	if after := stamps(t, filepath.Join(root, "fonts")); !maps.Equal(before, after) {
		t.Error("a redraw rewrote the fonts, which moves their timestamps for nothing")
	}
}

// stamps is the modification time of every file in dir, by name.
func stamps(t *testing.T, dir string) map[string]time.Time {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}
	out := map[string]time.Time{}
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			t.Fatalf("stat %s: %v", e.Name(), err)
		}
		out[e.Name()] = info.ModTime()
	}
	return out
}
