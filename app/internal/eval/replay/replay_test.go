package replay

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"gqy/internal/eval/cases"
	"gqy/internal/eval/judge"
	"gqy/internal/eval/result"
)

func sampleTrace() *result.Trace {
	should := true
	c := cases.Case{
		ID:    "c1",
		Input: "回显 hello",
		Expect: cases.Expect{
			ShouldCall:    &should,
			Tool:          "plugin__echo__echo",
			ArgContains:   map[string]any{"text": "hello"},
			FinalContains: []string{"hello"},
		},
	}
	tr := &result.Trace{
		RunID:       "r1-0001-c1",
		CaseID:      "c1",
		Case:        c,
		Config:      result.RunConfig{Prompt: "basic", Schema: "full", Return: "raw", Model: "m", Tool: "plugin__echo__echo", StrictSchema: false},
		Round:       "r1",
		Axis:        "full",
		Status:      result.StatusOK,
		FinalAnswer: "结果是 hello",
		ToolCalls: []result.ToolCallRecord{{
			Name:        "plugin__echo__echo",
			ArgsRaw:     `{"text":"hello"}`,
			ArgsParsed:  map[string]any{"text": "hello"},
			SchemaValid: true,
			ExecOK:      true,
			RetRaw:      `{"echo":"hello"}`,
			RetRendered: `{"echo":"hello"}`,
			LatencyMS:   5,
		}},
	}
	v := judge.Judge(c, tr, false)
	tr.Judge = &v
	return tr
}

func TestReplayConsistent(t *testing.T) {
	tr := sampleTrace()
	res, err := Run(tr, tr.Config.StrictSchema)
	if err != nil {
		t.Fatal(err)
	}
	if !res.RenderMatch || !res.JudgeMatch || !res.Matches() {
		t.Fatalf("回放应与录制一致: %+v", res)
	}
	if !res.Verdict.Pass || res.Trace.FinalAnswer != tr.FinalAnswer || res.Trace.Status != tr.Status {
		t.Fatalf("回放结论不对: %+v", res.Verdict)
	}
	if len(res.Rendered) != 1 || res.Rendered[0] != tr.ToolCalls[0].RetRendered {
		t.Fatalf("重渲染不对: %v", res.Rendered)
	}
	// 原始 trace 不应被回放修改。
	if tr.ToolCalls[0].RetRendered != `{"echo":"hello"}` {
		t.Fatal("回放修改了原始 trace")
	}
}

func TestReplayDetectsRenderDrift(t *testing.T) {
	tr := sampleTrace()
	tr.ToolCalls[0].RetRendered = "tampered"
	res, err := Run(tr, false)
	if err != nil {
		t.Fatal(err)
	}
	if res.RenderMatch || res.Matches() {
		t.Fatalf("渲染漂移应被发现: %+v", res)
	}
}

func TestReplayDetectsJudgeDrift(t *testing.T) {
	tr := sampleTrace()
	bad := result.Verdict{Pass: false, Reasons: []string{"tampered"}, Rules: map[string]bool{}}
	tr.Judge = &bad
	res, err := Run(tr, false)
	if err != nil {
		t.Fatal(err)
	}
	if res.JudgeMatch {
		t.Fatalf("判定漂移应被发现: %+v", res)
	}
}

func TestLoadDirAndReplay(t *testing.T) {
	root := t.TempDir()
	traceDir := filepath.Join(root, "trace")
	if err := os.MkdirAll(traceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	tr := sampleTrace()
	data, err := json.MarshalIndent(tr, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(traceDir, tr.RunID+".json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 1 || loaded[0].RunID != tr.RunID {
		t.Fatalf("加载 trace 不对: %+v", loaded)
	}
	res, err := Run(loaded[0], loaded[0].Config.StrictSchema)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Matches() || !res.Verdict.Pass {
		t.Fatalf("回放不一致: %+v", res)
	}
}

func TestLoadDirErrors(t *testing.T) {
	root := t.TempDir()
	if _, err := LoadDir(filepath.Join(root, "missing")); err == nil {
		t.Fatal("不存在目录应报错")
	}
	if err := os.WriteFile(filepath.Join(root, "x.json"), []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadDir(root); err == nil {
		t.Fatal("坏 trace 应报错")
	}
	empty := t.TempDir()
	if _, err := LoadDir(empty); err == nil {
		t.Fatal("空目录应报错")
	}
}
