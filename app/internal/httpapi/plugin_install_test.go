package httpapi

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCopyPluginTreeSkipsSymlinks(t *testing.T) {
	src := t.TempDir()
	dst := filepath.Join(t.TempDir(), "echo")

	if err := os.MkdirAll(filepath.Join(src, "sub"), 0o700); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"manifest.json":                    `{"id":"echo"}`,
		"index.js":                         "console.log('echo')\n",
		filepath.Join("sub", "nested.txt"): "nested",
	}
	for name, body := range files {
		path := filepath.Join(src, name)
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// 符号链接必须被跳过，不能把目标内容带进安装目录。
	if err := os.Symlink("/etc/passwd", filepath.Join(src, "escape")); err != nil {
		t.Skipf("无法创建符号链接: %v", err)
	}

	if err := copyPluginTree(src, dst); err != nil {
		t.Fatalf("copyPluginTree: %v", err)
	}
	for name, want := range files {
		got, err := os.ReadFile(filepath.Join(dst, name))
		if err != nil {
			t.Fatalf("读取 %s: %v", name, err)
		}
		if string(got) != want {
			t.Fatalf("%s 内容 = %q, want %q", name, got, want)
		}
		info, err := os.Stat(filepath.Join(dst, name))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("%s 权限 = %04o, want 0600", name, info.Mode().Perm())
		}
	}
	if _, err := os.Lstat(filepath.Join(dst, "escape")); !os.IsNotExist(err) {
		t.Fatalf("符号链接不应被复制，lstat err=%v", err)
	}
}
