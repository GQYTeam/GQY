package tool

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testPolicy(t *testing.T) Policy {
	t.Helper()
	ws := t.TempDir()
	return Policy{
		Workspace: ws,
		Roots:     []string{ws},
		MaxOutput: 4096,
	}
}

func writeFile(t *testing.T, path, content string, perm os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), perm); err != nil {
		t.Fatal(err)
	}
}

func TestRegistryDuplicateAndUnregister(t *testing.T) {
	r := NewRegistry()
	read := ReadTool{Policy: testPolicy(t)}
	if err := r.Register(read); err != nil {
		t.Fatalf("首次注册: %v", err)
	}
	if err := r.Register(read); err == nil {
		t.Fatal("重名注册应报错")
	}
	if _, ok := r.Get("read"); !ok {
		t.Fatal("注册后应能取到")
	}
	if err := r.Unregister("read"); err != nil {
		t.Fatalf("注销: %v", err)
	}
	if err := r.Unregister("read"); err == nil {
		t.Fatal("重复注销应返回未找到")
	}
}

func TestReadLimitsAndImage(t *testing.T) {
	policy := testPolicy(t)
	tool := ReadTool{Policy: policy}

	// 大文件超限
	big := filepath.Join(policy.Workspace, "big.txt")
	writeFile(t, big, strings.Repeat("a", MaxReadBytes+1), 0o644)
	if _, err := tool.Execute(context.Background(), map[string]any{"path": "big.txt"}); err == nil || !strings.Contains(err.Error(), "过大") {
		t.Fatalf("大文件应报超限: %v", err)
	}

	// 图片 data URI
	img := filepath.Join(policy.Workspace, "a.png")
	writeFile(t, img, "fakeimg", 0o644)
	out, err := tool.Execute(context.Background(), map[string]any{"path": "a.png"})
	if err != nil || !strings.HasPrefix(out, "data:image/png;base64,") {
		t.Fatalf("图片应转 data URI: %q, %v", out, err)
	}

	// 越界 ../ 拒绝
	if _, err := tool.Execute(context.Background(), map[string]any{"path": "../secret.txt"}); err == nil {
		t.Fatal("../ 越界应被拒")
	}
}

func TestReadSymlinkEscapeDenied(t *testing.T) {
	policy := testPolicy(t)
	outside := filepath.Join(t.TempDir(), "secret.txt")
	writeFile(t, outside, "secret", 0o644)
	link := filepath.Join(policy.Workspace, "link.txt")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	tool := ReadTool{Policy: policy}
	if _, err := tool.Execute(context.Background(), map[string]any{"path": "link.txt"}); err == nil {
		t.Fatal("符号链接逃逸应被拒")
	}
}

func TestReadDeniedPath(t *testing.T) {
	policy := testPolicy(t)
	denied := filepath.Join(policy.Workspace, "denied.txt")
	writeFile(t, denied, "x", 0o644)
	policy.Denied = []string{denied}
	tool := ReadTool{Policy: policy}
	if _, err := tool.Execute(context.Background(), map[string]any{"path": "denied.txt"}); err == nil {
		t.Fatal("拒绝路径应被拒")
	}
}

func TestEditAtomicPreservePermAndSymlink(t *testing.T) {
	policy := testPolicy(t)
	tool := EditTool{Policy: policy}

	// 自动建父目录 + 新文件 0644
	out, err := tool.Execute(context.Background(), map[string]any{"path": "sub/a.txt", "content": "hi"})
	if err != nil || !strings.Contains(out, "644") {
		t.Fatalf("写入失败: %q %v", out, err)
	}
	data, _ := os.ReadFile(filepath.Join(policy.Workspace, "sub", "a.txt"))
	if string(data) != "hi" {
		t.Fatalf("内容 = %q", data)
	}

	// 保留已有权限
	existing := filepath.Join(policy.Workspace, "perm.txt")
	writeFile(t, existing, "old", 0o600)
	if _, err := tool.Execute(context.Background(), map[string]any{"path": "perm.txt", "content": "new"}); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(existing)
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("权限未保留: %o", info.Mode().Perm())
	}

	// 符号链接目标拒绝
	outside := filepath.Join(t.TempDir(), "out.txt")
	writeFile(t, outside, "x", 0o644)
	link := filepath.Join(policy.Workspace, "lnk.txt")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	if _, err := tool.Execute(context.Background(), map[string]any{"path": "lnk.txt", "content": "y"}); err == nil {
		t.Fatal("符号链接写目标应被拒")
	}
}

