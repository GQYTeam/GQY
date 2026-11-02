package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Build 把启动参数与三份配置文件合成为一份新快照。
// 启动与热更新共用同一条管线（CF4、CF10）。
func Build(startup Startup, version uint64, getenv func(string) string, now time.Time) (*Snapshot, error) {
	if getenv == nil {
		getenv = os.Getenv
	}
	if now.IsZero() {
		now = time.Now()
	}

	providers, err := loadProviders(startup, getenv)
	if err != nil {
		return nil, err
	}
	routing, err := loadRouting(startup, providers)
	if err != nil {
		return nil, err
	}
	settings, err := loadSettings(startup.Home)
	if err != nil {
		return nil, err
	}

	rateLimit := settings.RateLimit
	if startup.RateLimit != nil {
		rateLimit = *startup.RateLimit
	}

	return &Snapshot{
		Version:   version,
		LoadedAt:  now,
		Home:      startup.Home,
		Host:      startup.Host,
		Port:      startup.Port,
		AuthToken: startup.AuthToken,
		Workspace: startup.Workspace,
		ToolRoots: append([]string(nil), startup.ToolRoots...),
		Mock:      startup.Mock,
		RateLimit: rateLimit,
		Providers: providers,
		Routing:   routing,
		Settings:  settings,
	}, nil
}

func readConfigFile(path string) ([]byte, bool, error) {
	data, err := os.ReadFile(path) // #nosec G304 -- 路径来自 GQY_HOME
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, false, nil
		}
		return nil, false, err
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, false, nil
	}
	return data, true, nil
}

func loadProviders(startup Startup, getenv func(string) string) ([]Provider, error) {
	path := filepath.Join(startup.Home, "providers.json")
	data, ok, err := readConfigFile(path)
	if err != nil {
		return nil, fmt.Errorf("providers.json: %w", err)
	}
	if !ok {
		return envProviders(startup), nil
	}

	var file struct {
		Providers []Provider `json:"providers"`
	}
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("providers.json: JSON 解析失败: %w", err)
	}
	if len(file.Providers) == 0 {
		// CF9：文件存在但为空，等同于缺失。
		return envProviders(startup), nil
	}
	return normalizeProviders(file.Providers, getenv)
}

func envProviders(startup Startup) []Provider {
	if startup.APIKey == "" {
		return []Provider{}
	}
	model := strings.TrimSpace(startup.Model)
	if model == "" {
		model = DefaultModel
	}
	return []Provider{{
		ID:             "env",
		DisplayName:    "环境变量",
		BaseURL:        strings.TrimRight(startup.APIBase, "/"),
		Protocol:       ProtocolOpenAIChat,
		DefaultModel:   model,
		Models:         []string{model},
		Accounts:       []Account{{ID: "default", APIKey: startup.APIKey}},
		MaxConcurrency: 4,
		UserAgent:      startup.APIUA,
	}}
}

func normalizeProviders(in []Provider, getenv func(string) string) ([]Provider, error) {
	problems := make([]string, 0)
	seen := make(map[string]bool, len(in))
	out := make([]Provider, 0, len(in))

	for i, raw := range in {
		p := raw
		p.ID = strings.TrimSpace(p.ID)
		p.DisplayName = strings.TrimSpace(p.DisplayName)
		p.BaseURL = strings.TrimRight(strings.TrimSpace(p.BaseURL), "/")
		p.Protocol = strings.ToLower(strings.TrimSpace(p.Protocol))
		p.DefaultModel = strings.TrimSpace(p.DefaultModel)
		p.UserAgent = strings.TrimSpace(p.UserAgent)

		label := fmt.Sprintf("providers[%d]", i)
		if p.ID == "" {
			problems = append(problems, label+": id 不能为空")
			continue
		}
		label = "provider " + p.ID
		if seen[p.ID] {
			problems = append(problems, label+": id 重复")
			continue
		}
		seen[p.ID] = true

		if p.BaseURL == "" {
			problems = append(problems, label+": base_url 不能为空")
		} else if !strings.HasPrefix(p.BaseURL, "http://") && !strings.HasPrefix(p.BaseURL, "https://") {
			problems = append(problems, label+": base_url 必须以 http:// 或 https:// 开头")
		}
		if p.Protocol == "" {
			p.Protocol = ProtocolDefault
		}
		if !validProtocol(p.Protocol) {
			problems = append(problems, label+": protocol 只能是 openai-chat / anthropic-messages / gemini")
		}
		if p.DisplayName == "" {
			p.DisplayName = p.ID
		}

		p.Models = normalizeStrings(p.Models)
		if p.DefaultModel == "" && len(p.Models) > 0 {
			p.DefaultModel = p.Models[0]
		}
		if p.DefaultModel == "" && len(p.Models) == 0 {
			problems = append(problems, label+": default_model 与 models 不能同时为空")
		}
		if p.DefaultModel != "" && !containsString(p.Models, p.DefaultModel) {
			p.Models = append([]string{p.DefaultModel}, p.Models...)
		}

		if p.MaxConcurrency < 0 {
			problems = append(problems, label+": max_concurrency 不能为负数")
		}
		if p.MaxConcurrency == 0 {
			p.MaxConcurrency = DefaultMaxConcurrent
		}

		accounts, errs := normalizeAccounts(p, getenv)
		problems = append(problems, errs...)
		p.Accounts = accounts

		out = append(out, p)
	}

	if len(problems) > 0 {
		return nil, fmt.Errorf("providers.json: %s", strings.Join(problems, "; "))
	}
	return out, nil
}

