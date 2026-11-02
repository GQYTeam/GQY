package gateway

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"gqy/internal/config"
	"gqy/internal/provider"
)

type fakeAdapter struct {
	mu       sync.Mutex
	calls    []string
	chatFn   func(ctx context.Context, ep provider.Endpoint, req *provider.Request) (*provider.Response, error)
	streamFn func(ctx context.Context, ep provider.Endpoint, req *provider.Request, emit func(provider.Event) error) (*provider.Response, error)
}

func (f *fakeAdapter) Protocol() string { return "openai-chat" }

func (f *fakeAdapter) Chat(ctx context.Context, ep provider.Endpoint, req *provider.Request) (*provider.Response, error) {
	f.mu.Lock()
	f.calls = append(f.calls, ep.ProviderID)
	fn := f.chatFn
	f.mu.Unlock()
	if fn != nil {
		return fn(ctx, ep, req)
	}
	return &provider.Response{Text: "ok", StopReason: provider.StopEndTurn}, nil
}

func (f *fakeAdapter) Stream(ctx context.Context, ep provider.Endpoint, req *provider.Request, emit func(provider.Event) error) (*provider.Response, error) {
	f.mu.Lock()
	f.calls = append(f.calls, ep.ProviderID)
	fn := f.streamFn
	f.mu.Unlock()
	if fn != nil {
		return fn(ctx, ep, req, emit)
	}
	if err := emit(provider.Event{Type: provider.EventText, Delta: "ok"}); err != nil {
		return nil, err
	}
	if err := emit(provider.Event{Type: provider.EventDone, StopReason: provider.StopEndTurn}); err != nil {
		return nil, err
	}
	return &provider.Response{Text: "ok", StopReason: provider.StopEndTurn}, nil
}

func (f *fakeAdapter) callList() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

func testProvider(id, model string, accounts ...config.Account) config.Provider {
	return config.Provider{
		ID:             id,
		DisplayName:    id,
		BaseURL:        "https://" + id + ".test/v1",
		Protocol:       config.ProtocolOpenAIChat,
		DefaultModel:   model,
		Models:         []string{model},
		Accounts:       accounts,
		MaxConcurrency: 2,
	}
}

func testAccount(id string) config.Account {
	return config.Account{ID: id, APIKey: "key-" + id}
}

func testRouterSnapshot(providers []config.Provider, groups []config.Group) *config.Snapshot {
	defaultRef := &config.ModelRef{Provider: providers[0].ID, Model: providers[0].DefaultModel}
	return &config.Snapshot{
		Providers: providers,
		Routing: config.Routing{
			Default:         defaultRef,
			Groups:          groups,
			MaxAttempts:     3,
			CooldownSeconds: 30,
			QueueTimeoutMS:  1500,
		},
	}
}

func newTestRouter(t *testing.T, home string, fake *fakeAdapter) *Router {
	t.Helper()
	registry := provider.NewRegistry(fake)
	r := New(registry, nil, home)
	r.now = func() time.Time { return time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC) }
	return r
}

