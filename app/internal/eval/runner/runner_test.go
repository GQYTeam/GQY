package runner

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"gqy/internal/eval/cases"
	"gqy/internal/eval/config"
	"gqy/internal/eval/judge"
	"gqy/internal/eval/matrix"
	"gqy/internal/eval/result"
	"gqy/internal/provider"
	"gqy/internal/tool"
)

type fakeTool struct {
	name   string
	schema map[string]any
	out    string
	err    error
	calls  int
}

func (f *fakeTool) Name() string { return f.name }
func (f *fakeTool) Description() string {
	return "回显工具。用于测试。不要用它做别的。"
}
func (f *fakeTool) Kind() tool.Kind { return tool.KindCore }
func (f *fakeTool) Schema() map[string]any {
	if f.schema != nil {
		return f.schema
	}
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"text":  map[string]any{"type": "string", "description": "文本"},
			"count": map[string]any{"type": "number", "description": "可选数量"},
		},
		"required": []any{"text"},
	}
}
func (f *fakeTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	f.calls++
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return f.out, f.err
}

type seqCompleter struct {
	mu    sync.Mutex
	calls int
	fn    func(ctx context.Context, req provider.Request, n int) (provider.Message, *provider.Usage, error)
}

func (s *seqCompleter) Complete(ctx context.Context, req provider.Request) (provider.Message, *provider.Usage, error) {
	s.mu.Lock()
	s.calls++
	n := s.calls
	s.mu.Unlock()
	return s.fn(ctx, req, n)
}

func (s *seqCompleter) Calls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func usage(in, out int) *provider.Usage {
	return &provider.Usage{PromptTokens: in, CompletionTokens: out, TotalTokens: in + out}
}

func toolCall(name, args string) provider.Message {
	var msg provider.Message
	msg.Role = "assistant"
	msg.ToolCalls = []provider.ToolCall{{
		ID:   "",
		Type: "function",
		Function: struct {
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		}{Name: name, Arguments: args},
	}}
	return msg
}

func final(text string) provider.Message { return provider.Message{Role: "assistant", Content: text} }

func testCfg() *config.Config {
	return &config.Config{
		Name: "t", Tool: "plugin__echo__echo", Model: "fake-model", Cases: "cases.json",
		Repeats: 1, Concurrency: 1, MaxRounds: 4, Stage: config.StageFull, Round: "r1",
		Axes:  config.Axes{Prompt: []string{"basic"}, Schema: []string{"full"}, Return: []string{"raw"}},
		Fixed: config.Fixed{Prompt: "basic", Schema: "full", Return: "raw"},
		Timeouts: config.Timeouts{
			RequestMS: 1000, ToolMS: 1000, TotalMS: 5000,
			Retries: 0, BackoffMS: 1, BackoffMaxMS: 2,
		},
	}
}

func exp(returnVariant string) matrix.Experiment {
	return matrix.Experiment{Prompt: "basic", Schema: "full", Return: returnVariant, Round: "r1", Axis: "full"}
}

func testCase() cases.Case {
	should := true
	return cases.Case{
		ID:    "case-1",
		Input: "回显 hello",
		Expect: cases.Expect{
			ShouldCall:    &should,
			Tool:          "plugin__echo__echo",
			ArgContains:   map[string]any{"text": "hello"},
			FinalContains: []string{"hello"},
		},
	}
}

func registryWith(t *testing.T, ft *fakeTool) *tool.Registry {
	t.Helper()
	reg := tool.NewRegistry()
	if err := reg.Register(ft); err != nil {
		t.Fatal(err)
	}
	return reg
}

