package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gqy/internal/gateway"
	"gqy/internal/provider"
)

func newModelEnv(t *testing.T) *testEnv {
	t.Helper()
	env := newTestServer(t, "", nil)
	env.srv.gw = gateway.New(provider.NewRegistry(), nil, env.home)
	return env
}

// TestActiveModelEndpoints 验证全局默认模型的读写、校验与持久化（HR5）。
func TestActiveModelEndpoints(t *testing.T) {
	env := newModelEnv(t)

	// 初始：使用配置默认。
	rec := env.request(http.MethodGet, "/api/model/active", nil, nil)
	var got activeModelResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Source != "config" || got.Model != "p/m1" {
		t.Fatalf("初始状态 = %+v", got)
	}

	// 非法模型被拒绝。
	rec = env.request(http.MethodPut, "/api/model/active", strings.NewReader(`{"model":"nope/none"}`), nil)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "model_not_found") {
		t.Fatalf("非法模型应 400 model_not_found，实际 %d %s", rec.Code, rec.Body.String())
	}

	// 合法切换：GET 反映 runtime。
	rec = env.request(http.MethodPut, "/api/model/active", strings.NewReader(`{"model":"p/m2"}`), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("切换失败: %d %s", rec.Code, rec.Body.String())
	}
	rec = env.request(http.MethodGet, "/api/model/active", nil, nil)
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Source != "runtime" || got.Model != "p/m2" {
		t.Fatalf("切换后 = %+v", got)
	}

	// 持久化：routing.json 的 default 被写回，配置版本自增。
	before := env.mgr.Current().Version
	rec = env.request(http.MethodPut, "/api/model/active", strings.NewReader(`{"model":"p/m2","persist":true}`), nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"persisted":true`) {
		t.Fatalf("持久化失败: %d %s", rec.Code, rec.Body.String())
	}
	raw, err := os.ReadFile(filepath.Join(env.home, "routing.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Default struct {
			Provider string `json:"provider"`
			Model    string `json:"model"`
		} `json:"default"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Default.Provider != "p" || doc.Default.Model != "m2" {
		t.Fatalf("routing.json default 未更新: %s", string(raw))
	}
	if env.mgr.Current().Version <= before {
		t.Fatalf("持久化后应重载配置，version=%d", env.mgr.Current().Version)
	}

	// 清除覆盖：回落配置默认（此时配置默认已是 m2）。
	rec = env.request(http.MethodPut, "/api/model/active", strings.NewReader(`{"model":""}`), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("清除失败: %d", rec.Code)
	}
	rec = env.request(http.MethodGet, "/api/model/active", nil, nil)
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Source != "config" || got.Model != "p/m2" {
		t.Fatalf("清除后 = %+v", got)
	}
}

// TestEventsStreamPushes 验证 SSE 长连接能收到 ready 与业务事件（HR7）。
func TestEventsStreamPushes(t *testing.T) {
	env := newModelEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, "/api/events", nil).WithContext(ctx)
	req.RemoteAddr = "127.0.0.1:12345"
	rec := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		env.srv.Handler().ServeHTTP(rec, req)
		close(done)
	}()

	time.Sleep(50 * time.Millisecond)
	env.srv.Publish("model", map[string]any{"model": "p/m2"})
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("SSE 处理函数未在取消后退出")
	}

	body := rec.Body.String()
	if !strings.Contains(body, `"type":"ready"`) {
		t.Fatalf("应收到 ready 帧: %s", body)
	}
	if !strings.Contains(body, `"type":"model"`) || !strings.Contains(body, "p/m2") {
		t.Fatalf("应收到 model 事件: %s", body)
	}
}
