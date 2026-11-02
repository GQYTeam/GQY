package tool

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

// ---- read ----

// ReadTool 读取文件内容；图片转 data URI；只读允许根内路径（T4、T5）。
type ReadTool struct {
	Policy Policy
}

func (t ReadTool) Name() string        { return "read" }
func (t ReadTool) Description() string { return "读取文件内容；图片会转成 data URI" }
func (t ReadTool) Source() Source      { return SourceCore }
func (t ReadTool) Schema() map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{"path": map[string]any{"type": "string"}},
		"required":   []string{"path"},
	}
}

func (t ReadTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	path, ok := args["path"].(string)
	if !ok || path == "" {
		return "", fmt.Errorf("%w: path 必填", ErrInvalidArgument)
	}
	resolved, err := t.Policy.Resolve(path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("tool: 读文件失败: %w", err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("tool: %s 是目录，不是文件", resolved)
	}
	if isImageExt(resolved) {
		data, err := readLimited(resolved, MaxReadBytes)
		if err != nil {
			return "", err
		}
		return "data:" + mimeByExt(resolved) + ";base64," + base64.StdEncoding.EncodeToString(data), nil
	}
	if info.Size() > MaxReadBytes {
		return "", fmt.Errorf("tool: 文件过大（%d 字节，上限 %d 字节）", info.Size(), MaxReadBytes)
	}
	data, err := readLimited(resolved, MaxReadBytes)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func readLimited(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path) // #nosec G304 -- 路径已由 Policy.Resolve 校验
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("tool: 文件超过读取上限 %d 字节", limit)
	}
	return data, nil
}

func isImageExt(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp":
		return true
	}
	return false
}

func mimeByExt(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	}
	return "application/octet-stream"
}

// ---- edit ----

// EditTool 覆盖写文件：原子写、保留权限、自动建父目录、拒绝符号链接目标（T4、T5）。
type EditTool struct {
	Policy Policy
}

func (t EditTool) Name() string        { return "edit" }
func (t EditTool) Description() string { return "覆盖写文件，自动建父目录" }
func (t EditTool) Source() Source      { return SourceCore }
func (t EditTool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path":    map[string]any{"type": "string"},
			"content": map[string]any{"type": "string"},
		},
		"required": []string{"path", "content"},
	}
}

func (t EditTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	path, _ := args["path"].(string)
	content, ok := args["content"].(string)
	if path == "" || !ok {
		return "", fmt.Errorf("%w: path 与 content 必填", ErrInvalidArgument)
	}
	resolved, err := t.Policy.Resolve(path)
	if err != nil {
		return "", err
	}
	if err := RejectSymlink(resolved); err != nil {
		return "", err
	}
	dir := filepath.Dir(resolved)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", err
	}
	mode := os.FileMode(0o644)
	if info, err := os.Lstat(resolved); err == nil {
		mode = info.Mode().Perm()
	}
	tmp, err := os.CreateTemp(dir, ".gqy-edit-*")
	if err != nil {
		return "", err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if _, err := tmp.WriteString(content); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(tmpName, resolved); err != nil {
		return "", err
	}
	return fmt.Sprintf("已写入 %s（%d 字节，权限 %o）", filepath.Base(resolved), len(content), mode.Perm()), nil
}

// ---- shell ----

// ShellTool 执行一条 shell 命令：固定 workspace、超时杀进程组、输出截断、
// 白名单 + 元字符拒绝（T4、T6、SEC4）。
type ShellTool struct {
	Policy Policy
}

func (t ShellTool) Name() string        { return "shell" }
func (t ShellTool) Description() string { return "在 workspace 下执行一条 shell 命令" }
func (t ShellTool) Source() Source      { return SourceCore }
func (t ShellTool) Schema() map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{"command": map[string]any{"type": "string"}},
		"required":   []string{"command"},
	}
}

