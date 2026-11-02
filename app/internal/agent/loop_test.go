package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"gqy/internal/config"
	"gqy/internal/gateway"
	"gqy/internal/provider"
	"gqy/internal/tool"
)

type fakeStreamer struct {
	calls int
	fn    func(call int, req *provider.Request, emit func(provider.Event) error) (*gateway.Result, error)
}

func (f *fakeStreamer) Stream(ctx context.Context, snap *config.Snapshot, req *provider.Request, meta gateway.Meta, emit func(provider.Event) error) (*gateway.Result, error) {
	f.calls++
	if f.fn != nil {
		return f.fn(f.calls, req, emit)
	}
	_ = emit(provider.Event{Type: provider.EventText, Delta: "ok"})
	_ = emit(provider.Event{Type: provider.EventDone, StopReason: provider.StopEndTurn})
	return &gateway.Result{Response: &provider.Response{Text: "ok", StopReason: provider.StopEndTurn}}, nil
}

type fakeSink struct {
	texts     []string
	thinkings []string
	tools     []tool.Record
	questions []tool.Question
	doneStop  []provider.StopReason
	answerFn  func(q tool.Question) (string, error)
}

func (f *fakeSink) Thinking(delta string) error { f.thinkings = append(f.thinkings, delta); return nil }
func (f *fakeSink) Text(delta string) error     { f.texts = append(f.texts, delta); return nil }
func (f *fakeSink) Tool(rec tool.Record) error  { f.tools = append(f.tools, rec); return nil }
func (f *fakeSink) Question(q tool.Question) (string, error) {
	f.questions = append(f.questions, q)
	if f.answerFn != nil {
		return f.answerFn(q)
	}
	return "", errors.New("no answer")
}
func (f *fakeSink) Done(usage *provider.Usage, stop provider.StopReason) error {
	f.doneStop = append(f.doneStop, stop)
	return nil
}

func loopForTest(reg *tool.Registry) (*Loop, *fakeStreamer) {
	streamer := &fakeStreamer{}
	return &Loop{Streamer: streamer, Tools: reg, MaxRounds: 3, ToolTimeout: 120e9}, streamer
}

func TestLoopToolUseAndContinue(t *testing.T) {
	reg := tool.NewRegistry()
	_ = reg.Register(echoTool{})
	loop, streamer := loopForTest(reg)

	streamer.fn = func(call int, req *provider.Request, emit func(provider.Event) error) (*gateway.Result, error) {
		if call == 1 {
			_ = emit(provider.Event{Type: provider.EventToolCall, Index: 0, ID: "call_1", Name: "echo", ArgumentsDelta: `{"text":"hi"}`})
			_ = emit(provider.Event{Type: provider.EventDone, StopReason: provider.StopToolUse})
			return &gateway.Result{Response: &provider.Response{StopReason: provider.StopToolUse}}, nil
		}
		_ = emit(provider.Event{Type: provider.EventText, Delta: "完成"})
		_ = emit(provider.Event{Type: provider.EventDone, StopReason: provider.StopEndTurn})
		return &gateway.Result{Response: &provider.Response{Text: "完成", StopReason: provider.StopEndTurn}}, nil
	}

	sink := &fakeSink{}
	req := &provider.Request{Model: "m", Messages: []provider.Message{{Role: provider.RoleUser, Content: "hi"}}}
	resp, err := loop.Run(context.Background(), &config.Snapshot{}, req, gateway.Meta{}, sink)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if resp.StopReason != provider.StopEndTurn || resp.Text != "完成" {
		t.Fatalf("resp = %+v", resp)
	}
	if len(sink.tools) != 1 || sink.tools[0].Status != tool.StatusOK || sink.tools[0].Result != "echo:hi" {
		t.Fatalf("tools = %+v", sink.tools)
	}
	// 回填的 tool 消息应进请求上下文
	found := false
	for _, m := range req.Messages {
		if m.Role == provider.RoleTool && m.Name == "echo" && m.Content == "echo:hi" {
			found = true
		}
	}
	if !found {
		t.Fatalf("工具结果未回填: %+v", req.Messages)
	}
	if len(sink.doneStop) != 1 || sink.doneStop[0] != provider.StopEndTurn {
		t.Fatalf("done = %v", sink.doneStop)
	}
}

