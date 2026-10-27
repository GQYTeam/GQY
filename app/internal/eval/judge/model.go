package judge

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"gqy/internal/eval/cases"
	"gqy/internal/eval/result"
	"gqy/internal/provider"
)

// Completer 是模型裁判依赖的最小上游契约。
type Completer interface {
	Complete(ctx context.Context, req provider.Request) (provider.Message, *provider.Usage, error)
}

// Model 用固定裁判模型给最终答案打分。模型裁判只作为补充，不覆盖规则判定。
func Model(ctx context.Context, c Completer, model string, cs cases.Case, tr *result.Trace) *result.ModelVerdict {
	v := &result.ModelVerdict{Model: model}
	if model == "" || c == nil {
		return nil
	}
	prompt := fmt.Sprintf(`你是工具调用评测的裁判。只输出 JSON，不要 Markdown。
用户输入：%s
期望行为：%s
最终答案：%s
工具调用：%s

判断最终答案是否满足用户意图，并给出理由。输出 {"pass":true|false,"reasons":["..."]}。`,
		cs.Input, expectationJSON(cs.Expect), tr.FinalAnswer, callsSummary(tr))
	msg, _, err := c.Complete(ctx, provider.Request{
		Model:  model,
		Effort: "Low",
		Messages: []provider.Message{
			{Role: "system", Content: "你是严格的评测裁判，只根据给定材料判断，不补充事实。"},
			{Role: "user", Content: prompt},
		},
	})
	if err != nil {
		v.Error = err.Error()
		return v
	}
	raw := stripFence(msg.Content)
	var parsed struct {
		Pass    bool     `json:"pass"`
		Reasons []string `json:"reasons"`
	}
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		v.Error = "裁判输出不是合法 JSON"
		return v
	}
	v.Pass = parsed.Pass
	v.Reasons = parsed.Reasons
	if v.Reasons == nil {
		v.Reasons = []string{}
	}
	return v
}

func expectationJSON(e cases.Expect) string {
	b, _ := json.Marshal(e)
	return string(b)
}

func callsSummary(tr *result.Trace) string {
	if len(tr.ToolCalls) == 0 {
		return "无"
	}
	parts := make([]string, 0, len(tr.ToolCalls))
	for _, c := range tr.ToolCalls {
		parts = append(parts, c.Name+"("+c.ArgsRaw+")")
	}
	return strings.Join(parts, ", ")
}

func stripFence(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSuffix(s, "```")
	return strings.TrimSpace(s)
}
