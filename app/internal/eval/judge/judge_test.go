package judge

import (
	"testing"

	"gqy/internal/eval/cases"
	"gqy/internal/eval/result"
)

func boolp(v bool) *bool { return &v }

func trace(calls []result.ToolCallRecord, final, status string) *result.Trace {
	return &result.Trace{
		CaseID:      "c",
		ToolCalls:   calls,
		FinalAnswer: final,
		Status:      result.Status(status),
	}
}

func fullCase() cases.Case {
	return cases.Case{
		ID: "c",
		Expect: cases.Expect{
			ShouldCall:    boolp(true),
			Tool:          "plugin__echo__echo",
			ArgContains:   map[string]any{"text": "hello"},
			FinalContains: []string{"hello"},
		},
	}
}

func goodCall() result.ToolCallRecord {
	return result.ToolCallRecord{
		Name:        "plugin__echo__echo",
		ArgsParsed:  map[string]any{"text": "hello"},
		SchemaValid: true,
		ExecOK:      true,
	}
}

func TestPassAllRules(t *testing.T) {
	tr := trace([]result.ToolCallRecord{goodCall()}, "结果是 hello", "ok")
	v := Judge(fullCase(), tr, true)
	if !v.Pass || v.Runtime || len(v.Reasons) != 0 {
		t.Fatalf("应全通过: %+v", v)
	}
	for _, rule := range []string{"status_ok", "should_call", "expected_tool", "arg_contains", "final_contains", "schema_valid"} {
		if !v.Rules[rule] {
			t.Fatalf("规则 %s 未通过: %+v", rule, v.Rules)
		}
	}
}

func TestMustCallAlias(t *testing.T) {
	c := cases.Case{Expect: cases.Expect{MustCall: boolp(true)}}
	if v := Judge(c, trace(nil, "", "ok"), false); v.Pass || v.Rules["should_call"] {
		t.Fatalf("must_call=true 时零调用应失败: %+v", v)
	}
	if v := Judge(c, trace([]result.ToolCallRecord{goodCall()}, "", "ok"), false); !v.Pass {
		t.Fatalf("must_call=true 时有调用应通过: %+v", v)
	}
}

func TestShouldCallFailures(t *testing.T) {
	tr := trace(nil, "hello", "ok")
	v := Judge(fullCase(), tr, true)
	if v.Pass || v.Rules["should_call"] || v.Rules["expected_tool"] {
		t.Fatalf("没有调用应失败: %+v", v)
	}
}

func TestMustNotCall(t *testing.T) {
	c := cases.Case{Expect: cases.Expect{MustNotCall: true}}
	v := Judge(c, trace([]result.ToolCallRecord{goodCall()}, "x", "ok"), false)
	if v.Pass || v.Rules["must_not_call"] {
		t.Fatalf("must_not_call 应失败: %+v", v)
	}
	v = Judge(c, trace(nil, "x", "ok"), false)
	if !v.Pass {
		t.Fatalf("零调用应通过: %+v", v)
	}
}

func TestExpectedTool(t *testing.T) {
	c := fullCase()
	call := goodCall()
	call.Name = "other"
	v := Judge(c, trace([]result.ToolCallRecord{call}, "hello", "ok"), true)
	if v.Pass || v.Rules["expected_tool"] {
		t.Fatalf("工具选错应失败: %+v", v)
	}
}

func TestArgConstraints(t *testing.T) {
	c := fullCase()
	call := goodCall()
	call.ArgsParsed = map[string]any{"text": "bye"}
	v := Judge(c, trace([]result.ToolCallRecord{call}, "hello", "ok"), true)
	if v.Pass || v.Rules["arg_contains"] {
		t.Fatalf("参数不满足应失败: %+v", v)
	}

	c.Expect.ArgContains = map[string]any{"nested.id": 1.0}
	call.ArgsParsed = map[string]any{"nested": map[string]any{"id": 1}}
	v = Judge(c, trace([]result.ToolCallRecord{call}, "hello", "ok"), true)
	if !v.Pass {
		t.Fatalf("嵌套参数应通过: %+v", v)
	}

	c.Expect.ArgContains = map[string]any{"tags": []any{"a", "b"}}
	call.ArgsParsed = map[string]any{"tags": []any{"c", "a", "b"}}
	v = Judge(c, trace([]result.ToolCallRecord{call}, "hello", "ok"), true)
	if !v.Pass {
		t.Fatalf("数组包含约束应通过: %+v", v)
	}
}

func TestFinalFactsCaseInsensitive(t *testing.T) {
	c := cases.Case{Expect: cases.Expect{FinalContains: []string{"Hello", "世界"}}}
	v := Judge(c, trace(nil, "hello 世界", "ok"), false)
	if !v.Pass {
		t.Fatalf("关键事实应大小写不敏感: %+v", v)
	}
	v = Judge(c, trace(nil, "hello", "ok"), false)
	if v.Pass || v.Rules["final_contains"] {
		t.Fatalf("缺少事实应失败: %+v", v)
	}
}

func TestRuntimeSeparated(t *testing.T) {
	tr := trace(nil, "", "timeout")
	tr.Error = "context deadline exceeded"
	v := Judge(cases.Case{}, tr, false)
	if v.Pass || !v.Runtime || v.Rules["status_ok"] {
		t.Fatalf("运行时错误应单独标记: %+v", v)
	}
}

func TestSchemaValidityOnlyRequiredInStrict(t *testing.T) {
	c := cases.Case{Expect: cases.Expect{FinalContains: []string{"ok"}}}
	call := goodCall()
	call.SchemaValid = false
	tr := trace([]result.ToolCallRecord{call}, "ok", "ok")

	if v := Judge(c, tr, false); !v.Pass || v.Rules["schema_valid"] {
		t.Fatalf("非严格模式不应因 schema 失败: %+v", v)
	}
	if v := Judge(c, tr, true); v.Pass || v.Rules["schema_valid"] {
		t.Fatalf("严格模式应失败: %+v", v)
	}
}

func TestNoExpectation(t *testing.T) {
	v := Judge(cases.Case{}, trace(nil, "whatever", "ok"), false)
	if !v.Pass || !v.Rules["status_ok"] || !v.Rules["schema_valid"] {
		t.Fatalf("无期望时应只看状态与 schema 合法性: %+v", v)
	}
}