func TestLoopMaxRounds(t *testing.T) {
	reg := tool.NewRegistry()
	_ = reg.Register(echoTool{})
	loop, streamer := loopForTest(reg)
	loop.MaxRounds = 2

	streamer.fn = func(call int, req *provider.Request, emit func(provider.Event) error) (*gateway.Result, error) {
		_ = emit(provider.Event{Type: provider.EventToolCall, Index: 0, ID: "c", Name: "echo", ArgumentsDelta: `{"text":"x"}`})
		_ = emit(provider.Event{Type: provider.EventDone, StopReason: provider.StopToolUse})
		return &gateway.Result{Response: &provider.Response{StopReason: provider.StopToolUse}}, nil
	}

	sink := &fakeSink{}
	resp, err := loop.Run(context.Background(), &config.Snapshot{}, &provider.Request{Model: "m", Messages: []provider.Message{{Role: provider.RoleUser, Content: "x"}}}, gateway.Meta{}, sink)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StopReason != provider.StopOther {
		t.Fatalf("max_rounds 到顶应标 other: %+v", resp)
	}
	if len(sink.tools) != 2 {
		t.Fatalf("应执行两轮工具: %d", len(sink.tools))
	}
	if len(sink.doneStop) != 1 || sink.doneStop[0] != provider.StopOther {
		t.Fatalf("done = %v", sink.doneStop)
	}
}

func TestAskUserNumericAndCustom(t *testing.T) {
	reg := tool.NewRegistry()
	_ = reg.Register(tool.AskUserTool{})
	for _, tc := range []struct {
		answer string
		want   string
	}{
		{"1", "继续"},
		{"自定义答案", "自定义答案"},
	} {
		loop, streamer := loopForTest(reg)
		streamer.fn = func(call int, req *provider.Request, emit func(provider.Event) error) (*gateway.Result, error) {
			if call == 1 {
				_ = emit(provider.Event{Type: provider.EventToolCall, Index: 0, ID: "q1", Name: "ask_user", ArgumentsDelta: `{"question":"继续吗","options":["继续","取消"]}`})
				_ = emit(provider.Event{Type: provider.EventDone, StopReason: provider.StopToolUse})
				return &gateway.Result{Response: &provider.Response{StopReason: provider.StopToolUse}}, nil
			}
			_ = emit(provider.Event{Type: provider.EventDone, StopReason: provider.StopEndTurn})
			return &gateway.Result{Response: &provider.Response{StopReason: provider.StopEndTurn}}, nil
		}
		sink := &fakeSink{answerFn: func(q tool.Question) (string, error) { return ResolveAnswer(tc.answer, q.Options), nil }}
		req := &provider.Request{Model: "m", Messages: []provider.Message{{Role: provider.RoleUser, Content: "hi"}}}
		resp, err := loop.Run(context.Background(), &config.Snapshot{}, req, gateway.Meta{}, sink)
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if resp.StopReason != provider.StopEndTurn {
			t.Fatalf("resp = %+v", resp)
		}
		if len(sink.questions) != 1 || len(sink.questions[0].Options) != 2 {
			t.Fatalf("questions = %+v", sink.questions)
		}
		if len(sink.tools) != 1 || sink.tools[0].Result != tc.want {
			t.Fatalf("answer %q → tools = %+v", tc.answer, sink.tools)
		}
	}
}

func TestAskUserPendingStopsLoop(t *testing.T) {
	reg := tool.NewRegistry()
	_ = reg.Register(tool.AskUserTool{})
	loop, streamer := loopForTest(reg)
	streamer.fn = func(call int, req *provider.Request, emit func(provider.Event) error) (*gateway.Result, error) {
		_ = emit(provider.Event{Type: provider.EventToolCall, Index: 0, ID: "q1", Name: "ask_user", ArgumentsDelta: `{"question":"继续吗"}`})
		_ = emit(provider.Event{Type: provider.EventDone, StopReason: provider.StopToolUse})
		return &gateway.Result{Response: &provider.Response{StopReason: provider.StopToolUse}}, nil
	}
	sink := &fakeSink{answerFn: func(q tool.Question) (string, error) { return "", ErrAskPending }}
	resp, err := loop.Run(context.Background(), &config.Snapshot{}, &provider.Request{Model: "m", Messages: []provider.Message{{Role: provider.RoleUser, Content: "hi"}}}, gateway.Meta{}, sink)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StopReason != provider.StopOther || len(sink.doneStop) != 0 {
		t.Fatalf("待回答应暂停且不 emit done: %+v / %v", resp, sink.doneStop)
	}
}

func TestToolDeclsCoreOnly(t *testing.T) {
	reg := tool.NewRegistry()
	_ = reg.Register(echoTool{})
	loop := &Loop{Tools: reg}
	decls := loop.ToolDecls()
	if len(decls) != 1 || decls[0].Name != "echo" {
		t.Fatalf("decls = %+v", decls)
	}
}

// echoTool 是测试用的核心工具。
type echoTool struct{}