func normalizeAccounts(p Provider, getenv func(string) string) ([]Account, []string) {
	problems := make([]string, 0)
	if len(p.Accounts) == 0 {
		p.Accounts = []Account{{ID: "default"}}
	}

	seen := make(map[string]bool, len(p.Accounts))
	out := make([]Account, 0, len(p.Accounts))
	for i, raw := range p.Accounts {
		a := raw
		a.ID = strings.TrimSpace(a.ID)
		a.Name = strings.TrimSpace(a.Name)
		label := fmt.Sprintf("provider %s accounts[%d]", p.ID, i)
		if a.ID == "" {
			problems = append(problems, label+": id 不能为空")
			continue
		}
		label = fmt.Sprintf("provider %s account %s", p.ID, a.ID)
		if seen[a.ID] {
			problems = append(problems, label+": id 重复")
			continue
		}
		seen[a.ID] = true
		if a.DailyTokenLimit < 0 {
			problems = append(problems, label+": daily_token_limit 不能为负数")
		}
		if a.APIKey == "" {
			a.APIKey = getenv("GQY_PROVIDER_" + EnvName(p.ID) + "_API_KEY")
		}
		out = append(out, a)
	}
	return out, problems
}

func loadRouting(startup Startup, providers []Provider) (Routing, error) {
	path := filepath.Join(startup.Home, "routing.json")
	data, ok, err := readConfigFile(path)
	if err != nil {
		return Routing{}, fmt.Errorf("routing.json: %w", err)
	}

	var file routingFile
	if ok {
		if err := json.Unmarshal(data, &file); err != nil {
			return Routing{}, fmt.Errorf("routing.json: JSON 解析失败: %w", err)
		}
	}

	r := Routing{
		Default:         file.Default,
		Groups:          file.Groups,
		MaxAttempts:     DefaultMaxAttempts,
		CooldownSeconds: DefaultCooldownSecs,
		QueueTimeoutMS:  DefaultQueueTimeout,
	}
	if file.MaxAttempts != nil {
		r.MaxAttempts = *file.MaxAttempts
	}
	if file.CooldownSeconds != nil {
		r.CooldownSeconds = *file.CooldownSeconds
	}
	if file.QueueTimeoutMS != nil {
		r.QueueTimeoutMS = *file.QueueTimeoutMS
	}
	if file.Tenants != nil {
		r.Tenants = file.Tenants
	}

	problems := make([]string, 0)
	if r.MaxAttempts < 0 {
		problems = append(problems, "max_attempts 不能为负数")
	}
	if r.MaxAttempts == 0 {
		r.MaxAttempts = DefaultMaxAttempts
	}
	if r.MaxAttempts > MaxAttemptsLimit {
		problems = append(problems, fmt.Sprintf("max_attempts 不能大于 %d", MaxAttemptsLimit))
	}
	if r.CooldownSeconds < 0 || r.CooldownSeconds > MaxCooldownSeconds {
		problems = append(problems, fmt.Sprintf("cooldown_seconds 必须在 0–%d 之间", MaxCooldownSeconds))
	}
	if r.CooldownSeconds == 0 {
		r.CooldownSeconds = DefaultCooldownSecs
	}
	if r.QueueTimeoutMS < 0 {
		problems = append(problems, "queue_timeout_ms 不能为负数")
	}
	seenTenant := map[string]bool{}
	for i := range r.Tenants {
		t := &r.Tenants[i]
		t.Name = strings.TrimSpace(t.Name)
		if t.Name == "" {
			problems = append(problems, fmt.Sprintf("tenants[%d].name 不能为空", i))
			continue
		}
		if !validTenantName(t.Name) {
			problems = append(problems, "tenants["+t.Name+"].name 只能包含字母、数字、下划线、点、短横线，长度 1–64")
		}
		if seenTenant[t.Name] {
			problems = append(problems, "tenants 名称重复: "+t.Name)
		}
		seenTenant[t.Name] = true
		if t.MaxConcurrency < 0 || t.MaxConcurrency > 1024 {
			problems = append(problems, "tenants["+t.Name+"].max_concurrency 必须在 0–1024 之间（0 表示不单独限制）")
		}
		if t.ReqPerMin < 0 || t.ReqPerMin > 1_000_000 {
			problems = append(problems, "tenants["+t.Name+"].req_per_min 必须在 0–1000000 之间（0 表示不限）")
		}
	}
	if r.QueueTimeoutMS == 0 {
		r.QueueTimeoutMS = DefaultQueueTimeout
	}

	providerByID := make(map[string]bool, len(providers))
	for _, p := range providers {
		providerByID[p.ID] = true
	}

	groups := make([]Group, 0, len(r.Groups))
	groupIDs := make(map[string]bool, len(r.Groups))
	for i, raw := range r.Groups {
		g := raw
		g.ID = strings.TrimSpace(g.ID)
		g.Name = strings.TrimSpace(g.Name)
		g.Strategy = strings.ToLower(strings.TrimSpace(g.Strategy))
		label := fmt.Sprintf("groups[%d]", i)
		if g.ID == "" {
			problems = append(problems, label+": id 不能为空")
			continue
		}
		label = "group " + g.ID
		if groupIDs[g.ID] {
			problems = append(problems, label+": id 重复")
			continue
		}
		groupIDs[g.ID] = true
		if g.Name == "" {
			g.Name = g.ID
		}
		if g.Strategy == "" {
			g.Strategy = StrategyOrder
		}
		if !validStrategy(g.Strategy) {
			problems = append(problems, label+": strategy 只能是 order / rotate / usage")
		}
		g.Members = normalizeStrings(g.Members)
		if len(g.Members) == 0 {
			problems = append(problems, label+": members 不能为空")
		}
		groups = append(groups, g)
	}
	r.Groups = groups

	for _, g := range r.Groups {
		for _, member := range g.Members {
			if strings.HasPrefix(member, "group/") {
				id := strings.TrimPrefix(member, "group/")
				if id == "" {
					problems = append(problems, "group "+g.ID+": 成员 "+member+" 缺少组 id")
					continue
				}
				if id == g.ID {
					problems = append(problems, "group "+g.ID+": 不能引用自身")
					continue
				}
				if !groupIDs[id] {
					problems = append(problems, "group "+g.ID+": 引用了不存在的组 "+id)
				}
				continue
			}
			if provider, model, ok := strings.Cut(member, "/"); ok {
				if provider == "" || model == "" {
					problems = append(problems, "group "+g.ID+": 成员 "+member+" 不是合法的 provider/model")
					continue
				}
				if !providerByID[provider] {
					problems = append(problems, "group "+g.ID+": 引用了不存在的 provider "+provider)
				}
				continue
			}
			if !bareModelKnown(member, providers) {
				problems = append(problems, "group "+g.ID+": 未知模型 "+member)
			}
		}
	}

	if cycle := findGroupCycle(r.Groups); cycle != "" {
		problems = append(problems, "路由组存在循环引用: "+cycle)
	}

	if r.Default == nil {
		if len(providers) > 0 {
			p := providers[0]
			for _, candidate := range providers {
				if candidate.ID == "env" {
					p = candidate
					break
				}
			}
			r.Default = &ModelRef{Provider: p.ID, Model: p.DefaultModel}
		}
	} else {
		r.Default.Provider = strings.TrimSpace(r.Default.Provider)
		r.Default.Model = strings.TrimSpace(r.Default.Model)
		if r.Default.Provider == "" {
			problems = append(problems, "default.provider 不能为空")
		} else if !providerByID[r.Default.Provider] {
			problems = append(problems, "default 引用了不存在的 provider "+r.Default.Provider)
		}
		if r.Default.Model == "" {
			problems = append(problems, "default.model 不能为空")
		}
	}

	if len(problems) > 0 {
		return Routing{}, fmt.Errorf("routing.json: %s", strings.Join(problems, "; "))
	}
	return r, nil
}