func TestMultiRoundToolCall(t *testing.T) {
	ft := &fakeTool{name: "plugin__echo__echo", out: `{"echo":"hello"}`}
	compl := &seqCompleter{fn: func(ctx context.Context, req provider.Request, n int) (provider.Message, *provider.Usage, error) {
		switch n {
		case 1:
			if len(req.Tools) != 1 {
				t.Fatalf("第一次请求应带 1 个工具，实际 %d", len(req.Tools))
			}
			return toolCall("plugin__echo__echo", `{"text":"hello"}`), usage(10, 5), nil
		case 2:
			return final("回显结果是 hello"), usage(10, 5), nil
		}
		return final("unexpected"), nil, nil
	}}
	cfg := testCfg()
	tr, err := RunOne(context.Background(), Options{
		Config: cfg, Case: testCase(), Experiment: exp("raw"), Registry: registryWith(t, ft),
		Completer: compl, RunID: "run-1", CaseSetHash: "hash", RepeatIdx: 0,
	})
	if err != nil {
		t.Fatal(err)
	}
	if tr.Status != result.StatusOK || tr.RoundsUsed != 1 || len(tr.Exchanges) != 2 {
		t.Fatalf("trace 状态不对: status=%s rounds=%d exchanges=%d", tr.Status, tr.RoundsUsed, len(tr.Exchanges))
	}
	if len(tr.ToolCalls) != 1 || !tr.ToolCalls[0].ExecOK || !tr.ToolCalls[0].SchemaValid {
		t.Fatalf("工具调用记录不对: %+v", tr.ToolCalls)
	}
	if tr.ToolCalls[0].RetRendered != `{"echo":"hello"}` {
		t.Fatalf("raw 渲染不对: %q", tr.ToolCalls[0].RetRendered)
	}
	if tr.FinalAnswer != "回显结果是 hello" || tr.Tokens.In != 20 || tr.Tokens.Out != 10 || tr.Tokens.Total != 30 {
		t.Fatalf("最终答案/token 不对: %+v", tr)
	}
	if len(tr.Messages) != 5 {
		t.Fatalf("消息轨迹长度=%d: %+v", len(tr.Messages), tr.Messages)
	}
	if tr.Messages[3].Role != "tool" || tr.Messages[3].Content != `{"echo":"hello"}` {
		t.Fatalf("工具消息没有回填: %+v", tr.Messages[3])
	}
	if tr.Messages[2].ToolCalls[0].ID == "" {
		t.Fatal("工具调用 ID 未补全")
	}
	v := judge.Judge(testCase(), tr, true)
	if !v.Pass {
		t.Fatalf("判定应通过: %+v", v)
	}
}

func TestNotConverged(t *testing.T) {
	ft := &fakeTool{name: "plugin__echo__echo", out: `{"ok":true}`}
	compl := &seqCompleter{fn: func(ctx context.Context, req provider.Request, n int) (provider.Message, *provider.Usage, error) {
		return toolCall("plugin__echo__echo", `{"text":"x"}`), usage(1, 1), nil
	}}
	cfg := testCfg()
	cfg.MaxRounds = 2
	tr, err := RunOne(context.Background(), Options{
		Config: cfg, Case: testCase(), Experiment: exp("raw"), Registry: registryWith(t, ft),
		Completer: compl, RunID: "run-2",
	})
	if err != nil {
		t.Fatal(err)
	}
	if tr.Status != result.StatusNotConverged || tr.RoundsUsed != 2 || len(tr.ToolCalls) != 2 {
		t.Fatalf("期望未收敛: %+v", tr)
	}
	if !strings.Contains(tr.Error, "最大轮次") {
		t.Fatalf("错误信息缺少最大轮次: %q", tr.Error)
	}
	if tr.FinalAnswer != "" {
		t.Fatalf("未收敛不应有最终答案: %q", tr.FinalAnswer)
	}
}

func TestRetryWithBackoff(t *testing.T) {
	ft := &fakeTool{name: "plugin__echo__echo", out: `{"ok":true}`}
	compl := &seqCompleter{fn: func(ctx context.Context, req provider.Request, n int) (provider.Message, *provider.Usage, error) {
		if n == 1 {
			return provider.Message{}, nil, &provider.UpstreamError{Status: 500, Code: "upstream_500", Detail: "boom"}
		}
		return final("ok"), usage(1, 1), nil
	}}
	cfg := testCfg()
	cfg.Timeouts.Retries = 1
	cfg.Timeouts.BackoffMS = 0
	tr, err := RunOne(context.Background(), Options{
		Config: cfg, Case: testCase(), Experiment: exp("raw"), Registry: registryWith(t, ft),
		Completer: compl, RunID: "run-3",
	})
	if err != nil {
		t.Fatal(err)
	}
	if tr.Status != result.StatusOK || compl.Calls() != 2 {
		t.Fatalf("重试失败: status=%s calls=%d", tr.Status, compl.Calls())
	}
	if len(tr.Retries) != 1 || !strings.Contains(tr.Retries[0].RetryOf, "#r0-a1") {
		t.Fatalf("retry_of 记录不对: %+v", tr.Retries)
	}
	if tr.RetryOf != tr.Retries[0].RetryOf {
		t.Fatalf("顶层 retry_of 不对: %q vs %q", tr.RetryOf, tr.Retries[0].RetryOf)
	}
	if len(tr.Exchanges) != 1 || tr.Exchanges[0].Attempts != 2 {
		t.Fatalf("exchange attempts 不对: %+v", tr.Exchanges)
	}
}

