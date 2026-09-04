package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ExpTechTW/prometheus-render/internal/params"
	"github.com/ExpTechTW/prometheus-render/internal/promapi"
	"github.com/ExpTechTW/prometheus-render/tsgraph"
)

// fakePrometheus serves a query_range response with one ramping series.
func fakePrometheus(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("upstream: %v", err)
		}
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
}

func newTestServer(upstream string) *Server {
	return &Server{Client: promapi.NewClient(upstream, 10*time.Second), Defaults: params.Defaults{}}
}

func do(t *testing.T, s *Server, q url.Values) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/render?"+q.Encode(), nil))
	return rec
}

func TestRenderReturnsPNG(t *testing.T) {
	up := fakePrometheus(t)
	defer up.Close()

	rec := do(t, newTestServer(up.URL), url.Values{
		"target": {"up"}, "legend": {"{{instance}}"},
		"from": {"-1h"}, "width": {"400"}, "height": {"180"},
	})

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "image/png" {
		t.Errorf("Content-Type = %q, want image/png", ct)
	}
	if body := rec.Body.Bytes(); len(body) < 8 || string(body[1:4]) != "PNG" {
		t.Errorf("body is not a PNG (%d bytes)", len(body))
	}
}

func TestRenderAcceptsEveryTheme(t *testing.T) {
	up := fakePrometheus(t)
	defer up.Close()
	s := newTestServer(up.URL)

	for _, tmpl := range tsgraph.ThemeNames() {
		rec := do(t, s, url.Values{"target": {"up"}, "theme": {tmpl}})
		if rec.Code != http.StatusOK {
			t.Errorf("theme %q: status %d: %s", tmpl, rec.Code, rec.Body.String())
		}
	}
}

func TestRenderHonoursRenderParameters(t *testing.T) {
	up := fakePrometheus(t)
	defer up.Close()
	s := newTestServer(up.URL)

	// A stacked, titled graph with the statistics table must differ in size from
	// a bare one, proving the parameters reached rrdtool.
	full := do(t, s, url.Values{
		"target": {"up"}, "title": {"T"}, "vtitle": {"V"}, "area": {"stacked"},
		"width": {"600"}, "height": {"300"},
	})
	bare := do(t, s, url.Values{"target": {"up"}, "graphOnly": {"true"}, "width": {"200"}, "height": {"100"}})
	if full.Code != http.StatusOK || bare.Code != http.StatusOK {
		t.Fatalf("status %d / %d", full.Code, bare.Code)
	}
	if full.Body.Len() <= bare.Body.Len() {
		t.Errorf("parameters not honoured: full %d bytes, bare %d bytes", full.Body.Len(), bare.Body.Len())
	}

}

func TestRenderBadRequests(t *testing.T) {
	up := fakePrometheus(t)
	defer up.Close()
	s := newTestServer(up.URL)

	tests := map[string]url.Values{
		"missing target": {},
		"bad max points": {"target": {"up"}, "step": {"0"}},
		"bad from":       {"target": {"up"}, "from": {"bogus"}},
		"bad until":      {"target": {"up"}, "until": {"bogus"}},
		"bad step":       {"target": {"up"}, "step": {"-5"}},
		"bad tz":         {"target": {"up"}, "tz": {"Mars/Olympus"}},
		"bad area":       {"target": {"up"}, "area": {"bogus"}},
	}
	for name, q := range tests {
		if rec := do(t, s, q); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400", name, rec.Code)
		}
	}
}

func TestRenderUpstreamFailure(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"status":"error","errorType":"bad_data","error":"parse error"}`))
	}))
	defer up.Close()

	rec := do(t, newTestServer(up.URL), url.Values{"target": {"!!!"}})
	if rec.Code != http.StatusBadGateway {
		t.Errorf("status %d, want 502", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "parse error") {
		t.Errorf("upstream error not surfaced: %s", rec.Body.String())
	}
}

func TestHealthz(t *testing.T) {
	rec := httptest.NewRecorder()
	newTestServer("http://example.invalid").Handler().
		ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("status %d, want 200", rec.Code)
	}
}
