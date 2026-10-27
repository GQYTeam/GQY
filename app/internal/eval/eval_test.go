package eval

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gqy/internal/provider"
	"gqy/internal/tool"
)

type fakeTool struct{}

func (fakeTool) Name() string        { return "echo" }
func (fakeTool) Kind() tool.Kind     { return tool.KindCore }
func (fakeTool) Description() string { return "回显文本" }
func (fakeTool) Schema() map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{"text": map[string]any{"type": "string", "description": "文本"}},
		"required":   []any{"text"},
	}
}
func (fakeTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	if args["text"] == "fail" {
		return "", errors.New("boom")
	}
	return "echo:" + args["text"].(string), nil
}

type fakeCompleter struct {
	calls int
	mode  string
}

func (f *fakeCompleter) Complete(ctx context.Context, req provider.Request) (provider.Message, *provider.Usage, error) {
	f.calls++
	if f.mode == "gateway_error" {
		return provider.Message{}, nil, errors.New("upstream down")
	}
	if f.calls%2 == 1 {
		return provider.Message{
			Role: "assistant",
			ToolCalls: []provider.ToolCall{{
				ID:   "call_1",
				Type: "function",
				Function: struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				}{Name: "echo", Arguments: `{"text":"hi"}`},
			}},
		}, &provider.Usage{PromptTokens: 3, CompletionTokens: 2, TotalTokens: 5}, nil
	}
	return provider.Message{Role: "assistant", Content: "结果是 echo:hi"}, &provider.Usage{PromptTokens: 4, CompletionTokens: 3}, nil
}

func TestGenerateSingleMatrix(t *testing.T) {
	cfg := Config{
		Axes: map[string][]string{
			"prompt": {"basic", "strict"},
			"schema": {"min", "full"},
			"return": {"raw", "trim"},
		},
		Fixed: map[string]string{"prompt": "basic", "schema": "full", "return": "raw"},
		Stage: "single",
	}
	got := GenerateMatrix(cfg)
	// fixed 值（basic/full/raw）会在三个轴里重复，去重后剩 4 个。
	if len(got) != 4 {
		t.Fatalf("single 变体数=%d: %+v", len(got), got)
	}
	cfg.Stage = "full"
	if got := GenerateMatrix(cfg); len(got) != 8 {
		t.Fatalf("full 变体数=%d", len(got))
	}
}

func TestRenderAndJudge(t *testing.T) {
	if got := RenderReturn("hello", nil, "summary"); !strings.Contains(got, "工具返回") {
		t.Fatalf("summary: %q", got)
	}
	if got := RenderReturn("", errors.New("bad"), "error-min"); got != "失败" {
		t.Fatalf("error-min: %q", got)
	}
	yes := true
	c := Case{ID: "c1", Expect: Expect{
		ShouldCall: &yes, Tool: "echo",
		ArgContains:   map[string]string{"text": "hi"},
		FinalContains: []string{"echo:hi"},
	}}
	res := Judge(c, []ToolCallRecord{{Name: "echo", ArgsRaw: `{"text":"hi"}`}}, "结果是 echo:hi", "ok")
	if !res.Pass {
		t.Fatalf("judge: %+v", res)
	}
}

func TestRunnerAndReport(t *testing.T) {
	reg := tool.NewRegistry()
	_ = reg.Register(fakeTool{})
	cfg := Config{
		Name: "test", Model: "m", Tool: "echo", Repeats: 1, MaxRounds: 3, Stage: "single",
		Axes:  map[string][]string{"prompt": {"basic"}, "schema": {"full"}, "return": {"raw"}},
		Fixed: map[string]string{"prompt": "basic", "schema": "full", "return": "raw"},
		Out:   t.TempDir(),
	}
	set := CaseSet{Version: "1", Cases: []Case{{
		ID: "c1", Input: "回显 hi",
		Expect: Expect{Tool: "echo", ArgContains: map[string]string{"text": "hi"}, FinalContains: []string{"echo:hi"}},
	}}}
	variants := GenerateMatrix(cfg)
	if len(variants) != 1 {
		t.Fatalf("variants=%d", len(variants))
	}
	runner := &Runner{Completer: &fakeCompleter{}, Registry: reg}
	records, err := runner.Run(context.Background(), cfg, set, variants)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || !records[0].Judge.Pass || records[0].Tokens.In != 7 {
		t.Fatalf("record: %+v", records)
	}
	rec, err := NewRecorder(cfg.Out, "run-1", cfg, set, variants)
	if err != nil {
		t.Fatal(err)
	}
	if err := rec.Add(records[0]); err != nil {
		t.Fatal(err)
	}
	_ = rec.Close()
	best, complete, err := Report(cfg.Out, "run-1", cfg, variants, records)
	if err != nil {
		t.Fatal(err)
	}
	if !complete || best.Prompt != "basic" {
		t.Fatalf("best=%+v complete=%v", best, complete)
	}
	for _, p := range []string{
		filepath.Join(cfg.Out, "runs", "run-1", "runs.jsonl"),
		filepath.Join(cfg.Out, "report", "run-1.md"),
		filepath.Join(cfg.Out, "best", "current.json"),
	} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("缺少 %s: %v", p, err)
		}
	}
	raw, _ := os.ReadFile(filepath.Join(cfg.Out, "runs", "run-1", "runs.jsonl"))
	var one RunRecord
	if err := json.Unmarshal(raw[:len(raw)-1], &one); err != nil {
		t.Fatalf("runs.jsonl: %v", err)
	}
}
