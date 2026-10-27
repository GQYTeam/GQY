package engine

import (
	"context"
	"sync"
	"testing"
	"time"

	"gqy/internal/eval/cases"
	"gqy/internal/eval/config"
	"gqy/internal/eval/matrix"
	"gqy/internal/eval/result"
	"gqy/internal/provider"
	"gqy/internal/tool"
)

type echoTool struct{}

func (echoTool) Name() string        { return "plugin__echo__echo" }
func (echoTool) Description() string { return "echo" }
func (echoTool) Kind() tool.Kind     { return tool.KindCore }
func (echoTool) Schema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{"text": map[string]any{"type": "string"}}}
}
func (echoTool) Execute(ctx context.Context, args map[string]any) (string, error) { return "ok", nil }

type finalCompleter struct {
	mu     sync.Mutex
	calls  int
	active int
	maxAct int
	delay  time.Duration
}

func (f *finalCompleter) Complete(ctx context.Context, req provider.Request) (provider.Message, *provider.Usage, error) {
	f.mu.Lock()
	f.calls++
	f.active++
	if f.active > f.maxAct {
		f.maxAct = f.active
	}
	f.mu.Unlock()
	if f.delay > 0 {
		select {
		case <-ctx.Done():
		case <-time.After(f.delay):
		}
	}
	f.mu.Lock()
	f.active--
	f.mu.Unlock()
	return provider.Message{Role: "assistant", Content: "ok"}, &provider.Usage{PromptTokens: 1, CompletionTokens: 1, TotalTokens: 2}, nil
}

func TestRunMatrixDeterministicOrder(t *testing.T) {
	cfg := &config.Config{
		Name: "t", Tool: "plugin__echo__echo", Model: "m", Cases: "c.json",
		Repeats: 2, Concurrency: 2, MaxRounds: 2, Stage: config.StageFull, Round: "r1",
		Axes:  config.Axes{Prompt: []string{"basic", "when"}, Schema: []string{"full"}, Return: []string{"raw"}},
		Fixed: config.Fixed{Prompt: "basic", Schema: "full", Return: "raw"},
	}
	exps := []matrix.Experiment{
		{Prompt: "basic", Schema: "full", Return: "raw", Round: "r1", Axis: "full", Index: 0},
		{Prompt: "when", Schema: "full", Return: "raw", Round: "r1", Axis: "full", Index: 1},
	}
	set := &cases.Set{
		Version: "1", Hash: "h",
		Cases: []cases.Case{
			{ID: "c1", Input: "a", Expect: cases.Expect{FinalContains: []string{"ok"}}},
			{ID: "c2", Input: "b", Expect: cases.Expect{FinalContains: []string{"ok"}}},
		},
	}
	reg := tool.NewRegistry()
	if err := reg.Register(echoTool{}); err != nil {
		t.Fatal(err)
	}
	compl := &finalCompleter{delay: 5 * time.Millisecond}
	progress := 0
	traces, err := Run(context.Background(), Options{
		Config: cfg, Cases: set, Experiments: exps, Registry: reg, Completer: compl,
		Progress: func(done, total int, tr *result.Trace) { progress = done },
	})
	if err != nil {
		t.Fatal(err)
	}
	if progress != 8 {
		t.Fatalf("progress 回调次数=%d", progress)
	}
	if len(traces) != 8 {
		t.Fatalf("结果数=%d", len(traces))
	}
	wantOrder := []struct {
		prompt, caseID string
		repeat         int
	}{
		{"basic", "c1", 0}, {"basic", "c1", 1},
		{"basic", "c2", 0}, {"basic", "c2", 1},
		{"when", "c1", 0}, {"when", "c1", 1},
		{"when", "c2", 0}, {"when", "c2", 1},
	}
	for i, want := range wantOrder {
		tr := traces[i]
		if tr.Config.Prompt != want.prompt || tr.CaseID != want.caseID || tr.RepeatIdx != want.repeat {
			t.Fatalf("第 %d 条顺序不对: prompt=%s case=%s repeat=%d, want %+v", i, tr.Config.Prompt, tr.CaseID, tr.RepeatIdx, want)
		}
		if tr.Judge == nil || !tr.Judge.Pass || tr.Status != "ok" {
			t.Fatalf("第 %d 条判定不对: %+v", i, tr)
		}
	}
	if compl.maxAct > cfg.Concurrency {
		t.Fatalf("并发超过上限: max=%d limit=%d", compl.maxAct, cfg.Concurrency)
	}
}

type noopCompleter struct{}

func (noopCompleter) Complete(ctx context.Context, req provider.Request) (provider.Message, *provider.Usage, error) {
	return provider.Message{Content: "ok"}, nil, nil
}

func TestRunValidation(t *testing.T) {
	if _, err := Run(context.Background(), Options{}); err == nil {
		t.Fatal("空参数应报错")
	}
	cfg := &config.Config{Name: "t", Tool: "x", Model: "m", Repeats: 1, Concurrency: 1, Round: "r1"}
	if _, err := Run(context.Background(), Options{Config: cfg, Cases: &cases.Set{}, Experiments: nil, Registry: tool.NewRegistry(), Completer: noopCompleter{}}); err == nil {
		t.Fatal("空矩阵应报错")
	}
}
