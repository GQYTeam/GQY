package tool

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Policy 是注入核心工具的权限与执行策略（SEC2–SEC4）。
type Policy struct {
	Workspace string
	// Roots 是允许访问的根目录；Workspace 会自动追加。
	Roots []string
	// Denied 是显式拒绝的路径，优先于 Roots 判断。
	Denied []string
	// ShellTimeout 是单条 shell 命令上限；零值回退 DefaultShellTimeout。
	ShellTimeout int
	// MaxOutput 是 shell 合并输出保留的最大字节数；零值回退 DefaultMaxOutput。
	MaxOutput int
	// ShellAllowlist 非空时只允许列表内命令（首 token 的 basename）。
	ShellAllowlist []string
	// WebSearchBackend 为空表示 websearch 未配置。
	WebSearchBackend string
}

const (
	// DefaultShellTimeout 单条 shell 命令默认上限（秒）。
	DefaultShellTimeout = 60
	// DefaultMaxOutput shell 输出默认上限（字节）。
	DefaultMaxOutput = 16 * 1024
	// MaxReadBytes read 默认上限：超过报错，不整读。
	MaxReadBytes = 10 << 20
)

func (p Policy) shellTimeoutSec() int {
	if p.ShellTimeout > 0 {
		return p.ShellTimeout
	}
	return DefaultShellTimeout
}

func (p Policy) maxOutput() int {
	if p.MaxOutput > 0 {
		return p.MaxOutput
	}
	return DefaultMaxOutput
}

// workspaceAbs 返回相对路径解析用的绝对 workspace。
func (p Policy) workspaceAbs() string {
	ws := p.Workspace
	if ws == "" {
		if cwd, err := os.Getwd(); err == nil {
			ws = cwd
		} else {
			ws = "."
		}
	}
	if abs, err := filepath.Abs(ws); err == nil {
		ws = abs
	}
	return filepath.Clean(ws)
}

// Resolve 按 SEC3 固定顺序解析路径：绝对化 → Clean → 最近已存在祖先
// EvalSymlinks → 接回剩余段 → 先查拒绝路径，再查允许根。
func (p Policy) Resolve(raw string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return "", ErrInvalidArgument
	}
	abs := raw
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(p.workspaceAbs(), raw)
	}
	abs = filepath.Clean(abs)

	resolved, err := canonicalPath(abs)
	if err != nil {
		return "", fmt.Errorf("tool: 无法解析路径 %q: %w", raw, err)
	}

	for _, denied := range canonicalList(p.Denied) {
		if withinRoot(denied, resolved) {
			return "", fmt.Errorf("tool: 路径 %q 命中拒绝路径 %s", raw, denied)
		}
	}

	roots := canonicalList(append(append([]string{}, p.Roots...), p.workspaceAbs()))
	if len(roots) == 0 {
		return "", fmt.Errorf("tool: 路径 %q 被拒绝：没有允许根", raw)
	}
	for _, root := range roots {
		if withinRoot(root, resolved) {
			return resolved, nil
		}
	}
	return "", fmt.Errorf("tool: 路径 %q 超出允许范围", raw)
}

// RejectSymlink 拒绝最终目标是符号链接的写目标（SEC3）。
func RejectSymlink(target string) error {
	info, err := os.Lstat(target)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("tool: 写目标 %s 是符号链接，拒绝写入", target)
	}
	return nil
}

// canonicalPath 解析路径中的符号链接，即使路径尚不存在。
func canonicalPath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	abs = filepath.Clean(abs)

	existing := abs
	var rest []string
	for {
		if _, err := os.Lstat(existing); err == nil {
			break
		}
		parent := filepath.Dir(existing)
		if parent == existing {
			return abs, nil
		}
		rest = append([]string{filepath.Base(existing)}, rest...)
		existing = parent
	}

	resolved, err := filepath.EvalSymlinks(existing)
	if err != nil {
		return "", err
	}
	return filepath.Clean(filepath.Join(append([]string{resolved}, rest...)...)), nil
}

// canonicalList 规范化、去重路径列表。
func canonicalList(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, root := range in {
		if strings.TrimSpace(root) == "" {
			continue
		}
		c, err := canonicalPath(root)
		if err != nil {
			continue
		}
		if seen[c] {
			continue
		}
		seen[c] = true
		out = append(out, c)
	}
	return out
}

// withinRoot 判断 target 是否等于 root 或位于 root 之下。
func withinRoot(root, target string) bool {
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	return rel != ".." && !filepath.IsAbs(rel) && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
