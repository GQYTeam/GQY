package host

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultRegistryUsesToolPolicy(t *testing.T) {
	ws := t.TempDir()
	home := filepath.Join(ws, "home")
	outside := t.TempDir()
	t.Setenv("GQY_WORKSPACE", ws)
	t.Setenv("GQY_HOME", home)
	// 把 home 显式加入允许根，验证 token 仍被 Denied 拦住。
	t.Setenv("GQY_TOOL_ROOTS", home)

	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	token := filepath.Join(home, "github_token.json")
	if err := os.WriteFile(token, []byte(`{"access_token":"secret"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	notes := filepath.Join(home, "notes.txt")
	if err := os.WriteFile(notes, []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}

	reg, err := DefaultRegistry()
	if err != nil {
		t.Fatalf("DefaultRegistry: %v", err)
	}
	read, ok := reg.Get("read")
	if !ok {
		t.Fatal("read 未注册")
	}

	if _, err := read.Execute(context.Background(), map[string]any{
		"path": filepath.Join(outside, "secret.txt"),
	}); err == nil || !strings.Contains(err.Error(), "允许范围") {
		t.Fatalf("允许根外路径应被拒绝，实际: %v", err)
	}
	if _, err := read.Execute(context.Background(), map[string]any{"path": "home/notes.txt"}); err != nil {
		t.Fatalf("允许根内文件应可读: %v", err)
	}
	if _, err := read.Execute(context.Background(), map[string]any{"path": token}); err == nil ||
		!strings.Contains(err.Error(), "受保护路径") {
		t.Fatalf("GitHub token 应被拒绝，实际: %v", err)
	}
}
