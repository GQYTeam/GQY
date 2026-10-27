// 工具路径沙箱与执行策略。
//
// 核心工具（read / edit / list_dir）只能访问 Policy 声明的允许根；
// shell 的工作目录固定为 workspace，并受白名单、超时和输出上限约束。
package tool

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	// DefaultShellTimeout 与 config.DefaultShellTimeout 保持一致。
	DefaultShellTimeout = 60 * time.Second
	// DefaultMaxOutput 与 config.DefaultToolMaxOutput 保持一致。
	DefaultMaxOutput = 16 * 1024
	// MaxReadBytes 是 read 的默认读取上限，超过时返回错误而不是把整文件读进内存。
	MaxReadBytes = 10 << 20
)

// Policy 是注入核心工具的权限与执行策略。
// 零值可用：workspace 为空时相对路径按进程当前目录解析；
// 未配置 Roots 时只允许 workspace（以及进程当前目录）。
type Policy struct {
	// Workspace 是相对路径的解析基准，也是 shell 的工作目录。
	Workspace string
	// Roots 是允许访问的根目录；Workspace 会自动追加。
	Roots []string
	// Denied 是显式拒绝访问的路径（可以位于 Roots 内部，例如 token 文件）。
	// Resolve 时优先于 Roots 判断。
	Denied []string
	// ShellTimeout 是单条 shell 命令的上限，零值回退 DefaultShellTimeout。
	ShellTimeout time.Duration
	// MaxOutput 是 shell 合并输出保留的最大字节数，零值回退 DefaultMaxOutput。
	MaxOutput int
	// ShellAllowlist 非空时只允许执行列表内命令（首 token 的 basename）。
	ShellAllowlist []string
}

func (p Policy) shellTimeout() time.Duration {
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

// workspaceAbs 返回解析相对路径用的绝对 workspace。
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

// canonicalRoots 返回去重后的规范化允许根（解析符号链接，不要求根已存在）。
func (p Policy) canonicalRoots() []string {
	in := make([]string, 0, len(p.Roots)+1)
	in = append(in, p.Roots...)
	in = append(in, p.workspaceAbs())
	return canonicalList(in)
}

// canonicalDenied 返回规范化后的拒绝路径。
func (p Policy) canonicalDenied() []string {
	return canonicalList(p.Denied)
}

// canonicalList 把路径列表规范化、去重；无法解析的项跳过。
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

// Resolve 把工具参数里的路径解析成规范的绝对路径，并校验它落在允许根内。
// 支持相对路径（按 workspace）、绝对路径、`..` 与符号链接：
// 对已存在的前缀做 EvalSymlinks，对新建文件解析最近的已存在祖先。
func (p Policy) Resolve(raw string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return "", errors.New("path 不能为空")
	}
	abs := raw
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(p.workspaceAbs(), raw)
	}
	abs = filepath.Clean(abs)

	resolved, err := canonicalPath(abs)
	if err != nil {
		return "", fmt.Errorf("无法解析路径 %q: %w", raw, err)
	}

	for _, denied := range p.canonicalDenied() {
		if withinRoot(denied, resolved) {
			return "", fmt.Errorf("路径 %q 被拒绝：命中受保护路径 %s", raw, denied)
		}
	}

	roots := p.canonicalRoots()
	if len(roots) == 0 {
		return "", fmt.Errorf("路径 %q 被拒绝：没有可用的允许根目录", raw)
	}
	for _, root := range roots {
		if withinRoot(root, resolved) {
			return resolved, nil
		}
	}
	return "", fmt.Errorf("路径 %q 超出允许范围（允许根: %s）", raw, strings.Join(roots, ", "))
}

// canonicalPath 解析路径中的符号链接，即使路径（或祖先）尚不存在。
// 做法：向上找到最近一个已存在的祖先，EvalSymlinks 后再接回剩余段。
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
			// 到根仍不存在（异常文件系统）：按字面绝对路径返回。
			return abs, nil
		}
		rest = append([]string{filepath.Base(existing)}, rest...)
		existing = parent
	}

	resolved, err := filepath.EvalSymlinks(existing)
	if err != nil {
		return "", err
	}
	parts := append([]string{resolved}, rest...)
	return filepath.Clean(filepath.Join(parts...)), nil
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
	if rel == ".." || filepath.IsAbs(rel) {
		return false
	}
	return !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// readLimited 读取文件，先看大小再读；超过 limit 直接报错，避免 OOM。
func readLimited(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		return nil, fmt.Errorf("%s 是目录，不是文件", path)
	}
	if info.Size() > limit {
		return nil, fmt.Errorf("文件过大（%d 字节，上限 %d 字节）：%s", info.Size(), limit, path)
	}
	// 额外 LimitReader 兜住读取期间文件被追加的情况。
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("文件超过读取上限 %d 字节：%s", limit, path)
	}
	return data, nil
}

// writeFileAtomic 在目标同目录写临时文件再 rename，保留已有文件权限。
func writeFileAtomic(target string, content []byte) (os.FileMode, error) {
	dir := filepath.Dir(target)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return 0, err
	}

	mode := os.FileMode(0o644)
	if info, err := os.Lstat(target); err == nil {
		if info.IsDir() {
			return 0, fmt.Errorf("%s 是目录，不能写入", target)
		}
		mode = info.Mode().Perm()
	} else if !os.IsNotExist(err) {
		return 0, err
	}

	f, err := os.CreateTemp(dir, ".gqy-edit-*")
	if err != nil {
		return 0, err
	}
	tmp := f.Name()
	defer os.Remove(tmp)

	if err := f.Chmod(mode); err != nil {
		f.Close()
		return 0, err
	}
	if _, err := f.Write(content); err != nil {
		f.Close()
		return 0, err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return 0, err
	}
	if err := f.Close(); err != nil {
		return 0, err
	}
	if err := os.Rename(tmp, target); err != nil {
		return 0, err
	}
	return mode, nil
}

// limitedBuffer 是可并发写入的有上限缓冲区（stdout / stderr 共用）。
type limitedBuffer struct {
	mu        sync.Mutex
	buf       []byte
	limit     int
	truncated bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	remaining := b.limit - len(b.buf)
	if remaining <= 0 {
		b.truncated = true
		return len(p), nil
	}
	if len(p) > remaining {
		b.buf = append(b.buf, p[:remaining]...)
		b.truncated = true
		return len(p), nil
	}
	b.buf = append(b.buf, p...)
	return len(p), nil
}

func (b *limitedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	s := string(b.buf)
	if b.truncated {
		s += "\n...[截断]"
	}
	return s
}
