package eval

import (
	"encoding/json"
	"strings"
)

// JudgeRules 执行五条规则判定（E5）。
func JudgeRules(tc Case, final string, calls []ToolCallRecord) RuleJudge {
	judge := RuleJudge{Pass: true, Reasons: []string{}}
	fail := func(reason string) {
		judge.Pass = false
		judge.Reasons = append(judge.Reasons, reason)
	}

	if tc.Expect.ShouldCall != nil {
		has := len(calls) > 0
		if *tc.Expect.ShouldCall != has {
			fail("should_call 不满足")
		}
	}
	if tc.Expect.Tool != "" {
		found := false
		for _, c := range calls {
			if c.Name == tc.Expect.Tool {
				found = true
			}
		}
		if !found {
			fail("未调用工具 " + tc.Expect.Tool)
		}
	}
	if len(tc.Expect.ArgContains) > 0 {
		for _, c := range calls {
			var args map[string]any
			if json.Unmarshal([]byte(c.ArgsRaw), &args) == nil {
				for key, want := range tc.Expect.ArgContains {
					got, ok := args[key]
					if !ok || !valueContains(got, want) {
						fail("参数不包含 " + key)
					}
				}
			}
		}
	}
	if len(tc.Expect.FinalContain) > 0 {
		for _, want := range tc.Expect.FinalContain {
			if !strings.Contains(final, want) {
				fail("最终答案缺少 " + want)
			}
		}
	}
	if tc.Expect.MustNotCall != "" {
		for _, c := range calls {
			if c.Name == tc.Expect.MustNotCall {
				fail("不应调用工具 " + tc.Expect.MustNotCall)
			}
		}
	}
	return judge
}

func valueContains(got, want any) bool {
	g, _ := json.Marshal(got)
	w, _ := json.Marshal(want)
	return strings.Contains(string(g), string(w))
}
