package report

import (
	"strings"
	"testing"

	"gqy/internal/eval/cases"
	"gqy/internal/eval/config"
	"gqy/internal/eval/result"
)

func testTrace(runID, caseID, prompt string, pass bool) *result.Trace {
	return &result.Trace{
		RunID:      runID,
		CaseID:     caseID,
		Config:     result.RunConfig{Prompt: prompt, Schema: "full", Return: "raw", Model: "m"},
		Round:      "r1",
		Axis:       "full",
		Status:     result.StatusOK,
		RoundsUsed: 1,
		Tokens:     result.Tokens{In: 10, Out: 5, Total: 15},
		TotalMS:    100,
		Judge: &result.Verdict{
			Pass:    pass,
			Reasons: []string{"测试原因"},
			Rules:   map[string]bool{"status_ok": true},
		},
	}
}

func TestBuildMatrixSortedAndFailures(t *testing.T) {
	cfg := &config.Config{
		Name: "n", Model: "m", Tool: "t", Stage: "full", Round: "r1",
		Axes:  config.Axes{Prompt: []string{"basic", "when"}, Schema: []string{"full"}, Return: []string{"raw"}},
		Fixed: config.Fixed{Prompt: "basic", Schema: "full", Return: "raw"},
	}
	set := &cases.Set{Version: "1", Hash: "abc", Cases: []cases.Case{{ID: "c1"}, {ID: "c2"}}}
	traces := []*result.Trace{
		testTrace("run-1", "c1", "basic", false),
		testTrace("run-2", "c1", "basic", true),
		testTrace("run-3", "c2", "when", true),
	}
	md := Build(cfg, set, traces, "../runs/r1-x/trace")
	if !strings.Contains(md, "## 矩阵汇总") || !strings.Contains(md, "## 单因素对比：prompt") {
		t.Fatalf("报告缺少章节:\n%s", md)
	}
	when := strings.Index(md, "| when | full | raw |")
	basic := strings.Index(md, "| basic | full | raw |")
	if when < 0 || basic < 0 || when > basic {
		t.Fatalf("矩阵未按成功率排序:\n%s", md)
	}
	if !strings.Contains(md, "## 失败反例") || !strings.Contains(md, "| c1 | 1/2 |") {
		t.Fatalf("失败反例不对:\n%s", md)
	}
	if !strings.Contains(md, "../runs/r1-x/trace/run-1.json") {
		t.Fatalf("轨迹链接不对:\n%s", md)
	}
	if !strings.Contains(md, "总 token：in=30 / out=15 / total=45") {
		t.Fatalf("成本汇总不对:\n%s", md)
	}
	if !strings.Contains(md, "prompt=`when`") {
		t.Fatalf("结论没有引用胜出组合:\n%s", md)
	}
	if !strings.Contains(md, "不能跨轮直接比较") {
		t.Fatalf("结论缺少边界说明:\n%s", md)
	}
}

func TestBuildEmpty(t *testing.T) {
	md := Build(nil, nil, nil, "")
	if !strings.Contains(md, "没有可分析的样本") {
		t.Fatalf("空数据应明确说明:\n%s", md)
	}
}
