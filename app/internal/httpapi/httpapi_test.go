package httpapi

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gqy/internal/config"
	"gqy/internal/gateway"
	"gqy/internal/goal"
	"gqy/internal/session"
)

const (
	testProviders = `{
	  "providers": [{
	    "id": "p",
	    "display_name": "P",
	    "base_url": "https://x.test/v1",
	    "default_model": "m1",
	    "models": ["m1", "m2"],
	    "accounts": [{"id": "a1", "api_key": "test-key-1", "daily_token_limit": 100}]
	  }]
	}`
	testRouting = `{
	  "default": {"provider": "p", "model": "m1"},
	  "groups": [{"id": "g", "name": "日常", "members": ["p/m1"]}]
	}`
)

type testEnv struct {
	srv  *Server
	mgr  *config.Manager
	home string
}

func newTestServer(t *testing.T, authToken string, extra map[string]string) *testEnv {
	t.Helper()
	home := t.TempDir()
	files := map[string]string{
		"providers.json": testProviders,
		"routing.json":   testRouting,
		"settings.json":  `{}`,
	}
	for name, content := range extra {
		files[name] = content
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(home, name), []byte(content), 0o600); err != nil {
			t.Fatalf("写入 %s: %v", name, err)
		}
	}

	startup := config.Startup{
		Home:      home,
		Host:      config.DefaultHost,
		Port:      config.DefaultPort,
		AuthToken: authToken,
		Workspace: home,
		APIBase:   config.DefaultAPIBase,
		Model:     config.DefaultModel,
	}
	mgr, err := config.NewManager(startup, func(string) string { return "" })
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return &testEnv{srv: New(mgr, nil, logger), mgr: mgr, home: home}
}

func (e *testEnv) request(method, path string, body io.Reader, headers map[string]string) *httptest.ResponseRecorder {
	return e.requestFrom("127.0.0.1:12345", method, path, body, headers)
}

func (e *testEnv) requestFrom(remote, method, path string, body io.Reader, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, body)
	req.RemoteAddr = remote
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	e.srv.Handler().ServeHTTP(rec, req)
	return rec
}

func decodeJSON(t *testing.T, rec *httptest.ResponseRecorder, out any) {
	t.Helper()
	if err := json.Unmarshal(rec.Body.Bytes(), out); err != nil {
		t.Fatalf("解析响应失败: %v\n%s", err, rec.Body.String())
	}
}

func TestHealthAndModels(t *testing.T) {
	e := newTestServer(t, "", nil)

	rec := e.request(http.MethodGet, "/healthz", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("/healthz = %d: %s", rec.Code, rec.Body.String())
	}
	var health map[string]any
	decodeJSON(t, rec, &health)
	if health["status"] != "ok" {
		t.Fatalf("健康状态不对: %v", health)
	}
	if rec.Header().Get("X-Request-ID") == "" {
		t.Fatal("应返回 X-Request-ID")
	}
	if rec.Header().Get("Content-Security-Policy") == "" || rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("安全响应头缺失: %+v", rec.Header())
	}

	rec = e.request(http.MethodGet, "/v1/models", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("/v1/models = %d: %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "test-key-1") {
		t.Fatal("模型列表不应包含密钥")
	}
	var models struct {
		Object string               `json:"object"`
		Data   []gateway.ModelEntry `json:"data"`
	}
	decodeJSON(t, rec, &models)
	if models.Object != "list" {
		t.Fatalf("object = %q", models.Object)
	}
	want := []string{"p/m1", "p/m2", "group/g"}
	if len(models.Data) != len(want) {
		t.Fatalf("模型数 = %d, want %d: %+v", len(models.Data), len(want), models.Data)
	}
	for i, id := range want {
		if models.Data[i].ID != id {
			t.Fatalf("第 %d 个模型 = %q, want %q", i, models.Data[i].ID, id)
		}
	}
	if !models.Data[0].Default {
		t.Fatal("默认模型应带 default 标记")
	}

	rec = e.request(http.MethodPost, "/v1/models", nil, nil)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST /v1/models 应为 405，得到 %d", rec.Code)
	}
}

