// 包 config：启动配置与环境变量解析。
// 密钥只从服务端环境变量读取，绝不进入前端。
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
	return Config{
		Host:                Env("HOST", "127.0.0.1"),
		Port:                Env("PORT", "5173"),
		APIBase:             strings.TrimRight(Env("GQY_API_BASE", "https://api.deepseek.com/v1"), "/"),
		APIKey:              os.Getenv("GQY_API_KEY"),
		APIUA:               os.Getenv("GQY_API_UA"),
		Model:               Env("GQY_MODEL", "deepseek-chat"),
		Wire:                strings.ToLower(Env("GQY_API_WIRE", "auto")),
		Home:                Env("GQY_HOME", filepath.Join(HomeDir(), ".gqy")),
		Mock:                IsTruthy(os.Getenv("GQY_MOCK")),
		EnableExternal:      IsTruthy(os.Getenv("GQY_ENABLE_EXTERNAL")),
		LowBalance:          EnvFloat("GQY_LOW_BALANCE", 0),
		GitHubClientID:      os.Getenv("GITHUB_CLIENT_ID"),
		GitHubClientSecret:  os.Getenv("GITHUB_CLIENT_SECRET"),
		GitHubOAuthRedirect: os.Getenv("GITHUB_OAUTH_REDIRECT"),
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
