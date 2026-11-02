package eval

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gqy/internal/provider"
	"gqy/internal/tool"
)

type fakeRounder struct {
	calls int
	fn    func(call int, messages []provider.Message, tools []provider.Tool) (provider.Response, error)
}

func (f *fakeRounder) Round(ctx context.Context, model string, messages []provider.Message, tools []provider.Tool) (provider.Response, error) {
	f.calls++
	if f.fn != nil {
		return f.fn(f.calls, messages, tools)
	}
	return provider.Response{Text: "ok", StopReason: provider.StopEndTurn}, nil
}

type echoExec struct{}

func (echoExec) Execute(ctx context.Context, name string, args map[string]any) (string, error) {
	if name != "echo" {
		return "", tool.ErrNotFound
	}
	text, _ := args["text"].(string)
	return "echo:" + text, nil
}

func testEngine() *Engine {
	return &Engine{
		Rounder:   &fakeRounder{},
		Host:      NewHost(echoExec{}, []ToolDecl{{Name: "echo", Schema: map[string]any{"type": "object"}}}),
		MaxRounds: 5,
		TimeoutMS: 10000,
	}
}

func TestMatrixSingleAxis(t *testing.T) {
	cfg := &EvalConfig{
		Stage:      "single",
		PromptVars: []string{"basic", "when"},
		SchemaVars: []string{"min", "full"},
		ReturnVars: []string{"raw", "trim"},
		Fixed:      Combo{Prompt: "basic", Schema: "full", Return: "raw"},
	}
	entries, err := BuildMatrix(cfg, "")
	if err != nil {
		t.Fatal(err)
	}
	// 三个轴各 2 个变体
	if len(entries) != 6 {
		t.Fatalf("组合数 = %d, want 6", len(entries))
	}
	axes := map[string]bool{}
	for _, e := range entries {
		axes[e.Axis] = true
		if e.Round != "single" {
			t.Fatalf("round = %q", e.Round)
		}
	}
	if !axes["prompt"] || !axes["schema"] || !axes["return"] {
		t.Fatalf("axis 标签不齐: %v", axes)
	}
	// 固定两维扫第三维：prompt 轴固定 schema/return
	for _, e := range entries {
		if e.Axis == "prompt" && (e.Combo.Schema != "full" || e.Combo.Return != "raw") {
			t.Fatalf("prompt 轴未固定其他两维: %+v", e.Combo)
		}
	}
	// filter
	filtered, _ := BuildMatrix(cfg, "prompt=basic")
	if len(filtered) == 0 {
		t.Fatal("filter 结果为空")
	}
	for _, e := range filtered {
		if e.Combo.Prompt != "basic" {
			t.Fatalf("filter 结果含非 basic: %+v", e.Combo)
		}
	}
}