func TestCompleteFailoverAndCooldown(t *testing.T) {
	fake := &fakeAdapter{chatFn: func(_ context.Context, ep provider.Endpoint, _ *provider.Request) (*provider.Response, error) {
		if ep.ProviderID == "p1" {
			return nil, provider.NewError(provider.CodeRateLimited, true, 429, "slow down")
		}
		return &provider.Response{Text: "ok", StopReason: provider.StopEndTurn}, nil
	}}
	snap := testRouterSnapshot([]config.Provider{
		testProvider("p1", "m1", testAccount("a1")),
		testProvider("p2", "m2", testAccount("a2")),
	}, []config.Group{{ID: "g", Strategy: config.StrategyOrder, Members: []string{"p1/m1", "p2/m2"}}})

	home := t.TempDir()
	r := newTestRouter(t, home, fake)
	res, err := r.Complete(context.Background(), snap, &provider.Request{Model: "group/g", Messages: []provider.Message{{Role: provider.RoleUser, Content: "hi"}}}, Meta{RequestID: "req-1"})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if res.Provider != "p2" || res.Account != "a2" || res.Attempts != 2 {
		t.Fatalf("结果 = %+v", res)
	}
	if got := fake.callList(); len(got) != 2 || got[0] != "p1" || got[1] != "p2" {
		t.Fatalf("调用顺序 = %v", got)
	}

	statuses := r.AccountStatuses(snap)
	if statuses[0].Provider != "p1" || statuses[0].Failures != 1 || statuses[0].CooldownRemainingS == 0 {
		t.Fatalf("p1 状态 = %+v", statuses[0])
	}
	if statuses[1].Provider != "p2" || statuses[1].Calls != 1 || !statuses[1].Healthy {
		t.Fatalf("p2 状态 = %+v", statuses[1])
	}

	// 冷却期内第二次请求直接落 p2。
	res, err = r.Complete(context.Background(), snap, &provider.Request{Model: "group/g"}, Meta{RequestID: "req-2"})
	if err != nil {
		t.Fatalf("第二次 Complete: %v", err)
	}
	if res.Provider != "p2" || res.Attempts != 1 {
		t.Fatalf("冷却后结果 = %+v", res)
	}

	// 请求日志包含一行完整字段。
	r.Close() // 冲刷异步请求日志（MT5）
	data, err := os.ReadFile(filepath.Join(home, "gateway", "requests.jsonl"))
	if err != nil {
		t.Fatalf("读请求日志: %v", err)
	}
	lines := 0
	for _, line := range splitLines(data) {
		if line == "" {
			continue
		}
		lines++
		var entry RequestLogEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("日志行不是 JSON: %v", err)
		}
		if entry.RequestID == "" || entry.Status == "" || entry.HTTPStatus == 0 {
			t.Fatalf("日志字段不完整: %+v", entry)
		}
	}
	if lines != 2 {
		t.Fatalf("日志行数 = %d, want 2", lines)
	}
}

func TestCompleteNonRetryable(t *testing.T) {
	fake := &fakeAdapter{chatFn: func(_ context.Context, ep provider.Endpoint, _ *provider.Request) (*provider.Response, error) {
		return nil, provider.NewError(provider.CodeInvalidRequest, false, 400, "bad")
	}}
	snap := testRouterSnapshot([]config.Provider{
		testProvider("p1", "m1", testAccount("a1")),
		testProvider("p2", "m2", testAccount("a2")),
	}, nil)
	r := newTestRouter(t, "", fake)

	_, err := r.Complete(context.Background(), snap, &provider.Request{Model: "p1/m1"}, Meta{})
	pe := provider.AsError(err)
	if pe.Code != provider.CodeInvalidRequest {
		t.Fatalf("错误 = %+v", pe)
	}
	if got := fake.callList(); len(got) != 1 || got[0] != "p1" {
		t.Fatalf("4xx 不应重试: %v", got)
	}
	if statuses := r.AccountStatuses(snap); statuses[0].CooldownRemainingS != 0 {
		t.Fatalf("4xx 不应冷却: %+v", statuses[0])
	}
}

func TestCompleteExhausted(t *testing.T) {
	fake := &fakeAdapter{chatFn: func(_ context.Context, ep provider.Endpoint, _ *provider.Request) (*provider.Response, error) {
		return nil, provider.NewError(provider.CodeProviderError, true, 502, "boom")
	}}
	snap := testRouterSnapshot([]config.Provider{
		testProvider("p1", "m1", testAccount("a1")),
		testProvider("p2", "m2", testAccount("a2")),
	}, []config.Group{{ID: "g", Strategy: config.StrategyOrder, Members: []string{"p1/m1", "p2/m2"}}})
	snap.Routing.MaxAttempts = 2
	r := newTestRouter(t, "", fake)

	_, err := r.Complete(context.Background(), snap, &provider.Request{Model: "group/g"}, Meta{})
	pe := provider.AsError(err)
	if pe.Code != provider.CodeNoCandidate {
		t.Fatalf("错误 = %+v", pe)
	}
	if got := fake.callList(); len(got) != 2 {
		t.Fatalf("尝试次数 = %d, want 2", len(got))
	}
}

