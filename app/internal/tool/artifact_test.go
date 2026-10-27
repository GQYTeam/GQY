package tool

import (
	"context"
	"os"
	"path/filepath"
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
