package session

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCleanExpired(t *testing.T) {
	home := t.TempDir()
	s := New(home)
	old := time.Now().Add(-48 * time.Hour).UTC().Format(time.RFC3339)
	fresh := time.Now().UTC().Format(time.RFC3339)
	for id, ts := range map[string]string{"old": old, "fresh": fresh} {
		dir := filepath.Join(s.Dir(), id)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		body := `{"session_id":"` + id + `","updated_at":"` + ts + `"}`
		if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.CleanExpired(time.Now(), 24*time.Hour, true)
	if err != nil || len(got) != 1 || got[0] != "old" {
		t.Fatalf("dry-run: %v %v", got, err)
	}
	got, err = s.CleanExpired(time.Now(), 24*time.Hour, false)
	if err != nil || len(got) != 1 {
		t.Fatalf("clean: %v %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(s.Dir(), "old")); !os.IsNotExist(err) {
		t.Fatal("old 应被删除")
	}
	if _, err := os.Stat(filepath.Join(s.Dir(), "fresh")); err != nil {
		t.Fatal("fresh 应保留")
	}
}
