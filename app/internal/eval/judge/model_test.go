package judge

import (
	"context"
	"strings"
	"testing"

	"gqy/internal/eval/cases"
	"gqy/internal/eval/result"
	"gqy/internal/provider"
)

type fakeJudge struct{ content string }

func (f fakeJudge) Complete(ctx context.Context, req provider.Request) (provider.Message, *provider.Usage, error) {
	return provider.Message{Role: "assistant", Content: f.content}, &provider.Usage{}, nil
}

func TestModelJudge(t *testing.T) {
	tr := &result.Trace{FinalAnswer: "结果是 5", ToolCalls: []result.ToolCallRecord{{Name: "add", ArgsRaw: `{"a":2,"b":3}`}}}
	cs := cases.Case{ID: "c", Input: "2+3", Expect: cases.Expect{FinalContains: []string{"5"}}}
	v := Model(context.Background(), fakeJudge{content: "```json\n{\"pass\":true,\"reasons\":[\"ok\"]}\n```"}, "judge-model", cs, tr)
	if v == nil || !v.Pass || v.Model != "judge-model" || len(v.Reasons) != 1 {
		t.Fatalf("verdict: %+v", v)
	}
	v = Model(context.Background(), fakeJudge{content: "not json"}, "judge-model", cs, tr)
	if v == nil || v.Error == "" {
		t.Fatalf("invalid json: %+v", v)
	}
	if Model(context.Background(), fakeJudge{}, "", cs, tr) != nil {
		t.Fatal("空模型应返回 nil")
	}
	_ = strings.TrimSpace("")
}