func TestRotateStrategy(t *testing.T) {
	fake := &fakeAdapter{}
	snap := testRouterSnapshot([]config.Provider{
		testProvider("p1", "m1", testAccount("a1")),
		testProvider("p2", "m2", testAccount("a2")),
	}, []config.Group{{ID: "g", Strategy: config.StrategyRotate, Members: []string{"p1/m1", "p2/m2"}}})
	r := newTestRouter(t, "", fake)

	first, err := r.Complete(context.Background(), snap, &provider.Request{Model: "group/g"}, Meta{})
	if err != nil || first.Provider != "p1" {
		t.Fatalf("第一次 = %+v, %v", first, err)
	}
	second, err := r.Complete(context.Background(), snap, &provider.Request{Model: "group/g"}, Meta{})
	if err != nil || second.Provider != "p2" {
		t.Fatalf("第二次 = %+v, %v", second, err)
	}
}

func TestUsageStrategy(t *testing.T) {
	fake := &fakeAdapter{}
	snap := testRouterSnapshot([]config.Provider{
		testProvider("p1", "m1", testAccount("a1")),
		testProvider("p2", "m2", testAccount("a2")),
	}, []config.Group{{ID: "g", Strategy: config.StrategyUsage, Members: []string{"p1/m1", "p2/m2"}}})
	r := newTestRouter(t, "", fake)

	first, err := r.Complete(context.Background(), snap, &provider.Request{Model: "group/g"}, Meta{})
	if err != nil || first.Provider != "p1" {
		t.Fatalf("第一次 = %+v, %v", first, err)
	}
	second, err := r.Complete(context.Background(), snap, &provider.Request{Model: "group/g"}, Meta{})
	if err != nil || second.Provider != "p2" {
		t.Fatalf("第二次 = %+v, %v", second, err)
	}
}

func TestSessionAffinity(t *testing.T) {
	fake := &fakeAdapter{}
	snap := testRouterSnapshot([]config.Provider{
		testProvider("p1", "m1", testAccount("a1")),
		testProvider("p2", "m2", testAccount("a2")),
		testProvider("p3", "m3", testAccount("a3")),
	}, []config.Group{{ID: "g", Strategy: config.StrategyRotate, Members: []string{"p1/m1", "p2/m2", "p3/m3"}}})
	r := newTestRouter(t, "", fake)

	res, err := r.Complete(context.Background(), snap, &provider.Request{Model: "group/g"}, Meta{SessionID: "s1"})
	if err != nil || res.Provider != "p1" {
		t.Fatalf("s1 第一次 = %+v, %v", res, err)
	}
	res, err = r.Complete(context.Background(), snap, &provider.Request{Model: "group/g"}, Meta{SessionID: "s2"})
	if err != nil || res.Provider != "p2" {
		t.Fatalf("s2 第一次 = %+v, %v", res, err)
	}
	// 轮转指针已到 p3，但 s1 的亲和应把 p1 排到最前。
	res, err = r.Complete(context.Background(), snap, &provider.Request{Model: "group/g"}, Meta{SessionID: "s1"})
	if err != nil || res.Provider != "p1" {
		t.Fatalf("s1 第二次应亲和 p1: %+v, %v", res, err)
	}
}

