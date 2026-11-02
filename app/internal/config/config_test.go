package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func testStartup(home string) Startup {
	return Startup{
		Home:      home,
		Host:      DefaultHost,
		Port:      DefaultPort,
		Workspace: home,
		ToolRoots: []string{home},
		APIBase:   DefaultAPIBase,
		Model:     DefaultModel,
	}
}

func writeTestFile(t *testing.T, home, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(home, name), []byte(content), 0o600); err != nil {
		t.Fatalf("写入 %s: %v", name, err)
	}
}

func noEnv(string) string { return "" }

func TestBuildDefaults(t *testing.T) {
	home := t.TempDir()
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)

	snap, err := Build(testStartup(home), 1, noEnv, now)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if snap.Version != 1 || !snap.LoadedAt.Equal(now) {
		t.Fatalf("版本/时间不对: %+v", snap)
	}
	if len(snap.Providers) != 0 {
		t.Fatalf("无配置时应没有 provider，得到 %d 个", len(snap.Providers))
	}
	if snap.Routing.Default != nil {
		t.Fatalf("无 provider 时 default 应为空: %+v", snap.Routing.Default)
	}
	if snap.Routing.MaxAttempts != DefaultMaxAttempts ||
		snap.Routing.CooldownSeconds != DefaultCooldownSecs ||
		snap.Routing.QueueTimeoutMS != DefaultQueueTimeout {
		t.Fatalf("路由默认值不对: %+v", snap.Routing)
	}
	want := DefaultSettings()
	if !reflect.DeepEqual(snap.Settings, want) {
		t.Fatalf("设置默认值不对: %+v", snap.Settings)
	}
}

func TestEnvProviderFallback(t *testing.T) {
	home := t.TempDir()
	startup := testStartup(home)
	startup.APIKey = "sk-env"
	startup.Model = "env-model"
	startup.APIBase = "https://example.test/v1/"
	rate := 7
	startup.RateLimit = &rate
	writeTestFile(t, home, "settings.json", `{"rate_limit": 2}`)

	snap, err := Build(startup, 1, noEnv, time.Now())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(snap.Providers) != 1 {
		t.Fatalf("应回退为一个 env provider，得到 %d", len(snap.Providers))
	}
	p := snap.Providers[0]
	if p.ID != "env" || p.DefaultModel != "env-model" || p.BaseURL != "https://example.test/v1" {
		t.Fatalf("env provider 不对: %+v", p)
	}
	if len(p.Accounts) != 1 || p.Accounts[0].APIKey != "sk-env" {
		t.Fatalf("env 账号不对: %+v", p.Accounts)
	}
	if snap.Routing.Default == nil || snap.Routing.Default.Provider != "env" || snap.Routing.Default.Model != "env-model" {
		t.Fatalf("默认模型应指向 env: %+v", snap.Routing.Default)
	}
	if snap.RateLimit != 7 {
		t.Fatalf("环境变量限流应覆盖文件: %d", snap.RateLimit)
	}
}

func TestProvidersFileAndKeyResolution(t *testing.T) {
	home := t.TempDir()
	writeTestFile(t, home, "providers.json", `{
	  "providers": [
	    {
	      "id": "deepseek",
	      "display_name": "DeepSeek",
	      "base_url": "https://api.deepseek.test/v1/",
	      "default_model": "deepseek-chat",
	      "models": ["deepseek-chat", "deepseek-reasoner"],
	      "accounts": [
	        {"id": "a1", "api_key": "sk-file", "daily_token_limit": 100},
	        {"id": "a2"}
	      ],
	      "max_concurrency": 3
	    }
	  ]
	}`)
	writeTestFile(t, home, "routing.json", `{
	  "default": {"provider": "deepseek", "model": "deepseek-chat"},
	  "groups": [
	    {"id": "daily", "name": "日常", "strategy": "rotate", "members": ["deepseek/deepseek-reasoner", "deepseek/deepseek-chat"]}
	  ]
	}`)
	env := func(key string) string {
		if key == "GQY_PROVIDER_DEEPSEEK_API_KEY" {
			return "sk-env-account"
		}
		return ""
	}

	snap, err := Build(testStartup(home), 1, env, time.Now())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	p := snap.Providers[0]
	if p.BaseURL != "https://api.deepseek.test/v1" {
		t.Fatalf("base_url 应去掉末尾斜杠: %q", p.BaseURL)
	}
	if p.Accounts[0].APIKey != "sk-file" {
		t.Fatalf("文件密钥应优先: %q", p.Accounts[0].APIKey)
	}
	if p.Accounts[1].APIKey != "sk-env-account" {
		t.Fatalf("空密钥应回退环境变量: %q", p.Accounts[1].APIKey)
	}
	if got := snap.Routing.Groups[0].Strategy; got != StrategyRotate {
		t.Fatalf("策略不对: %q", got)
	}

	data, err := json.Marshal(snap.ProviderViews())
	if err != nil {
		t.Fatalf("序列化视图: %v", err)
	}
	text := string(data)
	for _, secret := range []string{"sk-file", "sk-env-account"} {
		if strings.Contains(text, secret) {
			t.Fatalf("安全视图泄漏密钥 %q: %s", secret, text)
		}
	}
	if !strings.Contains(text, `"api_key_set":true`) {
		t.Fatalf("安全视图应保留 api_key_set: %s", text)
	}
}

