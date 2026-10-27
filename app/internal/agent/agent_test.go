package agent

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"gqy/internal/provider"
	"gqy/internal/tool"
)

type fakeCompleter struct {
	responses []provider.Message
	requests  []provider.Request
	calls     int
}

func (f *fakeCompleter) Complete(ctx context.Context, req provider.Request) (provider.Message, *provider.Usage, error) {
	f.requests = append(f.requests, req)
	if f.calls >= len(f.responses) {
		return provider.Message{}, nil, fmt.Errorf("unexpected Complete call %d", f.calls)
	}
	msg := f.responses[f.calls]
	f.calls++
	return msg, &provider.Usage{TotalTokens: 1}, nil
}

type testTool struct {
	name       string
	delay      time.Duration
	respectCtx bool
	calls      atomic.Int32
}

func (t *testTool) Name() string        { return t.name }
func (t *testTool) Description() string { return "test tool " + t.name }
func (t *testTool) Kind() tool.Kind     { return tool.KindCore }
func (t *testTool) Schema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}

func (t *testTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	t.calls.Add(1)
	if t.respectCtx {
		select {
		case <-time.After(t.delay):
			return "ok", nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	time.Sleep(t.delay)
	return "ok", nil
}

func makeToolCall(id, name, args string) provider.ToolCall {
	tc := provider.ToolCall{ID: id, Type: "function"}
	tc.Function.Name = name
	tc.Function.Arguments = args
	return tc
}

func TestToolTimeoutContinuesLoop(t *testing.T) {
	slow := &testTool{name: "slow", delay: time.Second}
	reg := tool.NewRegistry()
	if err := reg.Register(slow); err != nil {
		t.Fatal(err)
	}

	fake := &fakeCompleter{responses: []provider.Message{
		{Role: "assistant", ToolCalls: []provider.ToolCall{makeToolCall("c1", "slow", `{}`)}},
		{Role: "assistant", Content: "完成"},
	}}

	var results []ToolResult
	loop := &Loop{
		Upstream:    fake,
		Registry:    reg,
		ToolTimeout: 50 * time.Millisecond,
		OnTool:      func(r ToolResult) { results = append(results, r) },
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	final, _, ask, err := loop.Run(ctx, []provider.Message{{Role: "user", Content: "go"}}, "m", "")
	if err != nil {
		t.Fatalf("loop 应继续到最终答案: %v", err)
	}
	if ask != nil {
		t.Fatalf("不应返回 ask: %+v", ask)
	}
	if final.Content != "完成" {
		t.Fatalf("最终答案 = %q", final.Content)
	}
	if fake.calls != 2 {
		t.Fatalf("Complete 调用次数 = %d, want 2", fake.calls)
	}
	if len(results) != 1 {
		t.Fatalf("OnTool 次数 = %d, want 1", len(results))
	}
	if results[0].Error == nil || !strings.Contains(results[0].Error.Error(), "超时") {
		t.Fatalf("工具结果应回填超时错误，实际: %v", results[0].Error)
	}
	if results[0].Duration > 500*time.Millisecond {
		t.Fatalf("超时应在上限附近返回，实际耗时 %s", results[0].Duration)
	}

	// 第二轮请求里的 tool 消息应包含超时错误，证明 loop 把错误回填给了模型。
	last := fake.requests[1].Messages[len(fake.requests[1].Messages)-1]
	if last.Role != "tool" || !strings.Contains(last.Content, "超时") {
		t.Fatalf("回填消息不符合预期: role=%q content=%q", last.Role, last.Content)
	}
}

func TestToolTimeoutRespectsContext(t *testing.T) {
	slow := &testTool{name: "slowctx", delay: 5 * time.Second, respectCtx: true}
	reg := tool.NewRegistry()
	if err := reg.Register(slow); err != nil {
		t.Fatal(err)
	}
	fake := &fakeCompleter{responses: []provider.Message{
		{Role: "assistant", ToolCalls: []provider.ToolCall{makeToolCall("c1", "slowctx", `{}`)}},
		{Role: "assistant", Content: "done"},
	}}

	var got ToolResult
	loop := &Loop{
		Upstream:    fake,
		Registry:    reg,
		ToolTimeout: 40 * time.Millisecond,
		OnTool:      func(r ToolResult) { got = r },
	}
	final, _, _, err := loop.Run(context.Background(), []provider.Message{{Role: "user", Content: "go"}}, "m", "")
	if err != nil {
		t.Fatalf("loop: %v", err)
	}
	if final.Content != "done" {
		t.Fatalf("final = %q", final.Content)
	}
	if got.Error == nil || !strings.Contains(got.Error.Error(), "超时") {
		t.Fatalf("应报告超时，实际: %v", got.Error)
	}
	if got := slow.calls.Load(); got != 1 {
		t.Fatalf("工具调用次数 = %d", got)
	}
}

func TestNoToolTimeoutByDefault(t *testing.T) {
	fast := &testTool{name: "fast", delay: 30 * time.Millisecond}
	reg := tool.NewRegistry()
	if err := reg.Register(fast); err != nil {
		t.Fatal(err)
	}
	fake := &fakeCompleter{responses: []provider.Message{
		{Role: "assistant", ToolCalls: []provider.ToolCall{makeToolCall("c1", "fast", `{}`)}},
		{Role: "assistant", Content: "done"},
	}}
	var got ToolResult
	loop := &Loop{Upstream: fake, Registry: reg, OnTool: func(r ToolResult) { got = r }}
	if _, _, _, err := loop.Run(context.Background(), []provider.Message{{Role: "user", Content: "go"}}, "m", ""); err != nil {
		t.Fatalf("loop: %v", err)
	}
	if got.Error != nil || got.Output != "ok" {
		t.Fatalf("未设置超时不应误杀工具: out=%q err=%v", got.Output, got.Error)
	}
}

func TestAskUserStillInterruptsWithTimeout(t *testing.T) {
	reg := tool.NewRegistry()
	if err := reg.Register(tool.AskUser{}); err != nil {
		t.Fatal(err)
	}
	fake := &fakeCompleter{responses: []provider.Message{
		{Role: "assistant", ToolCalls: []provider.ToolCall{makeToolCall("c1", "ask_user", `{"question":"继续?","options":["是","否"]}`)}},
	}}
	loop := &Loop{Upstream: fake, Registry: reg, ToolTimeout: time.Nanosecond}
	_, _, ask, err := loop.Run(context.Background(), []provider.Message{{Role: "user", Content: "go"}}, "m", "")
	if err != nil {
		t.Fatalf("ask_user 不应受工具超时影响: %v", err)
	}
	if ask == nil || ask.Question != "继续?" || len(ask.Options) != 2 {
		t.Fatalf("ask = %+v", ask)
	}
	if fake.calls != 1 {
		t.Fatalf("ask_user 后不应再请求模型，calls=%d", fake.calls)
	}
}

func TestParentContextCancellation(t *testing.T) {
	slow := &testTool{name: "slow", delay: time.Second, respectCtx: true}
	reg := tool.NewRegistry()
	if err := reg.Register(slow); err != nil {
		t.Fatal(err)
	}
	fake := &fakeCompleter{responses: []provider.Message{
		{Role: "assistant", ToolCalls: []provider.ToolCall{makeToolCall("c1", "slow", `{}`)}},
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	loop := &Loop{Upstream: fake, Registry: reg, ToolTimeout: 5 * time.Second}
	if _, _, _, err := loop.Run(ctx, []provider.Message{{Role: "user", Content: "go"}}, "m", ""); err == nil {
		t.Fatal("父 context 取消应返回错误")
	}
}
