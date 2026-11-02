package storage

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeManifest(t *testing.T, dir, created string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	body := `{"session_id":"x","created_at":"` + created + `","cleanup_policy":{"session_tmp_ttl_days":7,"logs_ttl_days":30}}`
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestCleanExpired(t *testing.T) {
	home := t.TempDir()
	sessions := filepath.Join(home, "sessions")
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)

	old := filepath.Join(sessions, "aaaa")
	fresh := filepath.Join(sessions, "bbbb")
	writeManifest(t, old, now.AddDate(0, 0, -40).Format(time.RFC3339))
	writeManifest(t, fresh, now.AddDate(0, 0, -1).Format(time.RFC3339))

	removed, err := CleanExpired(home, now)
	if err != nil {
		t.Fatalf("CleanExpired: %v", err)
	}
	if removed != 1 {
		t.Fatalf("removed = %d, want 1", removed)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatal("过期会话应被删除")
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Fatalf("未到期会话不应被删: %v", err)
	}
}
