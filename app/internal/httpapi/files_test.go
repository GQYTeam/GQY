package httpapi

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
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

func TestSaveUploadConcurrentSameName(t *testing.T) {
	home := t.TempDir()
	s := &Server{Cfg: config.Config{Home: home}}
	const n = 16

	rels := make([]string, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			rels[i], _, errs[i] = s.saveUpload("s1", "a.txt", []byte(fmt.Sprintf("content-%d", i)))
		}(i)
	}
	wg.Wait()

	seen := map[string]bool{}
	for i, err := range errs {
		if err != nil {
			t.Fatalf("upload %d: %v", i, err)
		}
		if rels[i] == "" || seen[rels[i]] {
			t.Fatalf("上传路径为空或重复: %q", rels[i])
		}
		seen[rels[i]] = true
		data, err := os.ReadFile(filepath.Join(home, filepath.FromSlash(rels[i])))
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != fmt.Sprintf("content-%d", i) {
			t.Fatalf("%s 内容被覆盖: %q", rels[i], data)
		}
	}
}
