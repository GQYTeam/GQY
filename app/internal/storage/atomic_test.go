package storage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnsureRootPermissions(t *testing.T) {
	home := filepath.Join(t.TempDir(), "gqy-home")
	if err := EnsureRoot(home); err != nil {
		t.Fatalf("EnsureRoot: %v", err)
	}
	for _, dir := range []string{
		home,
		filepath.Join(home, "gateway"),
		filepath.Join(home, "sessions"),
		filepath.Join(home, "plugins"),
	} {
		info, err := os.Stat(dir)
		if err != nil {
			t.Fatalf("stat %s: %v", dir, err)
		}
		if info.Mode().Perm() != 0o700 {
			t.Fatalf("%s 权限 = %o, want 700", dir, info.Mode().Perm())
		}
	}
}

func TestAtomicWriteFileReplaces(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "cfg.json")

	if err := AtomicWriteFile(target, []byte("old"), 0o600); err != nil {
		t.Fatalf("首次写入: %v", err)
	}
	if data, _ := os.ReadFile(target); string(data) != "old" {
		t.Fatalf("内容 = %q, want old", data)
	}

	if err := AtomicWriteFile(target, []byte("new"), 0o600); err != nil {
		t.Fatalf("二次写入: %v", err)
	}
	if data, _ := os.ReadFile(target); string(data) != "new" {
		t.Fatalf("内容 = %q, want new", data)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".gqy-tmp-") {
			t.Fatalf("残留临时文件: %s", e.Name())
		}
	}
}
