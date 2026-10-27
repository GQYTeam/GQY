package tool

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testPolicy(t *testing.T, ws string) Policy {
	t.Helper()
	return Policy{
		Workspace:    ws,
		Roots:        []string{ws},
		ShellTimeout: 2 * time.Second,
		MaxOutput:    4096,
	}
}

func canon(t *testing.T, path string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("EvalSymlinks(%s): %v", path, err)
	}
	return resolved
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", path, err)
	}
	return string(b)
}

func TestResolveAndReadInsideRoot(t *testing.T) {
	ws := t.TempDir()
	p := testPolicy(t, ws)
	writeFile(t, filepath.Join(ws, "a.txt"), "hello")
	writeFile(t, filepath.Join(ws, "sub", "b.txt"), "nested")

	r := Read{Policy: p}
	cases := []struct {
		name string
		path string
		want string
	}{
		{"relative", "a.txt", "hello"},
		{"dotted relative", "./sub/../a.txt", "hello"},
		{"nested relative", "sub/b.txt", "nested"},
		{"absolute inside", filepath.Join(ws, "sub", "b.txt"), "nested"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := r.Execute(context.Background(), map[string]any{"path": tc.path})
			if err != nil {
				t.Fatalf("read %q: %v", tc.path, err)
			}
			if out != tc.want {
				t.Fatalf("read %q = %q, want %q", tc.path, out, tc.want)
			}
		})
	}
}

func TestPathSandboxRejectsTraversalAndOutside(t *testing.T) {
	ws := t.TempDir()
	outside := t.TempDir()
	writeFile(t, filepath.Join(outside, "secret.txt"), "secret")
	p := testPolicy(t, ws)
	root := canon(t, ws)

	paths := []string{
		"../secret.txt",
		filepath.Join(ws, "..", filepath.Base(outside), "secret.txt"),
		filepath.Join(outside, "secret.txt"),
		"/etc/hosts",
	}
	tools := map[string]Tool{
		"read":     Read{Policy: p},
		"edit":     Edit{Policy: p},
		"list_dir": ListDir{Policy: p},
	}
	for name, tl := range tools {
		for _, path := range paths {
			args := map[string]any{"path": path}
			if name == "edit" {
				args["content"] = "pwned"
			}
			_, err := tl.Execute(context.Background(), args)
			if err == nil {
				t.Fatalf("%s 应拒绝越界路径 %q", name, path)
			}
			if !strings.Contains(err.Error(), "允许根") || !strings.Contains(err.Error(), root) {
				t.Fatalf("%s 错误信息应说明允许根，实际: %v", name, err)
			}
		}
	}

	if got := readFile(t, filepath.Join(outside, "secret.txt")); got != "secret" {
		t.Fatalf("越界写入不应修改外部文件，实际内容 %q", got)
	}
}

func TestSymlinkEscapeRejected(t *testing.T) {
	ws := t.TempDir()
	outside := t.TempDir()
	writeFile(t, filepath.Join(outside, "f.txt"), "outside")

	if err := os.Symlink(outside, filepath.Join(ws, "linkdir")); err != nil {
		t.Fatalf("symlink dir: %v", err)
	}
	if err := os.Symlink(filepath.Join(outside, "f.txt"), filepath.Join(ws, "filelink")); err != nil {
		t.Fatalf("symlink file: %v", err)
	}

	p := testPolicy(t, ws)

	if _, err := (Read{Policy: p}).Execute(context.Background(), map[string]any{"path": "linkdir/f.txt"}); err == nil {
		t.Fatal("read 应拒绝通过目录符号链接逃逸")
	}
	if _, err := (ListDir{Policy: p}).Execute(context.Background(), map[string]any{"path": "linkdir"}); err == nil {
		t.Fatal("list_dir 应拒绝通过目录符号链接逃逸")
	}
	if _, err := (Edit{Policy: p}).Execute(context.Background(), map[string]any{"path": "linkdir/new/deep.txt", "content": "x"}); err == nil {
		t.Fatal("edit 应拒绝通过目录符号链接逃逸")
	}
	if _, err := (Read{Policy: p}).Execute(context.Background(), map[string]any{"path": "filelink"}); err == nil {
		t.Fatal("read 应拒绝通过文件符号链接逃逸")
	}
	if _, err := (Edit{Policy: p}).Execute(context.Background(), map[string]any{"path": "filelink", "content": "pwned"}); err == nil {
		t.Fatal("edit 应拒绝通过文件符号链接写到根外")
	}

	if _, err := os.Stat(filepath.Join(outside, "new", "deep.txt")); !os.IsNotExist(err) {
		t.Fatalf("根外不应产生新文件，stat err=%v", err)
	}
	if got := readFile(t, filepath.Join(outside, "f.txt")); got != "outside" {
		t.Fatalf("根外文件被改写: %q", got)
	}
}

func TestSymlinkInsideRootAllowed(t *testing.T) {
	ws := t.TempDir()
	writeFile(t, filepath.Join(ws, "real", "f.txt"), "inside")
	if err := os.Symlink(filepath.Join(ws, "real"), filepath.Join(ws, "alias")); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	p := testPolicy(t, ws)

	out, err := (Read{Policy: p}).Execute(context.Background(), map[string]any{"path": "alias/f.txt"})
	if err != nil || out != "inside" {
		t.Fatalf("根内符号链接应可用，out=%q err=%v", out, err)
	}
	if _, err := (Edit{Policy: p}).Execute(context.Background(), map[string]any{"path": "alias/new.txt", "content": "written"}); err != nil {
		t.Fatalf("通过根内目录符号链接写入: %v", err)
	}
	if got := readFile(t, filepath.Join(ws, "real", "new.txt")); got != "written" {
		t.Fatalf("写入落点错误: %q", got)
	}
}

