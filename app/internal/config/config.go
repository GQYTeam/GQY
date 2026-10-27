// 包 config：启动配置与环境变量解析。
// 密钥只从服务端环境变量读取，绝不进入前端。
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	// DefaultToolTimeout 单次工具调用的默认上限。要大于 ShellTimeout，否则 shell 会先被 agent 层取消。
	DefaultToolTimeout = 120 * time.Second
	// DefaultShellTimeout 单条 shell 命令的默认上限。
	DefaultShellTimeout = 60 * time.Second
	// DefaultToolMaxOutput shell 输出默认上限（字节）。
	DefaultToolMaxOutput = 16 * 1024
)

type Config struct {
	Host    string
	Port    string
	APIBase string
	APIKey  string
	APIUA   string
	Model   string
	Wire    string // auto | chat | responses
	Home    string
	Mock    bool
	// EnableExternal 为 true 时把外部工具（插件/skill/mcp）也注入模型。
	EnableExternal bool
	// LowBalance 是 provider_usage 的余额阈值（低于时提醒模型收尾）。
	LowBalance float64
	// GitHub OAuth 凭据（可选，用于只读账户插件）
	GitHubClientID     string
	GitHubClientSecret string
	// GitHubOAuthRedirect 是 OAuth 回调地址（默认 http://127.0.0.1:PORT/api/github/callback）
	GitHubOAuthRedirect string

	// Workspace 是相对路径的解析基准，也是 shell 的固定工作目录。默认启动目录。
	Workspace string
	// ToolRoots 是 read / edit / list_dir 允许访问的根目录；默认 workspace + $GQY_HOME/sessions。
	ToolRoots []string
	// ToolTimeout 是 agent loop 里单次工具调用的上限。
	ToolTimeout time.Duration
	// ShellTimeout 是单条 shell 命令的上限，超时后终止整个进程组。
	ShellTimeout time.Duration
	// ToolMaxOutput 是 shell 合并输出保留的最大字节数。
	ToolMaxOutput int
	// ShellAllowlist 非空时，shell 只允许执行列表里的命令（首 token 的 basename）。
	ShellAllowlist []string
}

// Provider 返回当前上游类型：真实密钥 → openai-compatible，否则 mock / none。
func (c Config) Provider() string {
	switch {
	case c.APIKey != "":
		return "openai-compatible"
	case c.Mock:
		return "mock"
	default:
		return "none"
	}
}

// Load 从环境变量（含已加载的 .env）构造配置。
func Load() Config {
	workspace := Env("GQY_WORKSPACE", "")
	if workspace == "" {
		if cwd, err := os.Getwd(); err == nil {
			workspace = cwd
		} else {
			workspace = "."
		}
	}
	if abs, err := filepath.Abs(workspace); err == nil {
		workspace = abs
	}
	home := Env("GQY_HOME", filepath.Join(HomeDir(), ".gqy"))

	// 默认允许根：workspace + $GQY_HOME/sessions（只暴露会话产物，不暴露 token 等凭据）；
	// GQY_TOOL_ROOTS 追加（冒号或逗号分隔）。
	roots := []string{workspace, filepath.Join(home, "sessions")}
	roots = append(roots, SplitList(os.Getenv("GQY_TOOL_ROOTS"))...)

	return Config{
		Host:                Env("HOST", "127.0.0.1"),
		Port:                Env("PORT", "5173"),
		APIBase:             strings.TrimRight(Env("GQY_API_BASE", "https://api.deepseek.com/v1"), "/"),
		APIKey:              os.Getenv("GQY_API_KEY"),
		APIUA:               os.Getenv("GQY_API_UA"),
		Model:               Env("GQY_MODEL", "deepseek-chat"),
		Wire:                strings.ToLower(Env("GQY_API_WIRE", "auto")),
		Home:                home,
		Mock:                IsTruthy(os.Getenv("GQY_MOCK")),
		EnableExternal:      IsTruthy(os.Getenv("GQY_ENABLE_EXTERNAL")),
		LowBalance:          EnvFloat("GQY_LOW_BALANCE", 0),
		GitHubClientID:      os.Getenv("GITHUB_CLIENT_ID"),
		GitHubClientSecret:  os.Getenv("GITHUB_CLIENT_SECRET"),
		GitHubOAuthRedirect: os.Getenv("GITHUB_OAUTH_REDIRECT"),
		Workspace:           workspace,
		ToolRoots:           Dedupe(roots),
		ToolTimeout:         EnvDuration("GQY_TOOL_TIMEOUT", DefaultToolTimeout),
		ShellTimeout:        EnvDuration("GQY_SHELL_TIMEOUT", DefaultShellTimeout),
		ToolMaxOutput:       EnvInt("GQY_TOOL_MAX_OUTPUT", DefaultToolMaxOutput),
		ShellAllowlist:      SplitList(os.Getenv("GQY_SHELL_ALLOWLIST")),
	}
}

func Env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func EnvFloat(key string, fallback float64) float64 {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	var f float64
	if _, err := fmt.Sscanf(v, "%f", &f); err == nil {
		return f
	}
	return fallback
}

// EnvDuration 读取时长：支持 "90s" / "2m" 这类 Go duration，也接受纯数字（按秒）。
func EnvDuration(key string, fallback time.Duration) time.Duration {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	if d, err := time.ParseDuration(v); err == nil {
		if d <= 0 {
			return fallback
		}
		return d
	}
	var secs float64
	if _, err := fmt.Sscanf(v, "%f", &secs); err == nil && secs > 0 {
		return time.Duration(secs * float64(time.Second))
	}
	return fallback
}

// EnvInt 读取正整数；非法或非正数时回退。
func EnvInt(key string, fallback int) int {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	var n int
	if _, err := fmt.Sscanf(v, "%d", &n); err == nil && n > 0 {
		return n
	}
	return fallback
}

// SplitList 按冒号或逗号拆分环境变量列表，去掉空白与空项。
// 用于 GQY_TOOL_ROOTS / GQY_SHELL_ALLOWLIST。
func SplitList(v string) []string {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	fields := strings.FieldsFunc(v, func(r rune) bool {
		return r == ':' || r == ','
	})
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if f = strings.TrimSpace(f); f != "" {
			out = append(out, f)
		}
	}
	return out
}

// Dedupe 去重并丢弃空项，保持首次出现的顺序。
func Dedupe(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, v := range in {
		v = strings.TrimSpace(v)
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

func IsTruthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

func HomeDir() string {
	if h, err := os.UserHomeDir(); err == nil {
		return h
	}
	return "."
}

// LoadDotEnv 读 .env，只填环境里不存在的变量；真实环境变量（含显式空值）优先。
func LoadDotEnv(name string) {
	data, err := os.ReadFile(name)
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		if key == "" {
			continue
		}
		// LookupEnv 区分「没设置」和「显式设为空」：后者用于强制关闭 .env 里的值
		if _, exists := os.LookupEnv(key); !exists {
			_ = os.Setenv(key, value)
		}
	}
}
