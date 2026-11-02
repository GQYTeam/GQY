package config

import "time"

// 协议白名单（C4）；空值等价 openai-chat。
const (
	ProtocolOpenAIChat   = "openai-chat"
	ProtocolAnthropic    = "anthropic-messages"
	ProtocolGemini       = "gemini"
	ProtocolDefault      = ProtocolOpenAIChat
	DefaultMaxAttempts   = 3
	DefaultCooldownSecs  = 30
	DefaultQueueTimeout  = 1500
	DefaultMaxConcurrent = 2
	MaxAttemptsLimit     = 5
	MaxCooldownSeconds   = 600
)

// 路由策略（R2）。
const (
	StrategyOrder  = "order"
	StrategyRotate = "rotate"
	StrategyUsage  = "usage"
)

// Provider 是一个上游供应商及其账号（R5）。
type Provider struct {
	ID             string    `json:"id"`
	DisplayName    string    `json:"display_name"`
	BaseURL        string    `json:"base_url"`
	Protocol       string    `json:"protocol"`
	DefaultModel   string    `json:"default_model"`
	Models         []string  `json:"models"`
	Accounts       []Account `json:"accounts"`
	MaxConcurrency int       `json:"max_concurrency"`
	UserAgent      string    `json:"user_agent,omitempty"`
}

// Account 是 provider 下的一个账号或一把密钥。
type Account struct {
	ID              string `json:"id"`
	Name            string `json:"name,omitempty"`
	APIKey          string `json:"api_key,omitempty"`
	DailyTokenLimit int64  `json:"daily_token_limit,omitempty"`
	Disabled        bool   `json:"disabled,omitempty"`
}

// ModelRef 是 provider/model 引用。
type ModelRef struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
}

// Group 是路由组（R2）。
type Group struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Strategy string   `json:"strategy"`
	Members  []string `json:"members"`
}

// Routing 对应 routing.json。
type Routing struct {
	Default         *ModelRef `json:"default"`
	Groups          []Group   `json:"groups"`
	MaxAttempts     int       `json:"max_attempts"`
	CooldownSeconds int       `json:"cooldown_seconds"`
	QueueTimeoutMS  int       `json:"queue_timeout_ms"`
}

// Settings 对应 settings.json。
type Settings struct {
	WatchEnabled       bool     `json:"watch_enabled"`
	RateLimit          int      `json:"rate_limit"`
	ToolTimeoutS       int      `json:"tool_timeout_s"`
	ShellTimeoutS      int      `json:"shell_timeout_s"`
	UpstreamTimeoutS   int      `json:"upstream_timeout_s"`
	StreamIdleTimeoutS int      `json:"stream_idle_timeout_s"`
	ShellAllowlist     []string `json:"shell_allowlist"`
}

// DefaultSettings 返回 settings.json 的全默认值（CF9）。
func DefaultSettings() Settings {
	return Settings{
		WatchEnabled:       true,
		UpstreamTimeoutS:   120,
		StreamIdleTimeoutS: 60,
		RateLimit:          0,
		ToolTimeoutS:       120,
		ShellTimeoutS:      60,
	}
}

// Snapshot 是运行时唯一的事实来源（CF1）。整体不可变，重载时整份替换。
type Snapshot struct {
	Version   uint64
	LoadedAt  time.Time
	Home      string
	Host      string
	Port      string
	AuthToken string
	Workspace string
	ToolRoots []string
	Mock      bool
	RateLimit int

	Providers []Provider
	Routing   Routing
	Settings  Settings
}

// ProviderView 是读接口使用的不含密钥视图（R5、CF8、A8）。
type ProviderView struct {
	ID             string        `json:"id"`
	DisplayName    string        `json:"display_name"`
	BaseURL        string        `json:"base_url"`
	Protocol       string        `json:"protocol"`
	DefaultModel   string        `json:"default_model"`
	Models         []string      `json:"models"`
	MaxConcurrency int           `json:"max_concurrency"`
	Accounts       []AccountView `json:"accounts"`
}

// AccountView 只说明密钥是否已配置，绝不携带明文。
type AccountView struct {
	ID              string `json:"id"`
	Name            string `json:"name,omitempty"`
	APIKeySet       bool   `json:"api_key_set"`
	DailyTokenLimit int64  `json:"daily_token_limit"`
	Disabled        bool   `json:"disabled"`
}

// ProviderViews 返回安全的 provider 列表。
func (s *Snapshot) ProviderViews() []ProviderView {
	out := make([]ProviderView, 0, len(s.Providers))
	for _, p := range s.Providers {
		view := ProviderView{
			ID:             p.ID,
			DisplayName:    p.DisplayName,
			BaseURL:        p.BaseURL,
			Protocol:       p.Protocol,
			DefaultModel:   p.DefaultModel,
			Models:         append([]string(nil), p.Models...),
			MaxConcurrency: p.MaxConcurrency,
			Accounts:       make([]AccountView, 0, len(p.Accounts)),
		}
		for _, a := range p.Accounts {
			view.Accounts = append(view.Accounts, AccountView{
				ID:              a.ID,
				Name:            a.Name,
				APIKeySet:       a.APIKey != "",
				DailyTokenLimit: a.DailyTokenLimit,
				Disabled:        a.Disabled,
			})
		}
		out = append(out, view)
	}
	return out
}