func TestTotalTimeout(t *testing.T) {
	ft := &fakeTool{name: "plugin__echo__echo", out: `{"ok":true}`}
	compl := &seqCompleter{fn: func(ctx context.Context, req provider.Request, n int) (provider.Message, *provider.Usage, error) {
		<-ctx.Done()
		return provider.Message{}, nil, ctx.Err()
	}}
	cfg := testCfg()
	cfg.Timeouts.TotalMS = 30
	cfg.Timeouts.RequestMS = 1000
	start := time.Now()
	tr, err := RunOne(context.Background(), Options{
		Config: cfg, Case: testCase(), Experiment: exp("raw"), Registry: registryWith(t, ft),
		Completer: compl, RunID: "run-4",
	})
	if err != nil {
		t.Fatal(err)
	}
	if tr.Status != result.StatusTimeout {
		t.Fatalf("期望 timeout，实际 %s (%s)", tr.Status, tr.Error)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("超时没有生效")
	}
	v := judge.Judge(testCase(), tr, true)
	if v.Pass || !v.Runtime {
		t.Fatalf("运行时错误应单独判定: %+v", v)
	}
}

func TestStrictSchemaBlocksExecution(t *testing.T) {
	ft := &fakeTool{name: "plugin__echo__echo", out: `{"ok":true}`}
	compl := &seqCompleter{fn: func(ctx context.Context, req provider.Request, n int) (provider.Message, *provider.Usage, error) {
		if n == 1 {
			return toolCall("plugin__echo__echo", `{}`), usage(1, 1), nil
		}
		return final("参数不对"), usage(1, 1), nil
	}}
	cfg := testCfg()
	cfg.StrictSchema = true
	tr, err := RunOne(context.Background(), Options{
		Config: cfg, Case: testCase(), Experiment: exp("raw"), Registry: registryWith(t, ft),
		Completer: compl, RunID: "run-5",
	})
	if err != nil {
		t.Fatal(err)
	}
	if ft.calls != 0 {
		t.Fatalf("严格校验下工具不应执行，实际执行 %d 次", ft.calls)
	}
	if len(tr.ToolCalls) != 1 || tr.ToolCalls[0].SchemaValid || tr.ToolCalls[0].ExecOK {
		t.Fatalf("工具调用记录不对: %+v", tr.ToolCalls)
	}
	if tr.Status != result.StatusExecError {
		t.Fatalf("期望 exec_error，实际 %s", tr.Status)
	}
	if !strings.Contains(tr.ToolCalls[0].RetRendered, "参数校验失败") {
		t.Fatalf("回填没有参数错误: %q", tr.ToolCalls[0].RetRendered)
	}
}

