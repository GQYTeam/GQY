package host

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	entries, err := os.ReadDir(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dst, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		s := filepath.Join(src, e.Name())
		d := filepath.Join(dst, e.Name())
		if e.IsDir() {
			copyTree(t, s, d)
			continue
		}
		b, err := os.ReadFile(s)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(d, b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestDefaultRegistryForToolLoadsPlugin(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("没有 node，跳过插件评测宿主测试")
	}
	home := t.TempDir()
	src := filepath.Join("..", "..", "..", "plugins", "echo")
	copyTree(t, src, filepath.Join(home, "plugins", "echo"))
	t.Setenv("GQY_HOME", home)

	reg, cleanup, warnings, err := DefaultRegistryForTool("echo__echo")
	if err != nil {
		t.Fatalf("registry: %v", err)
	}
	defer cleanup()
	if len(warnings) != 0 {
		t.Fatalf("warnings: %v", warnings)
	}
	tool, ok := reg.Get("echo__echo")
	if !ok {
		names := make([]string, 0, 8)
		for _, tl := range reg.List() {
			names = append(names, tl.Name())
		}
		t.Fatalf("echo__echo 未注册；当前: %v", names)
	}
	out, err := tool.Execute(context.Background(), map[string]any{"text": "hi"})
	if err != nil || out != "hi" {
		t.Fatalf("execute: %q %v", out, err)
	}
	if _, ok := reg.Get("plugin__echo__echo"); !ok {
		t.Fatal("内置占位工具应保留，避免旧 testdata 失效")
	}
}
