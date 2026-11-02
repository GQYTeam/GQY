package httpapi

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gqy/internal/config"
	"gqy/internal/gateway"
	"gqy/internal/provider"
	"gqy/internal/provider/openai"
)

func newRealChatServer(t *testing.T, upstream *httptest.Server) *Server {
	t.Helper()
	home := t.TempDir()
	providers := fmt.Sprintf(`{
	  "providers": [{
	    "id": "any-provider",
	    "display_name": "任意供应商",
	    "base_url": %q,
	    "protocol": "openai-chat",
	    "default_model": "m1",
	    "models": ["m1"],
	    "accounts": [{"id": "a1", "api_key": "key-real"}]
	  }]
	}`, upstream.URL+"/v1")
	files := map[string]string{
		"providers.json": providers,
		"routing.json":   `{"default":{"provider":"any-provider","model":"m1"}}`,
		"settings.json":  `{}`,
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(home, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	startup := config.Startup{
		Home:      home,
		Host:      config.DefaultHost,
		Port:      config.DefaultPort,
		Workspace: home,
		APIBase:   config.DefaultAPIBase,
		Model:     config.DefaultModel,
	}
	mgr, err := config.NewManager(startup, func(string) string { return "" })
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	registry := provider.NewRegistry(openai.NewAdapter(upstream.Client()))
	gw := gateway.New(registry, logger, home)
	return New(mgr, gw, logger)
}

func TestRealOpenAIAdapterEndToEndNonStream(t *testing.T) {
	var sawAuth string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAuth = r.Header.Get("Authorization")
		var body struct {
			Model  string `json:"model"`
			Stream bool   `json:"stream"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Model != "m1" || body.Stream {
			t.Errorf("上游请求 = %+v", body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{
		  "choices":[{
		    "message":{"content":"上游你好","reasoning_content":"上游想"},
		    "finish_reason":"stop"
		  }],
		  "usage":{"prompt_tokens":4,"completion_tokens":2}
		}`)
	}))
	defer upstream.Close()

	s := newRealChatServer(t, upstream)
	rec := postChat(s, `{"model":"m1","messages":[{"role":"user","content":"hi"}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if sawAuth != "Bearer key-real" {
		t.Fatalf("上游 Authorization = %q", sawAuth)
	}
	if got := rec.Header().Get("X-GQY-Provider"); got != "any-provider" {
		t.Fatalf("X-GQY-Provider = %q", got)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"content":"上游你好"`) || !strings.Contains(body, `"reasoning_content":"上游想"`) {
		t.Fatalf("响应 = %s", body)
	}
}

func TestRealOpenAIAdapterEndToEndStream(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		frames := []string{
			`data: {"choices":[{"delta":{"reasoning_content":"上游想"}}]}`,
			`data: {"choices":[{"delta":{"content":"你"}}]}`,
			`data: {"choices":[{"delta":{"content":"好"}}]}`,
			`data: {"choices":[{"delta":{},"finish_reason":"stop"}]}`,
			`data: {"choices":[],"usage":{"prompt_tokens":4,"completion_tokens":2}}`,
			`data: [DONE]`,
		}
		for _, frame := range frames {
			if _, err := io.WriteString(w, frame+"\n\n"); err != nil {
				return
			}
			w.(http.Flusher).Flush()
		}
	}))
	defer upstream.Close()

	s := newRealChatServer(t, upstream)
	rec := postChat(s, `{
	  "model":"m1",
	  "messages":[{"role":"user","content":"hi"}],
	  "stream":true,
	  "stream_options":{"include_usage":true}
	}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		`"reasoning_content":"上游想"`,
		`"content":"你"`,
		`"content":"好"`,
		`"usage":{"prompt_tokens":4,"completion_tokens":2,"total_tokens":6}`,
		`"finish_reason":"stop"`,
		`data: [DONE]`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("缺少 %q:\n%s", want, body)
		}
	}
}
