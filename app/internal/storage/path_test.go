package storage

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnsureInside(t *testing.T) {
	root := t.TempDir()
	inside := filepath.Join(root, "sessions", "abc")
	outside := t.TempDir()

	cases := []struct {
		name    string
		target  string
		wantErr bool
	}{
		{"根目录", root, false},
		{"不存在的子路径", inside, false},
		{"已存在的子路径", filepath.Join(root, "gateway", "requests.jsonl"), false},
		{"父目录穿越", filepath.Join(root, "..", "nope"), true},
		{"绝对路径越界", outside, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := EnsureInside(root, tc.target)
			if tc.wantErr && err == nil {
				t.Fatalf("%s 应被拒绝", tc.target)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("%s 应放行: %v", tc.target, err)
			}
		})
	}
}

func TestEnsureInsideRejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	link := filepath.Join(root, "escape")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("当前平台不支持符号链接: %v", err)
	}
	if err := EnsureInside(root, filepath.Join(link, "file")); err == nil {
		t.Fatal("符号链接逃逸应被拒绝")
	}
}