func (echoTool) Name() string        { return "echo" }
func (echoTool) Description() string { return "echo" }
func (echoTool) Source() tool.Source { return tool.SourceCore }
func (echoTool) Schema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{"text": map[string]any{"type": "string"}}, "required": []string{"text"}}
}
func (echoTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	text, _ := args["text"].(string)
	if strings.TrimSpace(text) == "" {
		return "", tool.ErrInvalidArgument
	}
	return "echo:" + text, nil
}

// fakeBranches 是 013/014 的测试替身：FIFO 队列 + 结果回收。
type fakeBranches struct {
	queue   []string
	records [][2]string
	adopt   []string
}

func (f *fakeBranches) TakeQueued() []string {
	out := append([]string(nil), f.queue...)
	f.queue = nil
	return out
}

func (f *fakeBranches) RecordBranch(message, output string) error {
	f.records = append(f.records, [2]string{message, output})
	return nil
}

func (f *fakeBranches) TakeAdoptions() []string {
	out := append([]string(nil), f.adopt...)
	f.adopt = nil
	return out
}

// TestLoopDrainsBranchesAtRoundBoundaries 验证每轮开始前处理支线，且不打断主线。
func TestLoopDrainsBranchesAtRoundBoundaries(t *testing.T) {
	fs := &fakeStreamer{}
	var branchTools, mainRounds int
	fs.fn = func(call int, req *provider.Request, emit func(provider.Event) error) (*gateway.Result, error) {
		// 支线请求：单条 user 消息，内容以「支线」开头，且不带工具。
		if len(req.Messages) == 1 && strings.HasPrefix(req.Messages[0].Content, "支线") {
			branchTools += len(req.Tools)
			out := "答:" + req.Messages[0].Content
			_ = emit(provider.Event{Type: provider.EventText, Delta: out})
			_ = emit(provider.Event{Type: provider.EventDone, StopReason: provider.StopEndTurn})
			return &gateway.Result{Response: &provider.Response{Text: out, StopReason: provider.StopEndTurn}}, nil
		}
		// 主线第一轮制造一次工具调用，让 loop 进入第二轮。
		mainRounds++
		if mainRounds == 1 {
			_ = emit(provider.Event{Type: provider.EventToolCall, Index: 0, ID: "c1", Name: "noop", ArgumentsDelta: `{}`})
			_ = emit(provider.Event{Type: provider.EventDone, StopReason: provider.StopToolUse})
			return &gateway.Result{Response: &provider.Response{StopReason: provider.StopToolUse}}, nil
		}
		_ = emit(provider.Event{Type: provider.EventText, Delta: "done"})
		_ = emit(provider.Event{Type: provider.EventDone, StopReason: provider.StopEndTurn})
		return &gateway.Result{Response: &provider.Response{Text: "done", StopReason: provider.StopEndTurn}}, nil
	}
	sink := &fakeSink{}
	loop := NewLoop(fs, tool.NewRegistry())
	fb := &fakeBranches{queue: []string{"支线A", "支线B"}}
	loop.Branches = fb

	mainReq := &provider.Request{Messages: []provider.Message{{Role: provider.RoleUser, Content: "主线"}}}
	resp, err := loop.Run(context.Background(), &config.Snapshot{}, mainReq, gateway.Meta{}, sink)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if resp.Text != "done" || resp.StopReason != provider.StopEndTurn {
		t.Fatalf("主线响应被支线影响: %+v", resp)
	}
	if len(mainReq.Messages) != 2 {
		t.Fatalf("主线上下文应只加一条工具结果，实际 %d 条", len(mainReq.Messages))
	}
	want := [][2]string{{"支线A", "答:支线A"}, {"支线B", "答:支线B"}}
	if len(fb.records) != 2 || fb.records[0] != want[0] || fb.records[1] != want[1] {
		t.Fatalf("支线结果 = %v，期望 %v", fb.records, want)
	}
	if branchTools != 0 {
		t.Fatalf("支线请求不应携带工具，实际 %d 个", branchTools)
	}
	// 2 条支线 + 2 轮主线 = 4 次模型调用
	if fs.calls != 4 {
		t.Fatalf("模型调用次数 = %d，期望 4", fs.calls)
	}
}

