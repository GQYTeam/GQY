package metrics

import (
	"bytes"
	"strings"
	"testing"
)

func TestWriteCountersGaugesHistogram(t *testing.T) {
	r := NewRegistry()
	r.Add("gqy_http_requests_total", "HTTP requests", map[string]string{"method": "GET", "path": "/x", "status": "200"}, 2)
	r.Set("gqy_config_version", "Config version", nil, 7)
	r.Observe("gqy_http_request_duration_seconds", "Duration", map[string]string{"method": "GET", "path": "/x"}, 0.05)
	r.Observe("gqy_http_request_duration_seconds", "Duration", map[string]string{"method": "GET", "path": "/x"}, 2)

	var buf bytes.Buffer
	if err := r.WriteText(&buf); err != nil {
		t.Fatalf("WriteText: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		"# TYPE gqy_http_requests_total counter",
		`gqy_http_requests_total{method="GET",path="/x",status="200"} 2`,
		"# TYPE gqy_config_version gauge",
		"gqy_config_version 7",
		"# TYPE gqy_http_request_duration_seconds histogram",
		`gqy_http_request_duration_seconds_bucket{method="GET",path="/x",le="0.05"} 1`,
		`gqy_http_request_duration_seconds_bucket{method="GET",path="/x",le="+Inf"} 2`,
		`gqy_http_request_duration_seconds_count{method="GET",path="/x"} 2`,
		`gqy_http_request_duration_seconds_sum{method="GET",path="/x"} 2.05`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("缺少 %q:\n%s", want, out)
		}
	}
}

func TestLabelEscaping(t *testing.T) {
	r := NewRegistry()
	r.Add("x_total", "x", map[string]string{"label": "a\"b\nc\\d"}, 1)
	var buf bytes.Buffer
	if err := r.WriteText(&buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), `label="a\"b\nc\\d"`) {
		t.Fatalf("转义不对:\n%s", buf.String())
	}
}