func TestDryRunNoModelCalls(t *testing.T) {
	engine := testEngine()
	rounder := engine.Rounder.(*fakeRounder)
	runner := &Runner{
		Cfg:      &EvalConfig{Stage: "single", PromptVars: []string{"basic"}, SchemaVars: []string{"full"}, ReturnVars: []string{"raw"}, Fixed: Combo{Prompt: "basic", Schema: "full", Return: "raw"}, Repeat: 1},
		Cases:    &Cases{Version: "1", Cases: []Case{{ID: "c1", Input: "hi"}}},
		Engine:   engine,
		Recorder: NewRecorder(t.TempDir()),
	}
	results, err := runner.Run(context.Background(), Options{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if results != nil {
		t.Fatalf("dry-run 不应有结果: %+v", results)
	}
	if rounder.calls != 0 {
		t.Fatalf("dry-run 不应发模型请求: %d", rounder.calls)
	}
}

func TestJudgeRulesPassAndFail(t *testing.T) {
	passCases := []Case{
		{ID: "should", Expect: Expect{ShouldCall: boolPtr(true)}},
		{ID: "tool", Expect: Expect{Tool: "echo"}},
		{ID: "arg", Expect: Expect{ArgContains: map[string]any{"text": "hello"}}},
		{ID: "final", Expect: Expect{FinalContain: []string{"hello"}}},
		{ID: "not", Expect: Expect{MustNotCall: "rm"}},
	}
	for _, tc := range passCases {
		calls := []ToolCallRecord{{Name: "echo", ArgsRaw: `{"text":"hello"}`}}
		if j := JudgeRules(tc, "结果包含 hello", calls); !j.Pass {
			t.Fatalf("%s 应通过: %+v", tc.ID, j)
		}
	}

	failCases := []Case{
		{ID: "should", Expect: Expect{ShouldCall: boolPtr(true)}},
		{ID: "tool", Expect: Expect{Tool: "rm"}},
		{ID: "arg", Expect: Expect{ArgContains: map[string]any{"text": "nope"}}},
		{ID: "final", Expect: Expect{FinalContain: []string{"missing"}}},
		{ID: "not", Expect: Expect{MustNotCall: "echo"}},
	}
	for _, tc := range failCases {
		var calls []ToolCallRecord
		if tc.ID != "should" {
			calls = []ToolCallRecord{{Name: "echo", ArgsRaw: `{"text":"hello"}`}}
		}
		if j := JudgeRules(tc, "结果包含 hello", calls); j.Pass {
			t.Fatalf("%s 应判反例: %+v", tc.ID, j)
		}
	}
}

func TestEngineLoopConverges(t *testing.T) {
	engine := testEngine()
	rounder := engine.Rounder.(*fakeRounder)
	rounder.fn = func(call int, messages []provider.Message, tools []provider.Tool) (provider.Response, error) {
		if call == 1 {
			return provider.Response{
				ToolCalls:  []provider.ToolCall{{ID: "t1", Name: "echo", Arguments: map[string]any{"text": "hello"}}},
				StopReason: provider.StopToolUse,
			}, nil
		}
		return provider.Response{Text: "echo:hello", StopReason: provider.StopEndTurn, Usage: provider.Usage{PromptTokens: 5, CompletionTokens: 3}}, nil
	}

	run := engine.Run(context.Background(), Combo{Prompt: "basic", Schema: "full", Return: "raw"}, Case{ID: "c1", Input: "hi", Expect: Expect{FinalContain: []string{"hello"}}}, 0, "single", "return", "r1")
	if run.Status != StatusOK || run.FinalAnswer != "echo:hello" {
		t.Fatalf("run = %+v", run)
	}
	if len(run.ToolCalls) != 1 || !run.ToolCalls[0].ExecOK || run.ToolCalls[0].RetRendered != "echo:hello" {
		t.Fatalf("tool_calls = %+v", run.ToolCalls)
	}
	if run.RoundsUsed != 2 {
		t.Fatalf("rounds = %d", run.RoundsUsed)
	}
	j := JudgeRules(Case{ID: "c1", Expect: Expect{FinalContain: []string{"hello"}}}, run.FinalAnswer, run.ToolCalls)
	if !j.Pass {
		t.Fatalf("judge = %+v", j)
	}
}

func TestFaultInjection(t *testing.T) {
	for _, mode := range []string{"empty", "error", "slow"} {
		engine := testEngine()
		engine.Rounder.(*fakeRounder).fn = func(call int, messages []provider.Message, tools []provider.Tool) (provider.Response, error) {
			if call == 1 {
				return provider.Response{ToolCalls: []provider.ToolCall{{ID: "t1", Name: "echo", Arguments: map[string]any{}}}, StopReason: provider.StopToolUse}, nil
			}
			return provider.Response{Text: "final", StopReason: provider.StopEndTurn}, nil
		}
		inj := &Inject{Mode: mode, Tool: "echo", Times: 1, DelayMS: 10}
		run := engine.Run(context.Background(), Combo{Prompt: "basic", Schema: "full", Return: "raw"}, Case{ID: "c1", Input: "x", Inject: inj}, 0, "single", "return", "r1")
		want := map[string]string{"empty": StatusOK, "error": StatusExecErr, "slow": StatusOK}[mode]
		if run.Status != want {
			t.Fatalf("mode=%s status=%s want %s", mode, run.Status, want)
		}
	}
	// timeout 单独测（短超时）
	engine := testEngine()
	engine.TimeoutMS = 1
	engine.Rounder.(*fakeRounder).fn = func(call int, messages []provider.Message, tools []provider.Tool) (provider.Response, error) {
		if call == 1 {
			return provider.Response{ToolCalls: []provider.ToolCall{{ID: "t1", Name: "echo", Arguments: map[string]any{}}}, StopReason: provider.StopToolUse}, nil
		}
		time.Sleep(10 * time.Millisecond)
		return provider.Response{Text: "late", StopReason: provider.StopEndTurn}, nil
	}
	run := engine.Run(context.Background(), Combo{Prompt: "basic", Schema: "full", Return: "raw"}, Case{ID: "c1", Input: "x", Inject: &Inject{Mode: "timeout", Tool: "echo", Times: 1}}, 0, "single", "return", "r1")
	if run.Status != StatusNotConverge {
		t.Fatalf("timeout status=%s", run.Status)
	}
}

func TestReplayNoModelCalls(t *testing.T) {
	dir := t.TempDir()
	trace := filepath.Join(dir, "trace")
	os.MkdirAll(trace, 0o700)
	run := &RunResult{RunID: "r1", CaseID: "c1", Round: "single", Axis: "return", Status: StatusOK, FinalAnswer: "ok", Judge: JudgeResult{Rule: RuleJudge{Pass: true}}}
	data, _ := json.Marshal(run)
	os.WriteFile(filepath.Join(trace, "r1.json"), data, 0o600)

	runs, err := ReplayResults(trace)
	if err != nil || len(runs) != 1 || !runs[0].Replay {
		t.Fatalf("ReplayResults = %+v, %v", runs, err)
	}
}

func TestRedactSecrets(t *testing.T) {
	raw := `{"config":{"api_key":"sk-secret","token":"tok","authorization":"Bearer x","secret":"s"}}`
	out := string(Redact([]byte(raw)))
	for _, secret := range []string{"sk-secret", `"tok"`, "Bearer x"} {
		if strings.Contains(out, secret) {
			t.Fatalf("脱敏后仍含 %q: %s", secret, out)
		}
	}
}

func boolPtr(v bool) *bool { return &v }