func TestLoopBranchFailureDoesNotBreakMain(t *testing.T) {
	fs := &fakeStreamer{}
	fs.fn = func(call int, req *provider.Request, emit func(provider.Event) error) (*gateway.Result, error) {
		if len(req.Messages) == 1 && strings.HasPrefix(req.Messages[0].Content, "支线") {
			return nil, errors.New("上游炸了")
		}
		_ = emit(provider.Event{Type: provider.EventText, Delta: "ok"})
		_ = emit(provider.Event{Type: provider.EventDone, StopReason: provider.StopEndTurn})
		return &gateway.Result{Response: &provider.Response{Text: "ok", StopReason: provider.StopEndTurn}}, nil
	}
	loop := NewLoop(fs, tool.NewRegistry())
	fb := &fakeBranches{queue: []string{"支线失败"}}
	loop.Branches = fb

	resp, err := loop.Run(context.Background(), &config.Snapshot{}, &provider.Request{Messages: []provider.Message{{Role: provider.RoleUser, Content: "主线"}}}, gateway.Meta{}, &fakeSink{})
	if err != nil {
		t.Fatalf("支线失败不应让主线返回错误: %v", err)
	}
	if resp.Text != "ok" {
		t.Fatalf("主线响应 = %+v", resp)
	}
	if len(fb.records) != 1 || !strings.HasPrefix(fb.records[0][1], "支线执行失败: ") {
		t.Fatalf("支线失败应记录，实际 %v", fb.records)
	}
}

func TestLoopBranchEmptyQueueNoExtraCalls(t *testing.T) {
	fs := &fakeStreamer{}
	loop := NewLoop(fs, tool.NewRegistry())
	loop.Branches = &fakeBranches{}
	if _, err := loop.Run(context.Background(), &config.Snapshot{}, &provider.Request{Messages: []provider.Message{{Role: provider.RoleUser, Content: "主线"}}}, gateway.Meta{}, &fakeSink{}); err != nil {
		t.Fatal(err)
	}
	if fs.calls != 1 {
		t.Fatalf("空队列不应产生额外调用，实际 %d", fs.calls)
	}
}

func TestToolInfos(t *testing.T) {
	reg := tool.NewRegistry()
	if err := reg.Register(echoTool{}); err != nil {
		t.Fatal(err)
	}
	loop := NewLoop(nil, reg)
	infos := loop.ToolInfos()
	if len(infos) != 1 || infos[0].Name != "echo" || infos[0].Source != "core" || infos[0].Schema == nil {
		t.Fatalf("infos = %+v", infos)
	}
	decls := loop.ToolDecls()
	if len(decls) != 1 || decls[0].Name != "echo" || decls[0].Parameters == nil {
		t.Fatalf("decls = %+v", decls)
	}
}

// TestLoopInjectsAdoptedBranchesOnce 验证已采纳支线摘要注入主线上下文且只注入一次（017）。
func TestLoopInjectsAdoptedBranchesOnce(t *testing.T) {
	fs := &fakeStreamer{}
	mainRounds := 0
	fs.fn = func(call int, req *provider.Request, emit func(provider.Event) error) (*gateway.Result, error) {
		mainRounds++
		if mainRounds == 1 {
			_ = emit(provider.Event{Type: provider.EventToolCall, Index: 0, ID: "c1", Name: "noop", ArgumentsDelta: `{}`})
			_ = emit(provider.Event{Type: provider.EventDone, StopReason: provider.StopToolUse})
			return &gateway.Result{Response: &provider.Response{StopReason: provider.StopToolUse}}, nil
		}
		_ = emit(provider.Event{Type: provider.EventText, Delta: "ok"})
		_ = emit(provider.Event{Type: provider.EventDone, StopReason: provider.StopEndTurn})
		return &gateway.Result{Response: &provider.Response{Text: "ok", StopReason: provider.StopEndTurn}}, nil
	}
	loop := NewLoop(fs, tool.NewRegistry())
	loop.Branches = &fakeBranches{adopt: []string{"支线摘要A"}}

	req := &provider.Request{Messages: []provider.Message{{Role: provider.RoleUser, Content: "主线"}}}
	if _, err := loop.Run(context.Background(), &config.Snapshot{}, req, gateway.Meta{}, &fakeSink{}); err != nil {
		t.Fatal(err)
	}
	adopted := 0
	for _, m := range req.Messages {
		if strings.Contains(m.Content, "支线已采纳") && strings.Contains(m.Content, "支线摘要A") {
			adopted++
		}
	}
	if adopted != 1 {
		t.Fatalf("采纳摘要应注入一次，实际 %d 次（messages=%d）", adopted, len(req.Messages))
	}
}

// TestArtifactsFor 验证只有写文件工具（edit）产出产物路径（022）。
func TestArtifactsFor(t *testing.T) {
	if got := artifactsFor("edit", map[string]any{"path": "/tmp/a.txt"}); len(got) != 1 || got[0] != "/tmp/a.txt" {
		t.Fatalf("edit 应产出 artifact，实际 %v", got)
	}
	if got := artifactsFor("read", map[string]any{"path": "/tmp/a.txt"}); got != nil {
		t.Fatalf("read 不应产出 artifact，实际 %v", got)
	}
	if got := artifactsFor("edit", map[string]any{}); got != nil {
		t.Fatalf("缺 path 时不应产出 artifact，实际 %v", got)
	}
}
