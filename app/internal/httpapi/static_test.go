package httpapi

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStaticServing(t *testing.T) {
	e := newTestServer(t, "", nil)
	web := t.TempDir()
	if err := os.WriteFile(filepath.Join(web, "index.html"), []byte("<!doctype html><title>GQY</title>"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(web, "js"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(web, "js", "app.js"), []byte("export const ok = true;"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(web, ".env"), []byte("SECRET=x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(web, "manifest.webmanifest"), []byte(`{"name":"GQY"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	e.srv.SetWebDir(web)

	rec := e.request(http.MethodGet, "/", nil, nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "<title>GQY</title>") {
		t.Fatalf("GET / = %d: %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Fatalf("Content-Type = %q", ct)
	}

	rec = e.request(http.MethodGet, "/js/app.js", nil, nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "export const ok") {
		t.Fatalf("GET /js/app.js = %d: %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "javascript") {
		t.Fatalf("JS Content-Type = %q", ct)
	}

	rec = e.request(http.MethodGet, "/manifest.webmanifest", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("manifest = %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "application/manifest+json") {
		t.Fatalf("manifest Content-Type = %q", ct)
	}

	for _, path := range []string{"/.env", "/secret.txt", "/js/../.env"} {
		rec = e.request(http.MethodGet, path, nil, nil)
		if rec.Code == http.StatusOK {
			t.Fatalf("%s 不应被静态托管", path)
		}
	}
}

func TestStaticDisabled(t *testing.T) {
	e := newTestServer(t, "", nil)
	rec := e.request(http.MethodGet, "/", nil, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("未设置 web dir 时应 404，得到 %d", rec.Code)
	}
}
