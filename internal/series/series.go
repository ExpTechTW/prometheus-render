// Package series holds the in-memory representation shared between the query
// layer and the renderer: a dense, fixed-step run of samples.
package series

import (
	"regexp"
	"sort"
	"strings"
)

// Series is one metric sampled on a regular grid. Values has one entry per step
// slot from Start (inclusive) to Stop (exclusive), with NaN marking a gap.
type Series struct {
	Name   string
	Labels map[string]string

	Start int64 // Unix seconds of the first slot
	Stop  int64 // Unix seconds one step past the last slot
	Step  int64 // seconds between slots

	Values []float64
}

// legendVarRE matches Grafana-style placeholders such as {{instance}}.
var legendVarRE = regexp.MustCompile(`\{\{\s*([a-zA-Z_][a-zA-Z0-9_]*)\s*\}\}`)

// FormatLegend renders a series name. An empty format yields Prometheus'
// canonical `name{label="value"}` notation; otherwise {{label}} placeholders in
// the format are substituted, and unknown labels collapse to an empty string.
func FormatLegend(format string, labels map[string]string) string {
	if format == "" {
		return defaultLegend(labels)
	}
	return legendVarRE.ReplaceAllStringFunc(format, func(m string) string {
		key := legendVarRE.FindStringSubmatch(m)[1]
		if key == "name" {
			if v, ok := labels["__name__"]; ok {
				return v
			}
		}
		return labels[key]
	})
}

func defaultLegend(labels map[string]string) string {
	metric := labels["__name__"]

	keys := make([]string, 0, len(labels))
	for k := range labels {
		if k == "__name__" {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)

	if len(keys) == 0 {
		if metric == "" {
			return "{}"
		}
		return metric
	}

	var b strings.Builder
	b.WriteString(metric)
	b.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(k)
		b.WriteString(`="`)
		b.WriteString(labels[k])
		b.WriteByte('"')
	}
	b.WriteByte('}')
	return b.String()
}
