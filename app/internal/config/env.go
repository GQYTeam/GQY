// Package config 负责启动参数、三份配置文件的解析与校验，以及不可变配置快照。
//
// 设计依据：docs/designs/12-配置与热更新.md（CF1–CF10）、
// docs/designs/02-网关与路由.md（R5、R6）。
package config

import (
	"bufio"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	// DefaultHost 默认只监听回环地址（A3）。
	DefaultHost = "127.0.0.1"
	// DefaultPort 是默认监听端口。
	DefaultPort = "5173"
	// DefaultAPIBase 是环境变量 provider 的默认上游地址。
	DefaultAPIBase = "https://api.deepseek.com/v1"
	// DefaultModel 是环境变量 provider 的默认模型。
	DefaultModel = "deepseek-chat"
)

// Startup 是只在启动时读取、改动需要重启的进程参数（CF2）。
type Startup struct {
	Home           string
	Host           string
	Port           string
	AuthToken      string
	Workspace      string
	ToolRoots      []string
	Mock           bool
	EnableExternal bool

	// 环境变量 provider 兜底（CF9）。
	APIBase string
	APIKey  string
	APIUA   string
	Model   string

	// RateLimit 非 nil 时覆盖 settings.json 的 rate_limit（A4）。
	RateLimit *int
}

// LoadStartup 从环境变量构造启动参数。它不读配置文件。
func LoadStartup() Startup {
	home := Env("GQY_HOME", filepath.Join(HomeDir(), ".gqy"))
	if abs, err := filepath.Abs(home); err == nil {
		home = abs
	}

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

	roots := []string{workspace, filepath.Join(home, "sessions")}
	roots = append(roots, SplitList(os.Getenv("GQY_TOOL_ROOTS"))...)

	var rateLimit *int
	if raw := strings.TrimSpace(os.Getenv("GQY_RATE_LIMIT")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n >= 0 {
			rateLimit = &n
		}
	}

	return Startup{
		Home:           home,
		Host:           Env("HOST", DefaultHost),
		Port:           Env("PORT", DefaultPort),
		AuthToken:      os.Getenv("GQY_AUTH_TOKEN"),
		Workspace:      workspace,
		ToolRoots:      Dedupe(roots),
		Mock:           IsTruthy(os.Getenv("GQY_MOCK")),
		EnableExternal: IsTruthy(os.Getenv("GQY_ENABLE_EXTERNAL")),
		APIBase:        strings.TrimRight(Env("GQY_API_BASE", DefaultAPIBase), "/"),
		APIKey:         os.Getenv("GQY_API_KEY"),
		APIUA:          os.Getenv("GQY_API_UA"),
		Model:          Env("GQY_MODEL", DefaultModel),
		RateLimit:      rateLimit,
	}
}

// Env 返回环境变量，空值时用 fallback。
func Env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// SplitList 按冒号或逗号拆分列表，去掉空白与空项。
func SplitList(v string) []string {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	fields := strings.FieldsFunc(v, func(r rune) bool { return r == ':' || r == ',' })
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

// IsTruthy 识别常见的布尔环境变量写法。
func IsTruthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// HomeDir 返回当前用户主目录。
func HomeDir() string {
	if h, err := os.UserHomeDir(); err == nil {
		return h
	}
	return "."
}

// LoadDotEnv 读取 .env，只填充环境里不存在的变量；真实环境变量优先。
func LoadDotEnv(name string) {
	f, err := os.Open(name) // #nosec G304 -- .env 路径来自启动参数
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" {
			continue
		}
		if len(value) >= 2 {
			if (value[0] == '"' && value[len(value)-1] == '"') ||
				(value[0] == '\'' && value[len(value)-1] == '\'') {
				value = value[1 : len(value)-1]
			}
		}
		if _, exists := os.LookupEnv(key); !exists {
			_ = os.Setenv(key, value)
		}
	}
}

// EnvName 把 provider id 转成环境变量片段：大写，非字母数字转下划线（R5）。
func EnvName(id string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(id) {
		switch {
		case r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}
