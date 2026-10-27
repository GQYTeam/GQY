package httpapi

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gqy/internal/config"
)

func TestSafeUploadName(t *testing.T) {
	cases := map[string]string{
		"../../etc/passwd": "passwd",
		"a<b>c.txt":        "a_b_c.txt",
		"..hidden":         "hidden",
		"你好.md":            "__.md",
		"":                 "upload",
	}
	for in, want := range cases {
		if got := safeUploadName(in); got != want {
			t.Errorf("safeUploadName(%q)=%q want %q", in, got, want)
		}
	}
}

func TestExtractUploadText(t *testing.T) {
	if got, trunc := extractUploadText("a.md", []byte("# hi")); got != "# hi" || trunc {
		t.Fatalf("text: %q %v", got, trunc)
	}
	big := strings.Repeat("x", maxTextRunes+10)
	got, trunc := extractUploadText("a.txt", []byte(big))
	if !trunc || len([]rune(got)) != maxTextRunes {
		t.Fatalf("truncated=%v len=%d", trunc, len([]rune(got)))
	}
	if got, _ := extractUploadText("a.png", []byte{0x89, 0x50}); got != "" {
		t.Fatalf("binary should not extract: %q", got)
	}
	if got, _ := extractUploadText("a.txt", []byte{0xff, 0xfe}); got != "" {
		t.Fatalf("invalid utf8 should not extract: %q", got)
	}
}

func TestSaveUploadUniqueAndPrivate(t *testing.T) {
	home := t.TempDir()
	s := &Server{Cfg: config.Config{Home: home}}
	rel, size, err := s.saveUpload("s1", "a.txt", []byte("one"))
	if err != nil {
		t.Fatal(err)
	}
	if size != 3 || !strings.HasSuffix(rel, "uploads/a.txt") {
		t.Fatalf("rel=%q size=%d", rel, size)
	}
	rel2, _, err := s.saveUpload("s1", "a.txt", []byte("two"))
	if err != nil {
		t.Fatal(err)
	}
	if rel2 == rel || !strings.HasSuffix(rel2, "a-1.txt") {
		t.Fatalf("duplicate not renamed: %q", rel2)
	}
	info, err := os.Stat(filepath.Join(home, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("perm=%v", info.Mode().Perm())
	}
}