func TestBareModelOnlyMatchesDefault(t *testing.T) {
	home := t.TempDir()
	writeTestFile(t, home, "providers.json", `{
	  "providers": [{"id": "p", "base_url": "https://x.test", "default_model": "m1", "models": ["m1", "m2"]}]
	}`)
	writeTestFile(t, home, "routing.json", `{
	  "groups": [{"id": "g", "members": ["m2"]}]
	}`)

	_, err := Build(testStartup(home), 1, noEnv, time.Now())
	if err == nil || !strings.Contains(err.Error(), "未知模型") {
		t.Fatalf("裸模型名必须匹配 default_model，得到 %v", err)
	}
}

func TestReloadKeepsOldSnapshot(t *testing.T) {
	home := t.TempDir()
	writeTestFile(t, home, "providers.json", `{
	  "providers": [{"id": "p", "base_url": "https://one.test", "default_model": "m1"}]
	}`)

	mgr, err := NewManager(testStartup(home), noEnv)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	if got := mgr.Current().Providers[0].BaseURL; got != "https://one.test" {
		t.Fatalf("初始配置不对: %q", got)
	}

	writeTestFile(t, home, "providers.json", `{"providers": [{"id": "p", "base_url":`)
	if _, err := mgr.Reload(); err == nil {
		t.Fatal("坏 JSON 应重载失败")
	}
	if mgr.Current().Version != 1 {
		t.Fatalf("失败后版本不应变化: %d", mgr.Current().Version)
	}
	if got := mgr.Current().Providers[0].BaseURL; got != "https://one.test" {
		t.Fatalf("失败后应保留旧快照: %q", got)
	}
	if status := mgr.Status(); status.LastError == "" {
		t.Fatal("状态应记录最近一次错误")
	}

	writeTestFile(t, home, "providers.json", `{
	  "providers": [{"id": "p", "base_url": "https://two.test", "default_model": "m1"}]
	}`)
	next, err := mgr.Reload()
	if err != nil {
		t.Fatalf("合法配置重载失败: %v", err)
	}
	if next.Version != 2 {
		t.Fatalf("成功重载版本应为 2: %d", next.Version)
	}
	if got := mgr.Current().Providers[0].BaseURL; got != "https://two.test" {
		t.Fatalf("新快照应生效: %q", got)
	}
	if status := mgr.Status(); status.LastError != "" {
		t.Fatalf("成功后错误应清空: %q", status.LastError)
	}
}

