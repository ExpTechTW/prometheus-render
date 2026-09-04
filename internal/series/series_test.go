package series

import "testing"

func TestFormatLegend(t *testing.T) {
	labels := map[string]string{"__name__": "node_load1", "instance": "web-01:9100", "job": "node"}

	tests := []struct {
		format string
		want   string
	}{
		{"", `node_load1{instance="web-01:9100", job="node"}`},
		{"{{instance}}", "web-01:9100"},
		{"{{ instance }}", "web-01:9100"},
		{"{{name}} on {{instance}}", "node_load1 on web-01:9100"},
		{"{{missing}}", ""},
		{"literal", "literal"},
	}
	for _, tc := range tests {
		if got := FormatLegend(tc.format, labels); got != tc.want {
			t.Errorf("FormatLegend(%q) = %q, want %q", tc.format, got, tc.want)
		}
	}
}

func TestDefaultLegendEdgeCases(t *testing.T) {
	if got := FormatLegend("", map[string]string{"__name__": "up"}); got != "up" {
		t.Errorf("bare metric name = %q, want %q", got, "up")
	}
	if got := FormatLegend("", map[string]string{}); got != "{}" {
		t.Errorf("empty labels = %q, want %q", got, "{}")
	}
	// Labels are sorted so a name is stable across runs.
	labels := map[string]string{"b": "2", "a": "1", "c": "3"}
	first := FormatLegend("", labels)
	for i := 0; i < 5; i++ {
		if got := FormatLegend("", labels); got != first {
			t.Fatalf("legend is not deterministic: %q vs %q", first, got)
		}
	}
	if first != `{a="1", b="2", c="3"}` {
		t.Errorf("labels not sorted: %q", first)
	}
}
