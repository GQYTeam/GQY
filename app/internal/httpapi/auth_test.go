package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"gqy/internal/config"
)

func TestAuthMiddleware(t *testing.T) {
	s := &Server{Cfg: config.Config{AuthToken: "secret"}}
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	h := s.withAuth(next)

	cases := []struct {
		name   string
		path   string
		setup  func(*http.Request)
		status int
	}{
		{"no token", "/api/health", nil, 401},
		{"bearer", "/api/health", func(r *http.Request) { r.Header.Set("Authorization", "Bearer secret") }, 200},
		{"header", "/v1/models", func(r *http.Request) { r.Header.Set("X-GQY-Token", "secret") }, 200},
		{"cookie", "/api/health", func(r *http.Request) { r.AddCookie(&http.Cookie{Name: authCookie, Value: "secret"}) }, 200},
		{"gemini no token", "/v1beta/models/x:generateContent", nil, 401},
		{"gemini bearer", "/v1beta/models/x:generateContent", func(r *http.Request) { r.Header.Set("Authorization", "Bearer secret") }, 200},
		{"static", "/index.html", nil, 200},
		{"auth endpoint", "/api/auth", nil, 200},
	}
	for _, c := range cases {
		req := httptest.NewRequest("GET", c.path, nil)
		if c.setup != nil {
			c.setup(req)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != c.status {
			t.Errorf("%s: status=%d want %d", c.name, rec.Code, c.status)
		}
	}
}

func TestHandleAuth(t *testing.T) {
	s := &Server{Cfg: config.Config{AuthToken: "secret"}}
	bad := httptest.NewRequest("POST", "/api/auth", strings.NewReader(`{"token":"nope"}`))
	rec := httptest.NewRecorder()
	s.handleAuth(rec, bad)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("bad token status=%d", rec.Code)
	}
	good := httptest.NewRequest("POST", "/api/auth", strings.NewReader(`{"token":"secret"}`))
	rec = httptest.NewRecorder()
	s.handleAuth(rec, good)
	if rec.Code != http.StatusOK {
		t.Fatalf("good token status=%d", rec.Code)
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != authCookie || !cookies[0].HttpOnly {
		t.Fatalf("cookie: %+v", cookies)
	}
}