func TestFaultInjection(t *testing.T) {
	base := testCase()
	casesInject := []struct {
		name   string
		inject *cases.Inject
		ret    string
		want   result.Status
		execOK bool
		render string
		timed  bool
	}{
		{
			name:   "error",
			inject: &cases.Inject{Mode: "error", Times: 1, Message: "注入失败"},
			ret:    "error-rich",
			want:   result.StatusExecError,
			render: "TOOL_ERROR",
		},
		{
			name:   "empty",
			inject: &cases.Inject{Mode: "empty", Times: 1},
			ret:    "raw",
			want:   result.StatusOK,
			execOK: true,
		},
		{
			name:   "timeout",
			inject: &cases.Inject{Mode: "timeout", Times: 1},
			ret:    "error-rich",
			want:   result.StatusTimeout,
			render: "TOOL_TIMEOUT",
			timed:  true,
		},
	}
	for _, tc := range casesInject {
		t.Run(tc.name, func(t *testing.T) {
			ft := &fakeTool{name: "plugin__echo__echo", out: `{"real":true}`}
			compl := &seqCompleter{fn: func(ctx context.Context, req provider.Request, n int) (provider.Message, *provider.Usage, error) {
				if n == 1 {
					return toolCall("plugin__echo__echo", `{"text":"hello"}`), usage(1, 1), nil
				}
				return final("hello"), usage(1, 1), nil
			}}
			c := base
			c.Inject = tc.inject
			tr, err := RunOne(context.Background(), Options{
				Config: testCfg(), Case: c, Experiment: exp(tc.ret), Registry: registryWith(t, ft),
				Completer: compl, RunID: "run-inject-" + tc.name,
			})
			if err != nil {
				t.Fatal(err)
			}
			if ft.calls != 0 {
				t.Fatalf("注入时不应执行真实工具，实际 %d", ft.calls)
			}
			if len(tr.ToolCalls) != 1 {
				t.Fatalf("工具调用数量=%d", len(tr.ToolCalls))
			}
			rec := tr.ToolCalls[0]
			if !rec.Injected || rec.ExecOK != tc.execOK || rec.TimedOut != tc.timed {
				t.Fatalf("注入记录不对: %+v", rec)
			}
			if tr.Status != tc.want {
				t.Fatalf("status=%s, want %s (%s)", tr.Status, tc.want, tr.Error)
			}
			if tc.render != "" && !strings.Contains(rec.RetRendered, tc.render) {
				t.Fatalf("渲染缺少 %s: %q", tc.render, rec.RetRendered)
			}
		})
	}
}

func TestRenderReturnVariants(t *testing.T) {
	raw := `{"a":"` + strings.Repeat("x", 200) + `","b":"","c":1,"d":[1,2,3,4]}`
	tests := []struct {
		variant string
		raw     string
		err     error
		want    []string
		not     []string
	}{
		{"raw", "hello", nil, []string{"hello"}, nil},
		{"trim", raw, nil, []string{`"a"`, `"c"`, `"d"`}, []string{`"b"`, `4`}},
		{"summary", "hello world", nil, []string{"工具返回：", "hello"}, nil},
		{"typed", `{"n":1,"s":"x"}`, nil, []string{`"type":"number"`, `"type":"string"`}, nil},
		{"error-rich", "", errors.New("boom"), []string{"TOOL_ERROR", `"retryable":false`}, nil},
		{"error-min", "", errors.New("boom"), []string{"失败"}, nil},
	}
	for _, tc := range tests {
		got, err := RenderReturn(tc.variant, tc.raw, tc.err)
		if err != nil {
			t.Fatalf("%s: %v", tc.variant, err)
		}
		for _, w := range tc.want {
			if !strings.Contains(got, w) {
				t.Errorf("%s: 缺少 %q: %q", tc.variant, w, got)
			}
		}
		for _, n := range tc.not {
			if strings.Contains(got, n) {
				t.Errorf("%s: 不应包含 %q: %q", tc.variant, n, got)
			}
		}
	}
	if _, err := RenderReturn("nope", "", nil); err == nil {
		t.Fatal("未知 return 变体应报错")
	}
}

