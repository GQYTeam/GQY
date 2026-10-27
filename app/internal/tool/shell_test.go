package tool

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestShellTimeoutKillsProcessGroup(t *testing.T) {
	ws := t.TempDir()
	p := testPolicy(t, ws)
	p.ShellTimeout = 300 * time.Millisecond
	p.MaxOutput = 4096

	sh := Shell{Policy: p}
	start := time.Now()
	out, err := sh.Execute(context.Background(), map[string]any{
		"command": `sleep 30 & child=$!; echo "child=$child"; wait $child`,
	})
	elapsed := time.Since(start)

	if err == nil || !strings.Contains(err.Error(), "超时") {
		t.Fatalf("应返回超时错误，实际: %v (out=%q)", err, out)
	}
	if elapsed > 5*time.Second {
		t.Fatalf("超时返回过慢: %s", elapsed)
	}

	m := regexp.MustCompile(`child=(\d+)`).FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("未拿到子进程 pid，输出: %q", out)
	}
	pid, err := strconv.Atoi(m[1])
	if err != nil {
		t.Fatalf("pid 解析: %v", err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for {
		kerr := syscall.Kill(pid, 0)
		if errors.Is(kerr, syscall.ESRCH) {
			return // 子进程已消失，没有留下孤儿
		}
		if time.Now().After(deadline) {
			t.Fatalf("子进程 %d 在超时后仍存活（kill err=%v）", pid, kerr)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestShellOutputTruncation(t *testing.T) {
	ws := t.TempDir()
	p := testPolicy(t, ws)
	p.MaxOutput = 64
	p.ShellTimeout = 5 * time.Second

	sh := Shell{Policy: p}
	out, err := sh.Execute(context.Background(), map[string]any{
		"command": "yes abcdefgh | head -c 8192",
	})
	if err != nil {
		t.Fatalf("命令执行失败: %v", err)
	}
	if !strings.Contains(out, "截断") {
		t.Fatalf("输出应带截断标记，实际: %q", out)
	}
	if len(out) > p.MaxOutput+32 {
		t.Fatalf("输出未截断: %d 字节", len(out))
	}
}

func TestShellAllowlistRejectsAndAllows(t *testing.T) {
	ws := t.TempDir()
	p := testPolicy(t, ws)
	p.ShellAllowlist = []string{"ls"}
	sh := Shell{Policy: p}

	_, err := sh.Execute(context.Background(), map[string]any{
		"command": `echo hi > marker.txt`,
	})
	if err == nil || !strings.Contains(err.Error(), "允许列表") {
		t.Fatalf("应拒绝未列入白名单的命令，实际: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(ws, "marker.txt")); !os.IsNotExist(statErr) {
		t.Fatalf("被拒绝的命令不应执行，stat err=%v", statErr)
	}

	p.ShellAllowlist = []string{"echo"}
	out, err := (Shell{Policy: p}).Execute(context.Background(), map[string]any{"command": "echo hi"})
	if err != nil {
		t.Fatalf("白名单命令应放行: %v", err)
	}
	if strings.TrimSpace(out) != "hi" {
		t.Fatalf("输出 = %q", out)
	}

	// 白名单按 basename 匹配，允许写绝对路径。
	p.ShellAllowlist = []string{"/bin/echo"}
	out, err = (Shell{Policy: p}).Execute(context.Background(), map[string]any{"command": "echo ok"})
	if err != nil || strings.TrimSpace(out) != "ok" {
		t.Fatalf("basename 匹配失败: out=%q err=%v", out, err)
	}
}

func TestShellUsesWorkspaceAsCwd(t *testing.T) {
	ws := t.TempDir()
	p := testPolicy(t, ws)
	sh := Shell{Policy: p}

	out, err := sh.Execute(context.Background(), map[string]any{"command": "pwd"})
	if err != nil {
		t.Fatalf("pwd: %v", err)
	}
	if got, want := canon(t, strings.TrimSpace(out)), canon(t, ws); got != want {
		t.Fatalf("cwd = %q, want %q", got, want)
	}

	if _, err := sh.Execute(context.Background(), map[string]any{"command": "touch made.txt"}); err != nil {
		t.Fatalf("touch: %v", err)
	}
	if _, err := os.Stat(filepath.Join(ws, "made.txt")); err != nil {
		t.Fatalf("相对路径应落在 workspace: %v", err)
	}
}

func TestShellDescriptionMentionsRestrictions(t *testing.T) {
	open := Shell{Policy: Policy{}}
	if !strings.Contains(open.Description(), "未设置命令白名单") {
		t.Fatalf("空白名单应在描述里提示：%s", open.Description())
	}
	closed := Shell{Policy: Policy{ShellAllowlist: []string{"git", "go"}}}
	if !strings.Contains(closed.Description(), "git, go") || !strings.Contains(closed.Description(), "只允许") {
		t.Fatalf("白名单应在描述里列出：%s", closed.Description())
	}
}

func TestFirstToken(t *testing.T) {
	cases := map[string]string{
		"echo hi":        "echo",
		`"echo" hi`:      "echo",
		`'/bin/ls' -la`:  "/bin/ls",
		"  git   status": "git",
	}
	for in, want := range cases {
		if got := firstToken(in); got != want {
			t.Fatalf("firstToken(%q) = %q, want %q", in, got, want)
		}
	}
}
