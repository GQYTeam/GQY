package httpapi

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gqy/internal/plugin"
)

// TestPluginCallResponseHasDuration 断言 /api/plugins/{id}/call 成功响应带 duration_ms。
func TestPluginCallResponseHasDuration(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node 不可用，跳过插件 call 测试")
	}
	home := t.TempDir()
	dir := filepath.Join(home, "plugins", "echo")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join("..", "..", "plugins", "echo")
	for _, name := range []string{"index.js", "manifest.json"} {
		data, err := os.ReadFile(filepath.Join(src, name))
		if err != nil {
			t.Fatalf("读示例插件 %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	pm := plugin.NewManager(home)
	if err := pm.Scan(); err != nil {
		t.Fatal(err)
	}
	if err := pm.Load(context.Background(), "echo"); err != nil {
		t.Fatalf("Load: %v", err)
	}

	srv := &Server{plugins: pm}
	req := httptest.NewRequest("POST", "/api/plugins/echo/call", strings.NewReader(`{"action":"echo","arguments":{"text":"hi"}}`))
	req.SetPathValue("id", "echo")
	rec := httptest.NewRecorder()
	srv.handlePluginCall(rec, req)

	if rec.Code != 200 {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["ok"] != true {
		t.Fatalf("ok = %v", body["ok"])
	}
	if _, ok := body["duration_ms"]; !ok {
		t.Fatalf("成功响应应含 duration_ms: %s", rec.Body.String())
	}
}