func TestPromptAndSchemaVariants(t *testing.T) {
	view := ToolView{ExposedName: "echo", RealName: "plugin__echo__echo", Description: "回显工具。用于测试。"}
	none, err := RenderSystemPrompt("none", []ToolView{view})
	if err != nil || none != "" {
		t.Fatalf("none 应为空: %q %v", none, err)
	}
	checks := map[string]string{
		"basic":    "echo",
		"when":     "什么时候调用",
		"when-how": "怎么调用",
		"fewshot":  "示例",
		"strict":   "不得",
	}
	for variant, want := range checks {
		got, err := RenderSystemPrompt(variant, []ToolView{view})
		if err != nil || !strings.Contains(got, want) {
			t.Fatalf("%s 缺少 %q: %q %v", variant, want, got, err)
		}
	}
	if _, err := RenderSystemPrompt("nope", nil); err == nil {
		t.Fatal("未知 prompt 变体应报错")
	}

	ft := &fakeTool{name: "plugin__echo__echo"}
	min, err := RenderToolSchema(ft, "min")
	if err != nil {
		t.Fatal(err)
	}
	props := min.Parameters["properties"].(map[string]any)
	if _, ok := props["count"]; ok {
		t.Fatalf("min 不应包含可选参数: %+v", props)
	}
	if _, ok := props["text"]; !ok {
		t.Fatalf("min 缺少必填参数: %+v", props)
	}

	full, _ := RenderToolSchema(ft, "full")
	if _, ok := full.Parameters["properties"].(map[string]any)["count"]; !ok {
		t.Fatal("full 应包含可选参数")
	}

	example, _ := RenderToolSchema(ft, "example")
	textProp := example.Parameters["properties"].(map[string]any)["text"].(map[string]any)
	if _, ok := textProp["examples"]; !ok {
		t.Fatal("example 缺少 examples")
	}

	neg, _ := RenderToolSchema(ft, "negative")
	if !strings.Contains(neg.Description, "不要用它") {
		t.Fatalf("negative 描述不对: %q", neg.Description)
	}

	alias, _ := RenderToolSchema(ft, "alias")
	if alias.ExposedName != "echo" || alias.RealName != "plugin__echo__echo" {
		t.Fatalf("alias 不对: %+v", alias)
	}
	if _, err := RenderToolSchema(ft, "nope"); err == nil {
		t.Fatal("未知 schema 变体应报错")
	}
}

func TestAliasCallResolvesRealTool(t *testing.T) {
	ft := &fakeTool{name: "plugin__echo__echo", out: `{"ok":true}`}
	compl := &seqCompleter{fn: func(ctx context.Context, req provider.Request, n int) (provider.Message, *provider.Usage, error) {
		if n == 1 {
			return toolCall("echo", `{"text":"hello"}`), usage(1, 1), nil
		}
		return final("hello"), usage(1, 1), nil
	}}
	e := exp("full")
	e.Schema = "alias"
	tr, err := RunOne(context.Background(), Options{
		Config: testCfg(), Case: testCase(), Experiment: e, Registry: registryWith(t, ft),
		Completer: compl, RunID: "run-alias",
	})
	if err != nil {
		t.Fatal(err)
	}
	if tr.Status != result.StatusOK || len(tr.ToolCalls) != 1 || tr.ToolCalls[0].Name != "plugin__echo__echo" || tr.ToolCalls[0].CalledAs != "echo" {
		t.Fatalf("alias 调用没有解析回真实工具: %+v status=%s", tr.ToolCalls, tr.Status)
	}
}

func TestUnknownToolIsExecError(t *testing.T) {
	ft := &fakeTool{name: "plugin__echo__echo", out: `{"ok":true}`}
	compl := &seqCompleter{fn: func(ctx context.Context, req provider.Request, n int) (provider.Message, *provider.Usage, error) {
		if n == 1 {
			return toolCall("nope", `{}`), usage(1, 1), nil
		}
		return final("failed"), usage(1, 1), nil
	}}
	tr, err := RunOne(context.Background(), Options{
		Config: testCfg(), Case: testCase(), Experiment: exp("raw"), Registry: registryWith(t, ft),
		Completer: compl, RunID: "run-unknown",
	})
	if err != nil {
		t.Fatal(err)
	}
	if tr.Status != result.StatusExecError || !strings.Contains(tr.ToolCalls[0].Error, "未知工具") {
		t.Fatalf("未知工具应记为 exec_error: %+v", tr)
	}
}

func TestRunOneSetupErrors(t *testing.T) {
	cfg := testCfg()
	if _, err := RunOne(context.Background(), Options{Config: cfg, Completer: &seqCompleter{fn: func(context.Context, provider.Request, int) (provider.Message, *provider.Usage, error) {
		return final("x"), nil, nil
	}}, Registry: tool.NewRegistry(), Case: testCase(), Experiment: exp("raw")}); err == nil {
		t.Fatal("缺少工具应报错")
	}
	if _, err := RunOne(context.Background(), Options{Config: nil}); err == nil {
		t.Fatal("缺少配置应报错")
	}
}
