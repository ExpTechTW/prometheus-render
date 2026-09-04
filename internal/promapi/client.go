package promapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/ExpTechTW/prometheus-render/internal/series"
)

// Client talks to the Prometheus-compatible HTTP API exposed by Prometheus,
// VictoriaMetrics (vmselect), Thanos, Mimir and friends.
type Client struct {
	BaseURL  string
	HTTP     *http.Client
	Username string
	Password string
	Headers  map[string]string
}

// NewClient returns a Client for the given base URL, e.g.
// "http://localhost:9090" or "http://vmselect:8481/select/0/prometheus".
func NewClient(baseURL string, timeout time.Duration) *Client {
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		HTTP:    &http.Client{Timeout: timeout},
		Headers: map[string]string{},
	}
}

// RangeQuery describes a single /api/v1/query_range call.
type RangeQuery struct {
	Expr  string
	Start time.Time
	End   time.Time
	Step  time.Duration
}

// rawSeries is one time series as the API returns it, before it is placed on a
// regular grid.
type rawSeries struct {
	labels map[string]string
	points []point
}

// point is a single (timestamp, value) sample.
type point struct {
	t float64 // Unix seconds, possibly fractional
	v float64
}

type apiResponse struct {
	Status string  `json:"status"`
	Data   apiData `json:"data"`
	Error  string  `json:"error"`
}

type apiData struct {
	ResultType string      `json:"resultType"`
	Result     []apiResult `json:"result"`
}

type apiResult struct {
	Metric map[string]string `json:"metric"`
	Values [][2]any          `json:"values"`
}

// QueryRange runs a range query and returns the matching series on a dense,
// step-aligned grid.
func (c *Client) QueryRange(ctx context.Context, q RangeQuery) ([]series.Series, error) {
	if q.Step <= 0 {
		return nil, fmt.Errorf("step must be positive")
	}

	form := url.Values{}
	form.Set("query", q.Expr)
	form.Set("start", formatTimestamp(q.Start))
	form.Set("end", formatTimestamp(q.End))
	form.Set("step", strconv.FormatFloat(q.Step.Seconds(), 'f', -1, 64))

	endpoint := c.BaseURL + "/api/v1/query_range"
	// POST keeps long PromQL expressions off the request line.
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	for k, v := range c.Headers {
		req.Header.Set(k, v)
	}
	if c.Username != "" || c.Password != "" {
		req.SetBasicAuth(c.Username, c.Password)
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("query %q: %w", q.Expr, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 256<<20))
	if err != nil {
		return nil, fmt.Errorf("query %q: reading response: %w", q.Expr, err)
	}

	var out apiResponse
	if jsonErr := json.Unmarshal(body, &out); jsonErr != nil {
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("query %q: %s: %s", q.Expr, resp.Status, snippet(body))
		}
		return nil, fmt.Errorf("query %q: decoding response: %w", q.Expr, jsonErr)
	}
	if out.Status != "success" {
		msg := out.Error
		if msg == "" {
			msg = snippet(body)
		}
		return nil, fmt.Errorf("query %q: %s: %s", q.Expr, resp.Status, msg)
	}
	if out.Data.ResultType != "matrix" {
		return nil, fmt.Errorf("query %q returned %q, expected a range vector (matrix); wrap instant selectors in a range query", q.Expr, out.Data.ResultType)
	}

	raw := make([]rawSeries, 0, len(out.Data.Result))
	for _, r := range out.Data.Result {
		s := rawSeries{labels: r.Metric, points: make([]point, 0, len(r.Values))}
		for _, v := range r.Values {
			p, ok := decodePoint(v)
			if !ok {
				continue
			}
			s.points = append(s.points, p)
		}
		raw = append(raw, s)
	}
	return densify(raw, q), nil
}

// decodePoint converts the API's [<unix ts>, "<value>"] pair into a Point.
// Unparsable values (including "NaN") are dropped so they show up as gaps.
func decodePoint(v [2]any) (point, bool) {
	ts, ok := v[0].(float64)
	if !ok {
		return point{}, false
	}
	str, ok := v[1].(string)
	if !ok {
		return point{}, false
	}
	f, err := strconv.ParseFloat(str, 64)
	if err != nil {
		return point{}, false
	}
	return point{t: ts, v: f}, true
}

func formatTimestamp(t time.Time) string {
	return strconv.FormatFloat(float64(t.UnixNano())/1e9, 'f', 3, 64)
}

func snippet(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 300 {
		s = s[:300] + "..."
	}
	return s
}
