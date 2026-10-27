// 核心工具：read（支持图片）、edit、shell、websearch、list_dir。
//
// 核心工具由 agent 内置，直接暴露给模型；其余能力走外部工具（插件/skill/mcp）。
// 文件类工具通过 Policy 做路径沙箱，shell 通过 Policy 做工作目录、白名单、超时与输出上限。
package tool

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

/* ---------------- read（读取文件，支持图片） ---------------- */

type Read struct{ Policy Policy }

func (Read) Name() string { return "read" }
func (Read) Kind() Kind   { return KindCore }
func (Read) Description() string {
	return fmt.Sprintf("读取一个文件的内容（只能访问允许根目录）。文本文件返回全文（上限 %d MB）；图片（png/jpg/jpeg/gif/webp）返回 base64 数据供视觉理解。", MaxReadBytes>>20)
}
func (Read) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{"type": "string", "description": "绝对或相对路径（相对 workspace）"},
		},
		"required": []any{"path"},
	}
}
func (r Read) Execute(ctx context.Context, args map[string]any) (string, error) {
	path, _ := args["path"].(string)
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("path 不能为空")
	}
	target, err := r.Policy.Resolve(path)
	if err != nil {
		return "", err
	}
	b, err := readLimited(target, MaxReadBytes)
	if err != nil {
		return "", err
	}
	// 图片：转 base64 供视觉模型
	if mime, ok := imageMIME(target); ok {
		return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(b), nil
	}
	return string(b), nil
}

func imageMIME(path string) (string, bool) {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png":
		return "image/png", true
	case ".jpg", ".jpeg":
		return "image/jpeg", true
	case ".gif":
		return "image/gif", true
	case ".webp":
		return "image/webp", true
	}
	return "", false
}

/* ---------------- edit（写入/覆盖文件） ---------------- */

type Edit struct{ Policy Policy }

func (Edit) Name() string { return "edit" }
func (Edit) Kind() Kind   { return KindCore }
func (Edit) Description() string {
	return "写入（覆盖）一个文件（只能访问允许根目录）。会自动创建父目录；已有文件保留原权限，写入用同目录临时文件 + rename 原子完成。"
}
func (Edit) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path":    map[string]any{"type": "string", "description": "要写入的文件路径（相对 workspace 或绝对路径）"},
			"content": map[string]any{"type": "string", "description": "完整文件内容"},
		},
		"required": []any{"path", "content"},
	}
}
func (e Edit) Execute(ctx context.Context, args map[string]any) (string, error) {
	path, _ := args["path"].(string)
	content, _ := args["content"].(string)
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("path 不能为空")
	}
	target, err := e.Policy.Resolve(path)
	if err != nil {
		return "", err
	}
	mode, err := writeFileAtomic(target, []byte(content))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("已写入 %s（%d 字节，权限 %04o）", path, len(content), mode.Perm()), nil
}

/* ---------------- shell（跑 shell 命令） ---------------- */

type Shell struct{ Policy Policy }