func TestEditCreatesParentsAndPreservesMode(t *testing.T) {
	ws := t.TempDir()
	p := testPolicy(t, ws)
	e := Edit{Policy: p}

	if _, err := e.Execute(context.Background(), map[string]any{"path": "a/b/c.txt", "content": "one"}); err != nil {
		t.Fatalf("创建嵌套文件: %v", err)
	}
	target := filepath.Join(ws, "a", "b", "c.txt")
	if got := readFile(t, target); got != "one" {
		t.Fatalf("内容 = %q", got)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o644 {
		t.Fatalf("新文件权限 = %04o, want 0644", info.Mode().Perm())
	}

	if err := os.Chmod(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Execute(context.Background(), map[string]any{"path": "a/b/c.txt", "content": "two"}); err != nil {
		t.Fatalf("覆盖写入: %v", err)
	}
	info, err = os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("覆盖后权限 = %04o, want 0755", info.Mode().Perm())
	}
	if got := readFile(t, target); got != "two" {
		t.Fatalf("覆盖后内容 = %q", got)
	}

	// 原子写不应留下临时文件。
	entries, err := os.ReadDir(filepath.Dir(target))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".gqy-edit-") {
			t.Fatalf("残留临时文件 %s", entry.Name())
		}
	}
}

func TestEditOverwritesExistingSymlinkInsideRoot(t *testing.T) {
	ws := t.TempDir()
	writeFile(t, filepath.Join(ws, "target.txt"), "old")
	if err := os.Symlink(filepath.Join(ws, "target.txt"), filepath.Join(ws, "link.txt")); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	p := testPolicy(t, ws)
	if _, err := (Edit{Policy: p}).Execute(context.Background(), map[string]any{"path": "link.txt", "content": "new"}); err != nil {
		t.Fatalf("edit: %v", err)
	}
	if got := readFile(t, filepath.Join(ws, "target.txt")); got != "new" {
		t.Fatalf("目标文件内容 = %q", got)
	}
}

func TestReadRejectsOversizeAndSupportsImages(t *testing.T) {
	ws := t.TempDir()
	p := testPolicy(t, ws)

	big := filepath.Join(ws, "big.txt")
	f, err := os.Create(big)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(MaxReadBytes + 1); err != nil {
		f.Close()
		t.Fatal(err)
	}
	f.Close()

	r := Read{Policy: p}
	if _, err := r.Execute(context.Background(), map[string]any{"path": "big.txt"}); err == nil || !strings.Contains(err.Error(), "过大") {
		t.Fatalf("超大文件应报错，实际: %v", err)
	}

	writeFile(t, filepath.Join(ws, "pic.png"), "not-a-real-png")
	out, err := r.Execute(context.Background(), map[string]any{"path": "pic.png"})
	if err != nil {
		t.Fatalf("读取图片: %v", err)
	}
	if !strings.HasPrefix(out, "data:image/png;base64,") {
		t.Fatalf("图片应返回 data URI，实际前缀: %.32s", out)
	}

	if _, err := r.Execute(context.Background(), map[string]any{"path": "."}); err == nil || !strings.Contains(err.Error(), "目录") {
		t.Fatalf("read 目录应报错，实际: %v", err)
	}
}

func TestListDirInsideRoot(t *testing.T) {
	ws := t.TempDir()
	writeFile(t, filepath.Join(ws, "f.txt"), "x")
	if err := os.Mkdir(filepath.Join(ws, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	p := testPolicy(t, ws)

	out, err := (ListDir{Policy: p}).Execute(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("列出默认目录: %v", err)
	}
	if !strings.Contains(out, "f.txt\n") || !strings.Contains(out, "sub/\n") {
		t.Fatalf("目录输出不符合预期: %q", out)
	}
}

func TestResolveEmptyPath(t *testing.T) {
	p := testPolicy(t, t.TempDir())
	if _, err := p.Resolve("  "); err == nil {
		t.Fatal("空路径应报错")
	}
}

func TestResolveDeniedPath(t *testing.T) {
	ws := t.TempDir()
	writeFile(t, filepath.Join(ws, "github_token.json"), `{"access_token":"secret"}`)
	writeFile(t, filepath.Join(ws, "notes.txt"), "ok")
	p := testPolicy(t, ws)
	p.Denied = []string{filepath.Join(ws, "github_token.json")}

	if _, err := p.Resolve("github_token.json"); err == nil || !strings.Contains(err.Error(), "受保护路径") {
		t.Fatalf("token 文件应被拒绝，实际: %v", err)
	}
	// 同目录其它文件仍可访问。
	out, err := (Read{Policy: p}).Execute(context.Background(), map[string]any{"path": "notes.txt"})
	if err != nil || out != "ok" {
		t.Fatalf("普通文件应可读: out=%q err=%v", out, err)
	}
	// 拒绝目录时，目录内所有路径都不可访问。
	p.Denied = []string{ws}
	if _, err := p.Resolve("notes.txt"); err == nil {
		t.Fatal("拒绝目录后其子路径应被拒绝")
	}
}