type routingFile struct {
	Default         *ModelRef `json:"default"`
	Groups          []Group   `json:"groups"`
	MaxAttempts     *int      `json:"max_attempts"`
	CooldownSeconds *int      `json:"cooldown_seconds"`
	QueueTimeoutMS  *int      `json:"queue_timeout_ms"`
	Tenants         []Tenant  `json:"tenants"`
}

func loadSettings(home string) (Settings, error) {
	path := filepath.Join(home, "settings.json")
	data, ok, err := readConfigFile(path)
	if err != nil {
		return Settings{}, fmt.Errorf("settings.json: %w", err)
	}
	settings := DefaultSettings()
	if !ok {
		return settings, nil
	}

	var file settingsFile
	if err := json.Unmarshal(data, &file); err != nil {
		return Settings{}, fmt.Errorf("settings.json: JSON 解析失败: %w", err)
	}
	if file.WatchEnabled != nil {
		settings.WatchEnabled = *file.WatchEnabled
	}
	if file.RateLimit != nil {
		settings.RateLimit = *file.RateLimit
	}
	if file.ToolTimeoutS != nil {
		settings.ToolTimeoutS = *file.ToolTimeoutS
	}
	if file.ShellTimeoutS != nil {
		settings.ShellTimeoutS = *file.ShellTimeoutS
	}
	if file.UpstreamTimeoutS != nil {
		settings.UpstreamTimeoutS = *file.UpstreamTimeoutS
	}
	if file.StreamIdleTimeoutS != nil {
		settings.StreamIdleTimeoutS = *file.StreamIdleTimeoutS
	}
	if file.ExposeExternalTools != nil {
		settings.ExposeExternalTools = *file.ExposeExternalTools
	}
	if file.ShellAllowlist != nil {
		settings.ShellAllowlist = normalizeStrings(file.ShellAllowlist)
	}

	problems := make([]string, 0)
	if settings.RateLimit < 0 {
		problems = append(problems, "rate_limit 不能为负数")
	}
	if settings.ToolTimeoutS <= 0 {
		problems = append(problems, "tool_timeout_s 必须大于 0")
	}
	if settings.ShellTimeoutS <= 0 {
		problems = append(problems, "shell_timeout_s 必须大于 0")
	}
	if settings.UpstreamTimeoutS <= 0 || settings.UpstreamTimeoutS > 3600 {
		problems = append(problems, "upstream_timeout_s 必须在 1–3600 之间")
	}
	if settings.StreamIdleTimeoutS < 5 || settings.StreamIdleTimeoutS > 600 {
		problems = append(problems, "stream_idle_timeout_s 必须在 5–600 之间")
	}
	if len(problems) > 0 {
		return Settings{}, fmt.Errorf("settings.json: %s", strings.Join(problems, "; "))
	}
	return settings, nil
}