func (t ShellTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	command, ok := args["command"].(string)
	if !ok || strings.TrimSpace(command) == "" {
		return "", fmt.Errorf("%w: command 必填", ErrInvalidArgument)
	}
	if err := t.checkAllowlist(command); err != nil {
		return "", err
	}

	timeout := time.Duration(t.Policy.shellTimeoutSec()) * time.Second
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", command) // #nosec G204 -- shell 工具按契约执行用户命令
	cmd.Dir = t.Policy.workspaceAbs()
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	var buf limitedBuffer
	buf.limit = t.Policy.maxOutput()
	cmd.Stdout = &buf
	cmd.Stderr = &buf

	err := cmd.Start()
	if err != nil {
		return "", fmt.Errorf("tool: 启动命令失败: %w", err)
	}
	waitErr := cmd.Wait()
	if ctx.Err() != nil {
		killGroup(cmd)
		_ = cmd.Wait()
		return buf.String(), fmt.Errorf("%w: 命令超时", ErrTimeout)
	}
	if waitErr != nil {
		return buf.String(), fmt.Errorf("tool: 命令退出: %w", waitErr)
	}
	return buf.String(), nil
}

// checkAllowlist：白名单非空时只允许列表内命令（首 token basename），并拒绝元字符。
func (t ShellTool) checkAllowlist(command string) error {
	if len(t.Policy.ShellAllowlist) == 0 {
		return nil
	}
	if strings.ContainsAny(command, ";|&><`$()\n") {
		return fmt.Errorf("%w: 白名单模式拒绝元字符", ErrInvalidArgument)
	}
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return fmt.Errorf("%w: command 必填", ErrInvalidArgument)
	}
	base := filepath.Base(fields[0])
	for _, allowed := range t.Policy.ShellAllowlist {
		if base == filepath.Base(allowed) {
			return nil
		}
	}
	return fmt.Errorf("%w: 命令 %s 不在白名单", ErrInvalidArgument, fields[0])
}

func killGroup(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}

// ErrTimeout 表示执行超时。
var ErrTimeout = fmt.Errorf("tool: 执行超时")

// limitedBuffer 是有上限的合并输出缓冲区。
type limitedBuffer struct {
	limit     int
	buf       []byte
	truncated bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if len(b.buf) >= b.limit {
		b.truncated = true
		return len(p), nil
	}
	remaining := b.limit - len(b.buf)
	if len(p) > remaining {
		b.buf = append(b.buf, p[:remaining]...)
		b.truncated = true
		return len(p), nil
	}
	b.buf = append(b.buf, p...)
	return len(p), nil
}

func (b *limitedBuffer) String() string {
	s := string(b.buf)
	if b.truncated {
		s += "\n...[截断]"
	}
	return s
}

// ---- list_dir ----

// ListDirTool 列目录项；目录以 / 结尾；只读允许根（T4）。
type ListDirTool struct {
	Policy Policy
}

func (t ListDirTool) Name() string        { return "list_dir" }
func (t ListDirTool) Description() string { return "列出目录项，每行一项" }
func (t ListDirTool) Source() Source      { return SourceCore }
func (t ListDirTool) Schema() map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{"path": map[string]any{"type": "string"}},
	}
}

func (t ListDirTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	raw, _ := args["path"].(string)
	if raw == "" {
		raw = "."
	}
	resolved, err := t.Policy.Resolve(raw)
	if err != nil {
		return "", err
	}
	entries, err := os.ReadDir(resolved)
	if err != nil {
		return "", fmt.Errorf("tool: 列目录失败: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() {
			name += "/"
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return strings.Join(names, "\n"), nil
}

// ---- websearch ----

// WebSearchTool 网络搜索；后端未配置返回明确错误，不编造（T4）。
type WebSearchTool struct {
	Policy Policy
}

func (t WebSearchTool) Name() string        { return "websearch" }
func (t WebSearchTool) Description() string { return "联网搜索" }
func (t WebSearchTool) Source() Source      { return SourceCore }
func (t WebSearchTool) Schema() map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{"query": map[string]any{"type": "string"}},
		"required":   []string{"query"},
	}
}

func (t WebSearchTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	query, _ := args["query"].(string)
	if query == "" {
		return "", fmt.Errorf("%w: query 必填", ErrInvalidArgument)
	}
	if t.Policy.WebSearchBackend == "" {
		return "", fmt.Errorf("tool: websearch 后端未配置")
	}
	return "", fmt.Errorf("tool: websearch 调用暂未实现")
}
