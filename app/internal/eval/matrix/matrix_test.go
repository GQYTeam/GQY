package matrix

import (
	"reflect"
	"strings"
	"testing"

	"gqy/internal/eval/config"
)

func testConfig(stage string) *config.Config {
	return &config.Config{
		Name: "t", Tool: "x", Model: "m", Cases: "cases.json",
		Stage: stage, Round: "r1", Repeats: 1, Concurrency: 1, MaxRounds: 3,
		Axes: config.Axes{
			Prompt: []string{"basic", "when"},
			Schema: []string{"full", "min"},
			Return: []string{"raw", "summary"},
		},
		Fixed: config.Fixed{Prompt: "basic", Schema: "full", Return: "raw"},
	}
}

func TestSingle(t *testing.T) {
	exps := Generate(testConfig(config.StageSingle))
	want := []struct {
		prompt, schema, ret, axis string
	}{
		{"basic", "full", "raw", "baseline"},
		{"when", "full", "raw", "prompt"},
		{"basic", "min", "raw", "schema"},
		{"basic", "full", "summary", "return"},
	}
	if len(exps) != len(want) {
		t.Fatalf("single 数量=%d, want %d: %+v", len(exps), len(want), exps)
	}
	for i, w := range want {
		e := exps[i]
		if e.Prompt != w.prompt || e.Schema != w.schema || e.Return != w.ret || e.Axis != w.axis {
			t.Fatalf("第 %d 个: %+v, want %+v", i, e, w)
		}
		if e.Round != "r1" {
			t.Fatalf("round 标签丢失: %+v", e)
		}
	}
}

func TestPair(t *testing.T) {
	exps := Generate(testConfig(config.StagePair))
	// baseline + prompt×schema + prompt×return + schema×return
	if len(exps) != 4 {
		t.Fatalf("pair 数量=%d: %+v", len(exps), exps)
	}
	axes := []string{exps[0].Axis, exps[1].Axis, exps[2].Axis, exps[3].Axis}
	want := []string{"baseline", "prompt+schema", "prompt+return", "schema+return"}
	if !reflect.DeepEqual(axes, want) {
		t.Fatalf("axes=%v, want %v", axes, want)
	}
	checks := []struct{ prompt, schema, ret string }{
		{"when", "min", "raw"},
		{"when", "full", "summary"},
		{"basic", "min", "summary"},
	}
	for i, want := range checks {
		e := exps[i+1]
		if e.Prompt != want.prompt || e.Schema != want.schema || e.Return != want.ret {
			t.Fatalf("pair 组合不对: %+v, want %+v", e, want)
		}
	}
}

func TestFullAndDedup(t *testing.T) {
	exps := Generate(testConfig(config.StageFull))
	if len(exps) != 8 {
		t.Fatalf("full 数量=%d", len(exps))
	}
	seen := map[string]bool{}
	for i, e := range exps {
		if e.Axis != "full" || e.Round != "r1" || e.Index != i {
			t.Fatalf("full 标签不对: %+v", e)
		}
		if seen[e.Key()] {
			t.Fatalf("组合重复: %+v", e)
		}
		seen[e.Key()] = true
	}

	cfg := testConfig(config.StageFull)
	cfg.Axes = config.Axes{Prompt: []string{"basic"}, Schema: []string{"full"}, Return: []string{"raw"}}
	if got := Generate(cfg); len(got) != 1 {
		t.Fatalf("单值全组合应去重为 1，实际 %d", len(got))
	}
}

func TestDeterministic(t *testing.T) {
	cfg := testConfig(config.StageFull)
	a := Generate(cfg)
	b := Generate(cfg)
	if !reflect.DeepEqual(a, b) {
		t.Fatal("矩阵顺序不确定")
	}
}

func TestFilters(t *testing.T) {
	exps := Generate(testConfig(config.StageFull))
	f, err := ParseFilter("prompt=when")
	if err != nil {
		t.Fatal(err)
	}
	f2, err := ParseFilter("schema=min")
	if err != nil {
		t.Fatal(err)
	}
	got, err := Apply(exps, []Filter{f, f2})
	if err != nil {
		t.Fatal(err)
	}
	// prompt=when 且 schema=min：return 两个值。
	if len(got) != 2 {
		t.Fatalf("过滤数量=%d: %+v", len(got), got)
	}
	for i, e := range got {
		if e.Prompt != "when" || e.Schema != "min" || e.Index != i {
			t.Fatalf("过滤结果不对: %+v", e)
		}
	}

	axisF, _ := ParseFilter("axis=baseline")
	got, byAxis := Apply(exps, []Filter{axisF})
	if byAxis != nil || len(got) != 0 {
		t.Fatalf("full 阶段没有 baseline 标签，应为 0: %+v", got)
	}

	if _, err := ParseFilter("prompt"); err == nil {
		t.Fatal("期望缺少 = 的错误")
	}
	if _, err := ParseFilter("prompt="); err == nil {
		t.Fatal("期望空值错误")
	}
	f3, _ := ParseFilter("nope=x")
	if _, err := Apply(exps, []Filter{f3}); err == nil || !strings.Contains(err.Error(), "未知键") {
		t.Fatalf("期望未知键错误，实际: %v", err)
	}
	f4, _ := ParseFilter("schema=wat")
	if _, err := Apply(exps, []Filter{f4}); err == nil || !strings.Contains(err.Error(), "未知变体") {
		t.Fatalf("期望未知变体错误，实际: %v", err)
	}
	f5, _ := ParseFilter("case=a,b")
	if _, err := Apply(exps, []Filter{f5}); err != nil {
		t.Fatalf("case 过滤不应影响矩阵: %v", err)
	}
	if got := CaseValues([]Filter{f5}); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("CaseValues=%v", got)
	}
}
