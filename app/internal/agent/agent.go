// 包 agent：agent loop —— 模型 ↔ 工具的多轮调用闭环。
//
// 流程：发消息 → 拿完整响应 → 若有工具调用则执行工具 → 回填 → 循环 → 最终答案。
// 工具执行结果通过回调吐给上层（HTTP 层转成 SSE tool 事件流）。
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"gqy/internal/provider"
	"gqy/internal/tool"
)

const maxRounds = 20 // 防死循环

// ToolResult 是一次工具调用的执行结果，用于回填模型 + 上报 UI。
type ToolResult struct {
	Name      string
	Arguments string
	Output    string
	Error     error
	Duration  time.Duration
}

// OnTool 是可选的工具执行回调（每个工具调用执行后触发）。
type OnTool func(ToolResult)

type Loop struct {
	Upstream *provider.Upstream
	Registry *tool.Registry
	OnTool   OnTool
}

// ToolsSchema 把注册表里的工具转成 OpenAI function calling 的 tools 数组。
func (l *Loop) ToolsSchema() []any {
	tools := l.Registry.List()
	out := make([]any, 0, len(tools))
	for _, t := range tools {
		out = append(out, map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        t.Name(),
				"description": t.Description(),
				"parameters":  t.Schema(),
			},
		})
	}
	return out
}

// Run 跑一轮 agent loop：从 messages 出发，直到模型返回最终答案或达到轮次上限。
// 返回最终的 assistant 消息（文本 + 累积 usage）。
func (l *Loop) Run(ctx context.Context, messages []provider.Message, model, effort string) (provider.Message, *provider.Usage, error) {
	totalUsage := &provider.Usage{}
	msgs := append([]provider.Message(nil), messages...)

	for round := 0; round < maxRounds; round++ {
		if ctx.Err() != nil {
			return provider.Message{}, totalUsage, ctx.Err()
		}
		assistant, usage, err := l.Upstream.Complete(ctx, provider.Request{
			Messages: msgs,
			Model:    model,
			Effort:   effort,
			Tools:    l.ToolsSchema(),
		})
		if usage != nil {
			totalUsage.PromptTokens += usage.PromptTokens
			totalUsage.CompletionTokens += usage.CompletionTokens
			totalUsage.TotalTokens += usage.TotalTokens
		}
		if err != nil {
			return assistant, totalUsage, err
		}

		// 没有工具调用 → 最终答案
		if len(assistant.ToolCalls) == 0 {
			return assistant, totalUsage, nil
		}

		// 有工具调用 → 执行并回填
		msgs = append(msgs, assistant)
		for _, tc := range assistant.ToolCalls {
			result := l.execute(ctx, tc)
			if l.OnTool != nil {
				l.OnTool(result)
			}
			role := "tool"
			content := result.Output
			if result.Error != nil {
				content = "错误: " + result.Error.Error()
			}
			msgs = append(msgs, provider.Message{
				Role:       role,
				Content:    content,
				ToolCallID: tc.ID,
			})
		}
	}
	return provider.Message{}, totalUsage, fmt.Errorf("工具调用超过 %d 轮，已中止", maxRounds)
}

func (l *Loop) execute(ctx context.Context, tc provider.ToolCall) ToolResult {
	start := time.Now()
	result := ToolResult{Name: tc.Function.Name, Arguments: tc.Function.Arguments}

	t, ok := l.Registry.Get(tc.Function.Name)
	if !ok {
		result.Error = fmt.Errorf("未知工具 %q", tc.Function.Name)
		result.Duration = time.Since(start)
		return result
	}

	var args map[string]any
	if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
		// 参数不是合法 JSON 也继续，用空 map 让工具自己报错
		args = map[string]any{"_raw": tc.Function.Arguments}
	}
	out, err := t.Execute(ctx, args)
	result.Output = out
	result.Error = err
	result.Duration = time.Since(start)
	if err != nil {
		log.Printf("[agent] 工具 %s 失败: %v", tc.Function.Name, err)
	}
	return result
}
