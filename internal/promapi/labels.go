package promapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// labelResponse is /api/v1/label/<name>/values as the API returns it.
type labelResponse struct {
	Status string   `json:"status"`
	Data   []string `json:"data"`
	Error  string   `json:"error"`
}

// LabelValues returns the values a label takes, optionally restricted to the
// series matching a selector such as `{job="nginx"}` and to a time window.
//
// It is how a site discovers what to split itself by, so that adding a node
// changes the pages without anyone editing a config file.
func (c *Client) LabelValues(ctx context.Context, label, match string, start, end time.Time) ([]string, error) {
	if label == "" {
		return nil, fmt.Errorf("no label given")
	}

	if c.Limit != nil {
		select {
		case c.Limit <- struct{}{}:
			defer func() { <-c.Limit }()
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	form := url.Values{}
	if match != "" {
		form.Set("match[]", match)
	}
	if !start.IsZero() {
		form.Set("start", formatTimestamp(start))
	}
	if !end.IsZero() {
		form.Set("end", formatTimestamp(end))
	}

	// GET, not POST: Prometheus only serves this endpoint that way, and the
	// arguments are a label name and at most one selector.
	endpoint := c.BaseURL + "/api/v1/label/" + url.PathEscape(label) + "/values"
	if q := form.Encode(); q != "" {
		endpoint += "?" + q
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	for k, v := range c.Headers {
		req.Header.Set(k, v)
	}
	if c.Username != "" || c.Password != "" {
		req.SetBasicAuth(c.Username, c.Password)
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("label %q: %w", label, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, fmt.Errorf("label %q: reading response: %w", label, err)
	}

	var out labelResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("label %q: %s: %w", label, resp.Status, err)
	}
	if out.Status != "success" {
		if out.Error != "" {
			return nil, fmt.Errorf("label %q: %s", label, out.Error)
		}
		return nil, fmt.Errorf("label %q: %s", label, resp.Status)
	}
	return out.Data, nil
}
