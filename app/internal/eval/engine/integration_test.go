package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gqy/internal/eval/cases"
	"gqy/internal/eval/config"
	"gqy/internal/eval/matrix"
	"gqy/internal/eval/recorder"
	"gqy/internal/eval/replay"
	"gqy/internal/eval/report"
	"gqy/internal/provider"
	"gqy/internal/tool"
)

type echoCompleter struct {
	calls int
}

func (e *echoCompleter) Complete(ctx context.Context, req provider.Request) (provider.Message, *provider.Usage, error) {
	e.calls++
	if e.calls == 1 {
		var msg provider.Message
		msg.Role = "assistant"
		msg.ToolCalls = []provider.ToolCall{{ID: "call-1", Type: "function"}}
		msg.ToolCalls[0].Function.Name = "plugin__echo__echo"
		msg.ToolCalls[0].Function.Arguments = `{"text":"hello"}`
		return msg, &provider.Usage{PromptTokens: 5, CompletionTokens: 3, TotalTokens: 8}, nil
	}
	return provider.Message{Role: "assistant", Content: "回显结果是 hello"}, &provider.Usage{PromptTokens: 5, CompletionTokens: 3, TotalTokens: 8}, nil
}

func TestEndToEndRecordReportReplay(t *testing.T) {
	out := t.TempDir()
	cfg := &config.Config{
		Name: "echo", Tool: "plugin__echo__echo", Model: "m", Cases: "cases.json",
		Repeats: 1, Concurrency: 1, MaxRounds: 3, Stage: config.StageFull, Round: "r1",
		Axes:  config.Axes{Prompt: []string{"basic"}, Schema: []string{"full"}, Return: []string{"raw"}},
		Fixed: config.Fixed{Prompt: "basic", Schema: "full", Return: "raw"},
		Timeouts: config.Timeouts{
			RequestMS: 1000, ToolMS: 1000, TotalMS: 5000, Retries: 0, BackoffMS: 1, BackoffMaxMS: 2,
		},
	}
	should := true
	set := &cases.Set{
		Version: "1",
		Cases: []cases.Case{{
			ID:    "c1",
			Input: "回显 hello",
			Expect: cases.Expect{
				ShouldCall:    &should,
				Tool:          "plugin__echo__echo",
				ArgContains:   map[string]any{"text": "hello"},
				FinalContains: []string{"hello"},
			},
		}},
	}
	// 用例哈希由加载器计算；这里手工构造时补一个稳定值。
	set.Hash = "manual-hash"

	reg := tool.NewRegistry()
	if err := reg.Register(echoTool{}); err != nil {
		t.Fatal(err)
	}
	rec, err := recorder.New(out, "r1", time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	defer rec.Close()

	exps := []matrix.Experiment{{Prompt: "basic", Schema: "full", Return: "raw", Round: "r1", Axis: "full", Index: 0}}
	traces, err := Run(context.Background(), Options{
		Config: cfg, Cases: set, Experiments: exps, Registry: reg, Completer: &echoCompleter{},
		Recorder: rec,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(traces) != 1 || traces[0].Judge == nil || !traces[0].Judge.Pass {
		t.Fatalf("运行未通过: %+v", traces)
	}
	for _, path := range []string{
		rec.ConfigPath(),
		rec.RunsPath(),
		rec.ErrorsPath(),
		rec.CasesDir(),
		filepath.Join(rec.TraceDir(), traces[0].RunID+".json"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("缺少产物 %s: %v", path, err)
		}
	}

	md := report.Build(cfg, set, traces, rec.TraceRelDir())
	if err := rec.WriteReport(md); err != nil {
		t.Fatal(err)
	}
	reportData, err := os.ReadFile(rec.ReportPath())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(reportData), "矩阵汇总") || !strings.Contains(string(reportData), "结论与建议") {
		t.Fatalf("报告内容异常:\n%s", reportData)
	}

	loaded, err := replay.LoadDir(rec.TraceDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 1 {
		t.Fatalf("trace 数量=%d", len(loaded))
	}
	res, err := replay.Run(loaded[0], loaded[0].Config.StrictSchema)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Matches() || !res.Verdict.Pass {
		t.Fatalf("回放与录制不一致: %+v", res)
	}
}
