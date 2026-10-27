package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"gqy/internal/config"
)

func TestRateLimit(t *testing.T) {
	s := &Server{Cfg: config.Config{RateLimit: 2}}
	h := s.withRateLimit(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	for i, want := range []int{200, 200, 429} {
		req := httptest.NewRequest("GET", "/api/health", nil)
		req.RemoteAddr = "127.0.0.1:1234"
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != want {
			t.Fatalf("request %d: %d want %d", i, rec.Code, want)
		}
	}
	req := httptest.NewRequest("GET", "/index.html", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatal("静态资源不应被限流")
	}
}

func TestRateLimitGeminiPath(t *testing.T) {
	s := &Server{Cfg: config.Config{RateLimit: 1}}
	h := s.withRateLimit(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	for i, want := range []int{200, 429} {
		req := httptest.NewRequest("POST", "/v1beta/models/x:generateContent", nil)
		req.RemoteAddr = "127.0.0.1:1234"
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != want {
			t.Fatalf("request %d: %d want %d", i, rec.Code, want)
		}
	}
}