func TestBuildValidation(t *testing.T) {
	const validProviders = `{"providers": [{"id": "p", "base_url": "https://x.test", "default_model": "m1"}]}`
	cases := []struct {
		name      string
		providers string
		routing   string
		settings  string
		want      string
	}{
		{
			name:      "providers 非法 JSON",
			providers: `{"providers": [`,
			want:      "JSON 解析失败",
		},
		{
			name:      "provider id 重复",
			providers: `{"providers": [{"id": "p", "base_url": "https://x"}, {"id": "p", "base_url": "https://y"}]}`,
			want:      "id 重复",
		},
		{
			name:      "provider id 为空",
			providers: `{"providers": [{"base_url": "https://x"}]}`,
			want:      "id 不能为空",
		},
		{
			name:      "protocol 不合法",
			providers: `{"providers": [{"id": "p", "base_url": "https://x", "protocol": "grpc", "default_model": "m"}]}`,
			want:      "protocol",
		},
		{
			name:      "base_url 为空",
			providers: `{"providers": [{"id": "p", "default_model": "m"}]}`,
			want:      "base_url",
		},
		{
			name:      "缺少模型",
			providers: `{"providers": [{"id": "p", "base_url": "https://x"}]}`,
			want:      "default_model",
		},
		{
			name:      "账号 id 重复",
			providers: `{"providers": [{"id": "p", "base_url": "https://x", "default_model": "m", "accounts": [{"id": "a"}, {"id": "a"}]}]}`,
			want:      "id 重复",
		},
		{
			name:      "账号 id 为空",
			providers: `{"providers": [{"id": "p", "base_url": "https://x", "default_model": "m", "accounts": [{}]}]}`,
			want:      "不能为空",
		},
		{
			name:      "配额为负",
			providers: `{"providers": [{"id": "p", "base_url": "https://x", "default_model": "m", "accounts": [{"id": "a", "daily_token_limit": -1}]}]}`,
			want:      "daily_token_limit",
		},
		{
			name:      "group id 重复",
			providers: validProviders,
			routing:   `{"groups": [{"id": "g", "members": ["p/m1"]}, {"id": "g", "members": ["p/m1"]}]}`,
			want:      "id 重复",
		},
		{
			name:      "strategy 不合法",
			providers: validProviders,
			routing:   `{"groups": [{"id": "g", "strategy": "smart", "members": ["p/m1"]}]}`,
			want:      "strategy",
		},
		{
			name:      "members 为空",
			providers: validProviders,
			routing:   `{"groups": [{"id": "g", "members": []}]}`,
			want:      "members",
		},
		{
			name:      "max_attempts 超上限",
			providers: validProviders,
			routing:   `{"max_attempts": 6}`,
			want:      "max_attempts",
		},
		{
			name:      "cooldown 超上限",
			providers: validProviders,
			routing:   `{"cooldown_seconds": 601}`,
			want:      "cooldown_seconds",
		},
		{
			name:      "cooldown 为负",
			providers: validProviders,
			routing:   `{"cooldown_seconds": -1}`,
			want:      "cooldown_seconds",
		},
		{
			name:      "queue_timeout 为负",
			providers: validProviders,
			routing:   `{"queue_timeout_ms": -1}`,
			want:      "queue_timeout_ms",
		},
		{
			name:      "group 引用未知 provider",
			providers: validProviders,
			routing:   `{"groups": [{"id": "g", "members": ["nope/m"]}]}`,
			want:      "不存在的 provider",
		},
		{
			name:      "group 自引用",
			providers: validProviders,
			routing:   `{"groups": [{"id": "g", "members": ["group/g"]}]}`,
			want:      "自身",
		},
		{
			name:      "group 循环",
			providers: validProviders,
			routing:   `{"groups": [{"id": "g1", "members": ["group/g2"]}, {"id": "g2", "members": ["group/g1"]}]}`,
			want:      "循环引用",
		},
		{
			name:      "group 裸模型未知",
			providers: validProviders,
			routing:   `{"groups": [{"id": "g", "members": ["unknown"]}]}`,
			want:      "未知模型",
		},
		{
			name:      "default 引用未知 provider",
			providers: validProviders,
			routing:   `{"default": {"provider": "nope", "model": "m"}}`,
			want:      "default 引用了不存在的 provider",
		},
		{
			name:      "settings 非法 JSON",
			providers: validProviders,
			settings:  `{"rate_limit":}`,
			want:      "JSON 解析失败",
		},
		{
			name:      "rate_limit 为负",
			providers: validProviders,
			settings:  `{"rate_limit": -1}`,
			want:      "rate_limit",
		},
		{
			name:      "tool_timeout 为 0",
			providers: validProviders,
			settings:  `{"tool_timeout_s": 0}`,
			want:      "tool_timeout_s",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			if tc.providers != "" {
				writeTestFile(t, home, "providers.json", tc.providers)
			}
			if tc.routing != "" {
				writeTestFile(t, home, "routing.json", tc.routing)
			}
			if tc.settings != "" {
				writeTestFile(t, home, "settings.json", tc.settings)
			}
			_, err := Build(testStartup(home), 1, noEnv, time.Now())
			if err == nil {
				t.Fatal("应校验失败")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("错误应包含 %q，得到 %q", tc.want, err.Error())
			}
		})
	}
}

func TestEnvName(t *testing.T) {
	cases := map[string]string{
		"deepseek":   "DEEPSEEK",
		"my-prov.id": "MY_PROV_ID",
		"a b":        "A_B",
	}
	for in, want := range cases {
		if got := EnvName(in); got != want {
			t.Fatalf("EnvName(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestSettingsAndBaseURLValidation 验证 upstream_timeout_s 与 base_url 协议的校验（019）。
func TestSettingsAndBaseURLValidation(t *testing.T) {
	home := t.TempDir()
	// 合法配置
	writeTestFile(t, home, "settings.json", `{"upstream_timeout_s": 60}`)
	if _, err := Build(testStartup(home), 1, noEnv, time.Now()); err != nil {
		t.Fatalf("合法 settings 应通过: %v", err)
	}
	// 越界
	writeTestFile(t, home, "settings.json", `{"upstream_timeout_s": 0}`)
	if _, err := Build(testStartup(home), 1, noEnv, time.Now()); err == nil {
		t.Fatal("upstream_timeout_s=0 应被拒绝")
	}
	writeTestFile(t, home, "settings.json", `{"upstream_timeout_s": 5000}`)
	if _, err := Build(testStartup(home), 1, noEnv, time.Now()); err == nil {
		t.Fatal("upstream_timeout_s>3600 应被拒绝")
	}
	// base_url 协议
	writeTestFile(t, home, "settings.json", `{}`)
	writeTestFile(t, home, "providers.json", `{"providers":[{"id":"p","base_url":"ftp://x/v1","default_model":"m","models":["m"]}]}`)
	if _, err := Build(testStartup(home), 1, noEnv, time.Now()); err == nil {
		t.Fatal("非 http(s) base_url 应被拒绝")
	}
}