func (Shell) Name() string { return "shell" }
func (Shell) Kind() Kind   { return KindCore }
func (s Shell) Description() string {
	var b strings.Builder
	fmt.Fprintf(&b, "执行一条 shell 命令，工作目录固定为 workspace；返回合并后的 stdout+stderr（最多 %dKB）。", s.Policy.maxOutput()/1024)
	fmt.Fprintf(&b, "命令超过 %s 会被终止（连同子进程）。", s.Policy.shellTimeout())
	if len(s.Policy.ShellAllowlist) == 0 {
		b.WriteString("当前未设置命令白名单（GQY_SHELL_ALLOWLIST 为空），可执行任意命令。")
	} else {
		b.WriteString("当前只允许这些命令: " + strings.Join(s.Policy.ShellAllowlist, ", ") + "。")
	}
	return b.String()
}
func (Shell) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"command": map[string]any{"type": "string", "description": "要执行的 shell 命令"},
		},
		"required": []any{"command"},
	}
}
func (s Shell) Execute(ctx context.Context, args map[string]any) (string, error) {
	command, _ := args["command"].(string)
	if strings.TrimSpace(command) == "" {
		return "", fmt.Errorf("command 不能为空")
	}
	if len(s.Policy.ShellAllowlist) > 0 {
		first := firstToken(command)
		base := filepath.Base(first)
		if !allowedCommand(base, s.Policy.ShellAllowlist) {
			return "", fmt.Errorf("命令 %q 不在允许列表中（GQY_SHELL_ALLOWLIST=%s）；请改用允许的命令，或由运维调整环境变量", base, strings.Join(s.Policy.ShellAllowlist, ","))
		}
	}

	timeout := s.Policy.shellTimeout()
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(runCtx, "sh", "-c", command)
	cmd.Dir = s.Policy.workspaceAbs()
	// 独立进程组：超时或取消时杀整组，避免子进程变孤儿。
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
			return err
		}
		return nil
	}
	// 后台子进程可能继承管道；超时后最多再等这么久，保证 Wait 能返回。
	cmd.WaitDelay = 2 * time.Second

	buf := &limitedBuffer{limit: s.Policy.maxOutput()}
	cmd.Stdout = buf
	cmd.Stderr = buf

	err := cmd.Run()
	out := buf.String()

	if ctx.Err() != nil {
		return out, ctx.Err()
	}
	if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
		return out, fmt.Errorf("命令超时（%s），已终止进程组", timeout)
	}
	if err != nil {
		if strings.TrimSpace(out) == "" {
			return out, fmt.Errorf("exit %v", err)
		}
		return out, fmt.Errorf("exit %v: %s", err, out)
	}
	return out, nil
}

// firstToken 取命令的第一个 token（简单处理引号），用于白名单匹配。
func firstToken(command string) string {
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return ""
	}
	first := fields[0]
	if len(first) >= 2 {
		if (first[0] == '"' && first[len(first)-1] == '"') || (first[0] == '\'' && first[len(first)-1] == '\'') {
			first = first[1 : len(first)-1]
		}
	}
	return first
}

func allowedCommand(base string, allowlist []string) bool {
	for _, allowed := range allowlist {
		if filepath.Base(strings.TrimSpace(allowed)) == base {
			return true
		}
	}
	return false
}

/* ---------------- list_dir（列目录） ---------------- */

type ListDir struct{ Policy Policy }

func (ListDir) Name() string { return "list_dir" }
func (ListDir) Kind() Kind   { return KindCore }
func (ListDir) Description() string {
	return "列出目录下的文件和子目录名（目录以 / 结尾，只能访问允许根目录），每项一行。"
}
func (ListDir) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{"type": "string", "description": "目录路径（相对 workspace），默认当前目录"},
		},
	}
}
func (l ListDir) Execute(ctx context.Context, args map[string]any) (string, error) {
	path, _ := args["path"].(string)
	if strings.TrimSpace(path) == "" {
		path = "."
	}
	target, err := l.Policy.Resolve(path)
	if err != nil {
		return "", err
	}
	entries, err := os.ReadDir(target)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() {
			name += "/"
		}
		b.WriteString(name)
		b.WriteByte('\n')
	}
	return b.String(), nil
}

/* ---------------- websearch（网络搜索） ---------------- */

type WebSearch struct{}

func (WebSearch) Name() string { return "websearch" }
func (WebSearch) Kind() Kind   { return KindCore }
func (WebSearch) Description() string {
	return "在网络上搜索信息。需要服务端配置搜索 API（未配置时返回提示）。"
}
func (WebSearch) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{"type": "string", "description": "搜索关键词（英文效果更好）"},
		},
		"required": []any{"query"},
	}
}
func (WebSearch) Execute(ctx context.Context, args map[string]any) (string, error) {
	// 未接搜索后端：明确返回，让模型不要编造
	return "", fmt.Errorf("websearch 尚未接入搜索后端，请用 shell 调用本地工具，或等待后续接入")
}