type settingsFile struct {
	WatchEnabled        *bool    `json:"watch_enabled"`
	RateLimit           *int     `json:"rate_limit"`
	ToolTimeoutS        *int     `json:"tool_timeout_s"`
	ShellTimeoutS       *int     `json:"shell_timeout_s"`
	UpstreamTimeoutS    *int     `json:"upstream_timeout_s"`
	StreamIdleTimeoutS  *int     `json:"stream_idle_timeout_s"`
	ExposeExternalTools *bool    `json:"expose_external_tools"`
	ShellAllowlist      []string `json:"shell_allowlist"`
}

func validProtocol(p string) bool {
	switch p {
	case ProtocolOpenAIChat, ProtocolAnthropic, ProtocolGemini:
		return true
	}
	return false
}

func validStrategy(s string) bool {
	switch s {
	case StrategyOrder, StrategyRotate, StrategyUsage:
		return true
	}
	return false
}

func containsString(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}

func normalizeStrings(in []string) []string {
	out := make([]string, 0, len(in))
	seen := make(map[string]bool, len(in))
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

func bareModelKnown(model string, providers []Provider) bool {
	for _, p := range providers {
		if p.DefaultModel == model {
			return true
		}
	}
	return false
}

func findGroupCycle(groups []Group) string {
	byID := make(map[string]Group, len(groups))
	for _, g := range groups {
		byID[g.ID] = g
	}
	const (
		unvisited = 0
		visiting  = 1
		done      = 2
	)
	state := make(map[string]int, len(groups))
	stack := make([]string, 0, len(groups))

	var visit func(string) string
	visit = func(id string) string {
		switch state[id] {
		case visiting:
			return id
		case done:
			return ""
		}
		state[id] = visiting
		stack = append(stack, id)
		for _, member := range byID[id].Members {
			if !strings.HasPrefix(member, "group/") {
				continue
			}
			next := strings.TrimPrefix(member, "group/")
			if cycle := visit(next); cycle != "" {
				return cycle
			}
		}
		stack = stack[:len(stack)-1]
		state[id] = done
		return ""
	}

	for _, g := range groups {
		if cycle := visit(g.ID); cycle != "" {
			return cycle
		}
	}
	return ""
}

// validTenantName 校验租户名：字母、数字、下划线、点、短横线，长度 1–64（MT2）。
func validTenantName(name string) bool {
	if name == "" || len(name) > 64 {
		return false
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '.', r == '-':
		default:
			return false
		}
	}
	return true
}
