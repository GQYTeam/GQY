package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"gqy/internal/metrics"
)

func TestMetricsEndpoint(t *testing.T) {
	metrics.Default.Reset()
	e := newTestServer(t, "", nil)
	// 先产生一条 HTTP 指标；accessLog 在响应写出后记录，所以 /metrics 本身不会出现在首次抓取里。
	if rec := e.request(http.MethodGet, "/healthz", nil, nil); rec.Code != http.StatusOK {
		t.Fatalf("/healthz = %d", rec.Code)
	}
	rec := e.request(http.MethodGet, "/metrics", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("/metrics = %d: %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "text/plain") {
		t.Fatalf("Content-Type = %q", ct)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"gqy_build_info", "gqy_config_version",
		"gqy_http_requests_total",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("缺少 %q:\n%s", want, body)
		}
	}
}

func TestMetricsGatewayGauges(t *testing.T) {
	s := newChatServer(t, &chatFakeAdapter{})
	if rec := postChat(s, `{"model":"p/m1","messages":[{"role":"user","content":"hi"}]}`); rec.Code != http.StatusOK {
		t.Fatalf("chat = %d: %s", rec.Code, rec.Body.String())
	}
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	req.RemoteAddr = "127.0.0.1:12345"
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("/metrics = %d", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{"gqy_gateway_cooldown_seconds", "gqy_gateway_inflight", "gqy_gateway_calls_total", "gqy_gateway_attempts_total"} {
		if !strings.Contains(body, want) {
			t.Fatalf("缺少 %q", want)
		}
	}
}

func TestMetricsRequiresAuth(t *testing.T) {
	e := newTestServer(t, "metrics-token", nil)
	rec := e.request(http.MethodGet, "/metrics", nil, nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("无令牌 /metrics = %d, want 401", rec.Code)
	}
	rec = e.request(http.MethodGet, "/metrics", nil, map[string]string{
		"Authorization": "Bearer metrics-token",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("Bearer /metrics = %d", rec.Code)
	}
}

func TestMetricPathNormalization(t *testing.T) {
	cases := map[string]string{
		"/api/sessions/abc123":          "/api/sessions/{id}",
		"/api/sessions/abc123/draft":    "/api/sessions/{id}/draft",
		"/api/plugins/echo/settings":    "/api/plugins/{id}/settings",
		"/api/plugins/install":          "/api/plugins/install",
		"/api/memory/xiaoying/facts":    "/api/memory/{persona}/facts",
		"/api/memory/xiaoying/facts/42": "/api/memory/{persona}/facts/{id}",
		"/v1beta/models/gemini-1.5:gen": "/v1beta/models/{model}",
		"/api/gateway/status":           "/api/gateway/status",
	}
	for in, want := range cases {
		if got := metricPath(in); got != want {
			t.Fatalf("metricPath(%q) = %q, want %q", in, got, want)
		}
	}
}