func TestAuth(t *testing.T) {
	e := newTestServer(t, "s3cret", nil)

	rec := e.request(http.MethodGet, "/v1/models", nil, nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("无令牌 = %d, want 401", rec.Code)
	}
	if rec.Header().Get("WWW-Authenticate") == "" {
		t.Fatal("401 应带 WWW-Authenticate")
	}

	rec = e.request(http.MethodGet, "/v1/models", nil, map[string]string{
		"Authorization": "Bearer s3cret",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("Bearer 令牌 = %d: %s", rec.Code, rec.Body.String())
	}

	rec = e.request(http.MethodGet, "/v1/models", nil, map[string]string{
		"Authorization": "Bearer wrong",
	})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("错误令牌 = %d, want 401", rec.Code)
	}

	rec = e.request(http.MethodPost, "/api/auth", strings.NewReader(`{"token":"s3cret"}`), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("/api/auth = %d: %s", rec.Code, rec.Body.String())
	}
	var session *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == cookieName {
			session = c
		}
	}
	if session == nil {
		t.Fatal("登录成功应种 cookie")
	}
	if !session.HttpOnly || session.SameSite != http.SameSiteLaxMode {
		t.Fatalf("cookie 安全属性不对: %+v", session)
	}
	if session.Secure {
		t.Fatal("普通 HTTP 不应设置 Secure")
	}

	rec = e.request(http.MethodGet, "/v1/models", nil, map[string]string{
		"Cookie": cookieName + "=" + session.Value,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("cookie 令牌 = %d: %s", rec.Code, rec.Body.String())
	}

	rec = e.request(http.MethodPost, "/api/auth", strings.NewReader(`{"token":"wrong"}`), nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("错误登录 = %d, want 401", rec.Code)
	}
	if len(rec.Result().Cookies()) != 0 {
		t.Fatal("登录失败不应种 cookie")
	}

	rec = e.request(http.MethodGet, "/healthz", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("/healthz 不应要求鉴权: %d", rec.Code)
	}
}

func TestAuthDisabledOnlyLoopback(t *testing.T) {
	e := newTestServer(t, "", nil)

	rec := e.request(http.MethodGet, "/v1/models", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("本机请求应放行: %d %s", rec.Code, rec.Body.String())
	}

	rec = e.requestFrom("192.0.2.10:4321", http.MethodGet, "/v1/models", nil, nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("非本机请求应拒绝: %d", rec.Code)
	}
}

func TestRateLimit(t *testing.T) {
	e := newTestServer(t, "", map[string]string{
		"settings.json": `{"rate_limit": 2}`,
	})
	fixed := time.Date(2026, 11, 2, 12, 0, 30, 0, time.UTC)
	e.srv.now = func() time.Time { return fixed }

	for i := 0; i < 2; i++ {
		rec := e.request(http.MethodGet, "/v1/models", nil, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("第 %d 次请求 = %d: %s", i+1, rec.Code, rec.Body.String())
		}
	}
	rec := e.request(http.MethodGet, "/v1/models", nil, nil)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("第 3 次请求 = %d, want 429: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Retry-After"); got != "30" {
		t.Fatalf("Retry-After = %q, want 30", got)
	}

	rec = e.request(http.MethodGet, "/healthz", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("/healthz 不应被限流: %d", rec.Code)
	}

	fixed = fixed.Add(time.Minute)
	rec = e.request(http.MethodGet, "/v1/models", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("新窗口应恢复: %d", rec.Code)
	}
}

func TestReloadEndpoint(t *testing.T) {
	e := newTestServer(t, "", nil)

	rec := e.request(http.MethodPost, "/api/config/reload", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("首次重载 = %d: %s", rec.Code, rec.Body.String())
	}
	var okBody struct {
		Version uint64 `json:"version"`
	}
	decodeJSON(t, rec, &okBody)
	if okBody.Version != 2 {
		t.Fatalf("重载后版本 = %d, want 2", okBody.Version)
	}

	if err := os.WriteFile(filepath.Join(e.home, "providers.json"), []byte(`{"providers":[`), 0o600); err != nil {
		t.Fatal(err)
	}
	rec = e.request(http.MethodPost, "/api/config/reload", nil, nil)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("坏配置 = %d, want 422: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "invalid_config") {
		t.Fatalf("错误码不对: %s", rec.Body.String())
	}

	rec = e.request(http.MethodGet, "/api/config", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("/api/config = %d", rec.Code)
	}
	var cfgBody struct {
		Version uint64 `json:"version"`
	}
	decodeJSON(t, rec, &cfgBody)
	if cfgBody.Version != 2 {
		t.Fatalf("失败后版本不应变化: %d", cfgBody.Version)
	}

	valid := strings.Replace(testProviders, `"display_name": "P"`, `"display_name": "P2"`, 1)
	if err := os.WriteFile(filepath.Join(e.home, "providers.json"), []byte(valid), 0o600); err != nil {
		t.Fatal(err)
	}
	rec = e.request(http.MethodPost, "/api/config/reload", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("合法配置重载 = %d: %s", rec.Code, rec.Body.String())
	}
	decodeJSON(t, rec, &okBody)
	if okBody.Version != 3 {
		t.Fatalf("重载后版本 = %d, want 3", okBody.Version)
	}
}