func TestQuotaExhaustedSkipsAccount(t *testing.T) {
	fake := &fakeAdapter{chatFn: func(_ context.Context, ep provider.Endpoint, _ *provider.Request) (*provider.Response, error) {
		return &provider.Response{Text: "ok", Usage: provider.Usage{PromptTokens: 3, CompletionTokens: 2}}, nil
	}}
	p1 := testProvider("p1", "m1", config.Account{ID: "a1", APIKey: "k1", DailyTokenLimit: 1})
	p2 := testProvider("p2", "m2", testAccount("a2"))
	snap := testRouterSnapshot([]config.Provider{p1, p2}, []config.Group{
		{ID: "g", Strategy: config.StrategyOrder, Members: []string{"p1/m1", "p2/m2"}},
	})
	r := newTestRouter(t, "", fake)

	first, err := r.Complete(context.Background(), snap, &provider.Request{Model: "group/g"}, Meta{})
	if err != nil || first.Provider != "p1" {
		t.Fatalf("第一次 = %+v, %v", first, err)
	}
	second, err := r.Complete(context.Background(), snap, &provider.Request{Model: "group/g", Messages: nil}, Meta{})
	if err != nil {
		t.Fatalf("第二次: %v", err)
	}
	if second.Provider != "p2" {
		t.Fatalf("配额耗尽后应转到 p2: %+v", second)
	}
	statuses := r.AccountStatuses(snap)
	if !statuses[0].QuotaExhausted || statuses[0].Healthy {
		t.Fatalf("p1 应配额耗尽: %+v", statuses[0])
	}
}

