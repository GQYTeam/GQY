package httpapi

// 生产路径集成测试：/api/chat 主线运行中排队支线 → 轮末执行 → 采纳 → 注入。
// 用测试替身替换模型来源，走的是真实的 handler / agent loop / goal / session 代码。

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"gqy/internal/agent"
	"gqy/internal/config"
	"gqy/internal/gateway"
	"gqy/internal/goal"
	"gqy/internal/provider"
	"gqy/internal/session"
	"gqy/internal/tool"
)

type branchStreamer struct {
	mu       sync.Mutex
	calls    []*provider.Request
	started  chan struct{}
	release  chan struct{}
	firstRun sync.Once
}

func newBranchStreamer() *branchStreamer {
	return &branchStreamer{started: make(chan struct{}), release: make(chan struct{})}
}

func (b *branchStreamer) Stream(ctx context.Context, snap *config.Snapshot, req *provider.Request, meta gateway.Meta, emit func(provider.Event) error) (*gateway.Result, error) {
	b.mu.Lock()
	b.calls = append(b.calls, req)
	b.mu.Unlock()

	// 支线调用：单条 user 消息、不带工具。
	if len(req.Tools) == 0 && len(req.Messages) == 1 && strings.HasPrefix(req.Messages[0].Content, "支线") {
		_ = emit(provider.Event{Type: provider.EventText, Delta: "支线答复"})
		_ = emit(provider.Event{Type: provider.EventDone, StopReason: provider.StopEndTurn})
		return &gateway.Result{Response: &provider.Response{Text: "支线答复", StopReason: provider.StopEndTurn}}, nil
	}
	// 主线第一次调用：阻塞，制造「主线运行中」窗口。
	blocked := false
	b.firstRun.Do(func() {
		blocked = true
		close(b.started)
		<-b.release
	})
	_ = blocked
	_ = emit(provider.Event{Type: provider.EventText, Delta: "主线完成"})
	_ = emit(provider.Event{Type: provider.EventDone, StopReason: provider.StopEndTurn})
	return &gateway.Result{Response: &provider.Response{Text: "主线完成", StopReason: provider.StopEndTurn}}, nil
}

func (b *branchStreamer) lastMessages() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.calls) == 0 {
		return nil
	}
	last := b.calls[len(b.calls)-1]
	out := make([]string, 0, len(last.Messages))
	for _, m := range last.Messages {
		out = append(out, m.Content)
	}
	return out
}

func sessionStoreForTest(t *testing.T, env *testEnv) *session.Store {
	t.Helper()
	return session.NewStore(env.home)
}

func TestAgentChatBranchChainProductionPath(t *testing.T) {
	env := newTestServer(t, "", nil)
	env.srv.SetGoals(goal.NewManager())
	env.srv.gw = gateway.New(provider.NewRegistry(), nil, env.home)
	streamer := newBranchStreamer()
	env.srv.SetAgent(agent.NewLoop(streamer, tool.NewRegistry()))

	g := env.srv.goals.Create("主线目标", 5)
	store := sessionStoreForTest(t, env)
	sess, err := store.Create("/tmp/ws")
	if err != nil {
		t.Fatal(err)
	}

	// 1) 主线请求：阻塞在第一次模型调用，会话进入 running。
	first := httptest.NewRecorder()
	firstReq := httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(
		`{"session_id":"`+sess.ID()+`","model":"p/m1","messages":[{"role":"user","content":"主线开始"}]}`))
	firstReq.RemoteAddr = "127.0.0.1:12345"
	done := make(chan struct{})
	go func() {
		env.srv.Handler().ServeHTTP(first, firstReq)
		close(done)
	}()
	select {
	case <-streamer.started:
	case <-time.After(3 * time.Second):
		t.Fatal("主线模型调用未开始")
	}

	// 2) 主线运行中再发一条：应排队，不打断主线。
	second := httptest.NewRecorder()
	secondReq := httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(
		`{"session_id":"`+sess.ID()+`","model":"p/m1","messages":[{"role":"user","content":"支线插话"}]}`))
	secondReq.RemoteAddr = "127.0.0.1:12345"
	env.srv.Handler().ServeHTTP(second, secondReq)
	if second.Code != http.StatusOK || !strings.Contains(second.Body.String(), `"queued":true`) {
		t.Fatalf("运行中应排队，status=%d body=%s", second.Code, second.Body.String())
	}
	// 只读检查（不要 TakeQueued：那会把队列取空，影响轮末执行）。
	queued := 0
	for _, st := range env.srv.goals.Status() {
		queued += len(st.Queued)
	}
	if queued != 1 {
		t.Fatalf("主线运行中应有 1 条排队支线，实际 %d", queued)
	}

	// 3) 放行主线：轮末应执行支线并写 branch 事件。
	close(streamer.release)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("主线请求未结束")
	}
	body := first.Body.String()
	if !strings.Contains(body, `"type":"branch"`) {
		t.Fatalf("主线响应应包含 branch 事件，实际 %s", body)
	}
	if !strings.Contains(body, "支线答复") {
		t.Fatalf("支线应被真实执行，实际 %s", body)
	}

	// 4) 采纳后，下一次请求应把摘要注入主线（出现在模型请求的 messages 里）。
	status := env.srv.goals.Status()
	if len(status) == 0 || len(status[0].Branches) == 0 {
		t.Fatal("goal 应登记支线")
	}
	branchID := status[0].Branches[0].BranchID
	if err := env.srv.goals.Adopt(g.ID, branchID, ""); err != nil {
		t.Fatal(err)
	}
	third := httptest.NewRecorder()
	thirdReq := httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(
		`{"session_id":"`+sess.ID()+`","model":"p/m1","messages":[{"role":"user","content":"继续主线"}]}`))
	thirdReq.RemoteAddr = "127.0.0.1:12345"
	env.srv.Handler().ServeHTTP(third, thirdReq)

	injected := false
	for _, m := range streamer.lastMessages() {
		if strings.Contains(m, "[支线已采纳") {
			injected = true
		}
	}
	if !injected {
		t.Fatalf("采纳摘要未注入主线请求: %v", streamer.lastMessages())
	}
}