func TestStatusAndConfigDoNotLeakSecrets(t *testing.T) {
	e := newTestServer(t, "", nil)

	rec := e.request(http.MethodGet, "/api/gateway/status", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("状态接口 = %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if strings.Contains(body, "test-key-1") {
		t.Fatalf("状态接口泄漏密钥: %s", body)
	}
	if !strings.Contains(body, `"api_key_set":true`) {
		t.Fatalf("状态接口应包含 api_key_set: %s", body)
	}
	var status statusResponse
	decodeJSON(t, rec, &status)
	if len(status.Accounts) != 1 {
		t.Fatalf("账号数 = %d, want 1", len(status.Accounts))
	}
	if status.Accounts[0].TokenLimit != 100 || status.Accounts[0].QuotaExhausted || status.Accounts[0].CooldownRemainingS != 0 {
		t.Fatalf("账号状态字段不对: %+v", status.Accounts[0])
	}
	if status.Config.Version != 1 {
		t.Fatalf("状态版本 = %d, want 1", status.Config.Version)
	}

	rec = e.request(http.MethodGet, "/api/config", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("/api/config = %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "test-key-1") {
		t.Fatalf("/api/config 泄漏密钥: %s", rec.Body.String())
	}
}

// TestAgentChatQueuesWhenGoalActiveAndRunning 验证主线抢占：goal active 且会话 loop 在跑时，
// /api/chat 的消息进支线队列并返回 queued，不打断主线（013）。
func TestAgentChatQueuesWhenGoalActiveAndRunning(t *testing.T) {
	env := newTestServer(t, "", nil)
	env.srv.SetGoals(goal.NewManager())
	env.srv.gw = &gateway.Router{} // 排队路径不会用到网关，仅为通过非 nil 检查

	g := env.srv.goals.Create("主线目标", 5)
	store := session.NewStore(env.home)
	sess, err := store.Create("")
	if err != nil {
		t.Fatal(err)
	}
	env.srv.setSessionRunning(sess.ID(), true)
	defer env.srv.setSessionRunning(sess.ID(), false)

	body := `{"session_id":"` + sess.ID() + `","model":"p/m1","messages":[{"role":"user","content":"插一句支线"}]}`
	req := httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(body))
	req.RemoteAddr = "127.0.0.1:12345"
	rec := httptest.NewRecorder()
	env.srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"queued":true`) {
		t.Fatalf("应返回 queued:true，实际 %s", rec.Body.String())
	}
	msgs := env.srv.goals.TakeQueued(g.ID)
	if len(msgs) != 1 || msgs[0] != "插一句支线" {
		t.Fatalf("支线队列 = %v", msgs)
	}

	// 同一 requestID 重放应幂等（不重复入队）。
	req2 := httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(body))
	req2.RemoteAddr = "127.0.0.1:12345"
	req2.Header.Set("X-Request-ID", "same-id")
	env.srv.Handler().ServeHTTP(httptest.NewRecorder(), req2)
	req3 := httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(body))
	req3.RemoteAddr = "127.0.0.1:12345"
	req3.Header.Set("X-Request-ID", "same-id")
	env.srv.Handler().ServeHTTP(httptest.NewRecorder(), req3)
	if msgs := env.srv.goals.TakeQueued(g.ID); len(msgs) != 1 {
		t.Fatalf("同一 requestID 重复请求应只入队一次，实际 %v", msgs)
	}
}

// TestAuthRequiredOnAllProtectedPaths 验证「未设置令牌 + 非本机」对所有受保护端点一律 403（020）。
func TestAuthRequiredOnAllProtectedPaths(t *testing.T) {
	env := newTestServer(t, "", nil)
	paths := []string{
		"/v1/models", "/v1/chat/completions", "/v1/messages", "/v1beta/models/x:generateContent",
		"/api/chat", "/api/config", "/api/config/reload", "/api/gateway/status",
		"/api/inhg", "/api/plugins", "/api/goal",
	}
	for _, p := range paths {
		req := httptest.NewRequest(http.MethodGet, p, nil)
		req.RemoteAddr = "192.0.2.10:4321" // 非回环
		rec := httptest.NewRecorder()
		env.srv.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("%s 应为 403（未设令牌且非本机），实际 %d", p, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "auth_required") {
			t.Fatalf("%s 错误体应为 auth_required，实际 %s", p, rec.Body.String())
		}
	}
}

// TestTenantHeaderValidation 验证 X-GQY-Tenant 头解析与校验（MT2）。
func TestTenantHeaderValidation(t *testing.T) {
	env := newTestServer(t, "", nil)
	// 合法租户名：放行（状态页 200）。
	rec := env.request(http.MethodGet, "/api/gateway/status", nil, map[string]string{"X-GQY-Tenant": "task-001.draw"})
	if rec.Code != http.StatusOK {
		t.Fatalf("合法租户头应放行，实际 %d", rec.Code)
	}
	// 非法字符：400 invalid_tenant。
	rec = env.request(http.MethodGet, "/api/gateway/status", nil, map[string]string{"X-GQY-Tenant": "bad tenant!"})
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "invalid_tenant") {
		t.Fatalf("非法租户头应 400 invalid_tenant，实际 %d %s", rec.Code, rec.Body.String())
	}
	// X-GQY-Task 作为别名。
	rec = env.request(http.MethodGet, "/api/gateway/status", nil, map[string]string{"X-GQY-Task": "team_a"})
	if rec.Code != http.StatusOK {
		t.Fatalf("X-GQY-Task 别名应放行，实际 %d", rec.Code)
	}
}