func TestStreamFailoverBeforeFirstEvent(t *testing.T) {
	fake := &fakeAdapter{streamFn: func(_ context.Context, ep provider.Endpoint, _ *provider.Request, emit func(provider.Event) error) (*provider.Response, error) {
		if ep.ProviderID == "p1" {
			return nil, provider.NewError(provider.CodeConnectionError, true, 502, "dial failed")
		}
		if err := emit(provider.Event{Type: provider.EventText, Delta: "hello"}); err != nil {
			return nil, err
		}
		if err := emit(provider.Event{Type: provider.EventDone, StopReason: provider.StopEndTurn}); err != nil {
			return nil, err
		}
		return &provider.Response{Text: "hello", StopReason: provider.StopEndTurn}, nil
	}}
	snap := testRouterSnapshot([]config.Provider{
		testProvider("p1", "m1", testAccount("a1")),
		testProvider("p2", "m2", testAccount("a2")),
	}, []config.Group{{ID: "g", Strategy: config.StrategyOrder, Members: []string{"p1/m1", "p2/m2"}}})
	r := newTestRouter(t, "", fake)

	var events []provider.Event
	res, err := r.Stream(context.Background(), snap, &provider.Request{Model: "group/g"}, Meta{}, func(ev provider.Event) error {
		events = append(events, ev)
		return nil
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if res.Provider != "p2" || len(events) != 2 || events[0].Delta != "hello" {
		t.Fatalf("结果 = %+v, events=%+v", res, events)
	}
}

func TestStreamNoFailoverAfterStart(t *testing.T) {
	fake := &fakeAdapter{streamFn: func(_ context.Context, ep provider.Endpoint, _ *provider.Request, emit func(provider.Event) error) (*provider.Response, error) {
		if err := emit(provider.Event{Type: provider.EventText, Delta: "half"}); err != nil {
			return nil, err
		}
		return nil, provider.NewError(provider.CodeProviderError, true, 502, "boom")
	}}
	snap := testRouterSnapshot([]config.Provider{
		testProvider("p1", "m1", testAccount("a1")),
		testProvider("p2", "m2", testAccount("a2")),
	}, nil)
	r := newTestRouter(t, "", fake)

	_, err := r.Stream(context.Background(), snap, &provider.Request{Model: "p1/m1"}, Meta{}, func(provider.Event) error { return nil })
	if err == nil {
		t.Fatal("已开始输出后应返回错误")
	}
	if got := fake.callList(); len(got) != 1 {
		t.Fatalf("已开始输出后不应转移: %v", got)
	}
}

func TestQueueTimeout(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	fake := &fakeAdapter{chatFn: func(_ context.Context, ep provider.Endpoint, _ *provider.Request) (*provider.Response, error) {
		select {
		case <-started:
		default:
			close(started)
		}
		<-release
		return &provider.Response{Text: "ok"}, nil
	}}
	p := testProvider("p1", "m1", testAccount("a1"))
	p.MaxConcurrency = 1
	snap := testRouterSnapshot([]config.Provider{p}, nil)
	snap.Routing.QueueTimeoutMS = 20
	r := newTestRouter(t, "", fake)

	done := make(chan error, 1)
	go func() {
		_, err := r.Complete(context.Background(), snap, &provider.Request{Model: "p1/m1"}, Meta{})
		done <- err
	}()
	<-started

	_, err := r.Complete(context.Background(), snap, &provider.Request{Model: "p1/m1"}, Meta{})
	pe := provider.AsError(err)
	if pe.Code != provider.CodeQueueTimeout {
		t.Fatalf("错误 = %+v", pe)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("第一个请求: %v", err)
	}
}

func TestUnregisteredProtocolHasNoCandidate(t *testing.T) {
	p := testProvider("p1", "m1", testAccount("a1"))
	p.Protocol = config.ProtocolAnthropic
	snap := testRouterSnapshot([]config.Provider{p}, nil)
	r := newTestRouter(t, "", &fakeAdapter{})

	_, err := r.Complete(context.Background(), snap, &provider.Request{Model: "p1/m1"}, Meta{})
	pe := provider.AsError(err)
	if pe.Code != provider.CodeNoCandidate {
		t.Fatalf("错误 = %+v", pe)
	}
}

func TestNoCandidatesUnknownModel(t *testing.T) {
	snap := testRouterSnapshot([]config.Provider{testProvider("p1", "m1", testAccount("a1"))}, nil)
	r := newTestRouter(t, "", &fakeAdapter{})
	_, err := r.Complete(context.Background(), snap, &provider.Request{Model: "nope"}, Meta{})
	pe := provider.AsError(err)
	if pe.Code != provider.CodeModelNotFound {
		t.Fatalf("错误 = %v / %+v", err, pe)
	}
}

func splitLines(data []byte) []string {
	out := make([]string, 0)
	start := 0
	for i, b := range data {
		if b == '\n' {
			out = append(out, string(data[start:i]))
			start = i + 1
		}
	}
	if start < len(data) {
		out = append(out, string(data[start:]))
	}
	return out
}

// TestUpstreamTimeoutComplete 验证上游超时由 settings.upstream_timeout_s 控制并归一为 upstream_timeout（019）。
func TestUpstreamTimeoutComplete(t *testing.T) {
	fake := &fakeAdapter{chatFn: func(ctx context.Context, _ provider.Endpoint, _ *provider.Request) (*provider.Response, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	snap := testRouterSnapshot([]config.Provider{testProvider("p1", "m1", testAccount("a1"))}, nil)
	snap.Settings.UpstreamTimeoutS = 1

	r := newTestRouter(t, t.TempDir(), fake)
	start := time.Now()
	_, err := r.Complete(context.Background(), snap, &provider.Request{Model: "p1/m1"}, Meta{RequestID: "req-timeout"})
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("上游卡死应超时返回错误")
	}
	pe := provider.AsError(err)
	if pe.Code != provider.CodeUpstreamTimeout || !pe.Retryable {
		t.Fatalf("错误 = %+v，期望 upstream_timeout 且可重试", pe)
	}
	if elapsed > 3*time.Second {
		t.Fatalf("超时应在 ~1s 生效，实际 %v", elapsed)
	}
}

// TestRequestLogDoesNotLeakAPIKey 验证请求日志只有元数据、不含密钥（020）。
func TestRequestLogDoesNotLeakAPIKey(t *testing.T) {
	const secret = "sk-log-test-secret-123456"
	acct := config.Account{ID: "a1", APIKey: secret}
	snap := testRouterSnapshot([]config.Provider{testProvider("p1", "m1", acct)}, nil)
	fake := &fakeAdapter{}
	home := t.TempDir()
	r := newTestRouter(t, home, fake)

	if _, err := r.Complete(context.Background(), snap, &provider.Request{Model: "p1/m1"}, Meta{RequestID: "req-log"}); err != nil {
		t.Fatal(err)
	}
	r.Close() // 冲刷异步请求日志（MT5）
	data, err := os.ReadFile(filepath.Join(home, "gateway", "requests.jsonl"))
	if err != nil {
		t.Fatalf("请求日志应存在: %v", err)
	}
	if strings.Contains(string(data), secret) {
		t.Fatalf("请求日志不应包含 api_key: %s", string(data))
	}
}

// TestStreamIdleTimeout 验证流式空闲超时：上游发一帧后卡住，idle 到期返回 upstream_timeout（022）。
func TestStreamIdleTimeout(t *testing.T) {
	fake := &fakeAdapter{streamFn: func(ctx context.Context, _ provider.Endpoint, _ *provider.Request, emit func(provider.Event) error) (*provider.Response, error) {
		if err := emit(provider.Event{Type: provider.EventText, Delta: "first"}); err != nil {
			return nil, err
		}
		<-ctx.Done() // 之后不再发事件
		return nil, ctx.Err()
	}}
	snap := testRouterSnapshot([]config.Provider{testProvider("p1", "m1", testAccount("a1"))}, nil)
	snap.Settings.StreamIdleTimeoutS = 5 // 下限

	r := newTestRouter(t, t.TempDir(), fake)
	start := time.Now()
	_, err := r.Stream(context.Background(), snap, &provider.Request{Model: "p1/m1"}, Meta{RequestID: "req-idle"},
		func(provider.Event) error { return nil })
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("流式空闲应超时")
	}
	pe := provider.AsError(err)
	if pe.Code != provider.CodeUpstreamTimeout || !pe.Retryable {
		t.Fatalf("错误 = %+v，期望 upstream_timeout", pe)
	}
	if elapsed < 4*time.Second || elapsed > 8*time.Second {
		t.Fatalf("空闲超时应约 5s 生效，实际 %v", elapsed)
	}
}

// TestActiveModelOverride 验证运行时全局模型覆盖：请求未指定 model 时用它（HR5）。
func TestActiveModelOverride(t *testing.T) {
	var seen string
	fake := &fakeAdapter{chatFn: func(_ context.Context, ep provider.Endpoint, req *provider.Request) (*provider.Response, error) {
		seen = ep.ProviderID + "/" + req.Model
		return &provider.Response{Text: "ok", StopReason: provider.StopEndTurn}, nil
	}}
	snap := testRouterSnapshot([]config.Provider{
		testProvider("p1", "m1", testAccount("a1")),
		testProvider("p2", "m2", testAccount("a2")),
	}, nil)
	r := newTestRouter(t, t.TempDir(), fake)
	defer r.Close()

	r.SetActiveModel("p2/m2")
	if _, err := r.Complete(context.Background(), snap, &provider.Request{}, Meta{}); err != nil {
		t.Fatal(err)
	}
	if seen != "p2/m2" {
		t.Fatalf("空 model 应走全局覆盖 p2/m2，实际 %s", seen)
	}

	// 显式 model 优先于覆盖。
	r.SetActiveModel("p2/m2")
	if _, err := r.Complete(context.Background(), snap, &provider.Request{Model: "p1/m1"}, Meta{}); err != nil {
		t.Fatal(err)
	}
	if seen != "p1/m1" {
		t.Fatalf("显式 model 应优先，实际 %s", seen)
	}

	// 清除覆盖后回落到配置默认。
	r.SetActiveModel("")
	if r.ActiveModel() != "" {
		t.Fatal("清除后 ActiveModel 应为空")
	}
	if _, err := r.Complete(context.Background(), snap, &provider.Request{}, Meta{}); err != nil {
		t.Fatal(err)
	}
	if seen != "p1/m1" {
		t.Fatalf("清除覆盖后应回落配置默认 p1/m1，实际 %s", seen)
	}
}
