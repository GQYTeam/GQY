package tool

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestArtifactWritesFiles(t *testing.T) {
	dir := t.TempDir()
	a := Artifact{Policy: Policy{Workspace: dir, Roots: []string{dir}}}
	if _, err := a.Execute(context.Background(), map[string]any{
		"title": "demo",
		"files": []any{
			map[string]any{"path": "src/main.go", "content": "package main"},
			map[string]any{"path": "README.md", "content": "# demo"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"src/main.go", "README.md"} {
		if _, err := os.Stat(filepath.Join(dir, p)); err != nil {
			t.Fatalf("%s: %v", p, err)
		}
	}
	if _, err := a.Execute(context.Background(), map[string]any{
		"title": "bad", "files": []any{map[string]any{"path": "../escape", "content": "x"}},
	}); err == nil {
		t.Fatal("越界路径应被拒绝")
	}
}

func TestArtifactTotalLimit(t *testing.T) {
	dir := t.TempDir()
	a := Artifact{Policy: Policy{Workspace: dir, Roots: []string{dir}}}
	chunk := strings.Repeat("x", 1<<20+1) // 单文件 < 2MB，两个合计 > 2MB

	_, err := a.Execute(context.Background(), map[string]any{
		"title": "big",
		"files": []any{
			map[string]any{"path": "a.txt", "content": chunk},
			map[string]any{"path": "b.txt", "content": chunk},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "总量") {
		t.Fatalf("应因总量超限失败，实际: %v", err)
	}
	// 校验失败时不能留下写了一半的产物。
	if _, statErr := os.Stat(filepath.Join(dir, "a.txt")); !os.IsNotExist(statErr) {
		t.Fatalf("总量超限不应写入任何文件，stat err=%v", statErr)
	}
}