func TestShellCwdTimeoutTruncateAllowlist(t *testing.T) {
	policy := testPolicy(t)
	policy.ShellTimeout = 1
	tool := ShellTool{Policy: policy}

	out, err := tool.Execute(context.Background(), map[string]any{"command": "pwd"})
	if err != nil || strings.TrimSpace(out) != policy.Workspace {
		t.Fatalf("cwd 应固定 workspace: %q %v", out, err)
	}

	// 输出截断
	if out, err := tool.Execute(context.Background(), map[string]any{"command": "yes abc | head -c 8192"}); err != nil || !strings.Contains(out, "截断") {
		t.Fatalf("输出应带截断标记: %v", err)
	}

	// 超时杀进程组（1s 超时，sleep 30 应被超时终止）
	if _, err := tool.Execute(context.Background(), map[string]any{"command": "sleep 30"}); err == nil || !strings.Contains(err.Error(), "超时") {
		t.Fatalf("应超时: %v", err)
	}

	// 白名单：未列入命令拒绝、元字符拒绝
	policy.ShellAllowlist = []string{"echo"}
	allowTool := ShellTool{Policy: policy}
	if _, err := allowTool.Execute(context.Background(), map[string]any{"command": "ls"}); err == nil {
		t.Fatal("未列入白名单应拒绝")
	}
	if _, err := allowTool.Execute(context.Background(), map[string]any{"command": "echo hi; rm -rf /"}); err == nil {
		t.Fatal("白名单模式应拒绝元字符")
	}
	if out, err := allowTool.Execute(context.Background(), map[string]any{"command": "echo ok"}); err != nil || strings.TrimSpace(out) != "ok" {
		t.Fatalf("白名单命令应放行: %q %v", out, err)
	}
}

func TestListDirAndWebSearch(t *testing.T) {
	policy := testPolicy(t)
	os.MkdirAll(filepath.Join(policy.Workspace, "sub"), 0o755)
	writeFile(t, filepath.Join(policy.Workspace, "a.txt"), "x", 0o644)

	list := ListDirTool{Policy: policy}
	out, err := list.Execute(context.Background(), map[string]any{"path": "."})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "sub/") || !strings.Contains(out, "a.txt") {
		t.Fatalf("目录项应带 / 结尾: %q", out)
	}
	if _, err := list.Execute(context.Background(), map[string]any{"path": "../.."}); err == nil {
		t.Fatal("越界目录应被拒")
	}

	search := WebSearchTool{Policy: policy}
	if _, err := search.Execute(context.Background(), map[string]any{"query": "x"}); err == nil || !strings.Contains(err.Error(), "未配置") {
		t.Fatalf("未配置应明确报错: %v", err)
	}
}

func TestAskUserOptions(t *testing.T) {
	tool := AskUserTool{}
	_, err := tool.Execute(context.Background(), map[string]any{"question": "继续吗", "options": []any{"继续", "取消"}})
	var askErr *AskError
	if err == nil || !strings.Contains(err.Error(), "ask_user") {
		t.Fatalf("ask_user 应返回 AskError: %v", err)
	}
	_ = askErr
	if _, err := tool.Execute(context.Background(), map[string]any{"options": []any{"a"}}); err == nil {
		t.Fatal("options 数量非法应报错")
	}
}
