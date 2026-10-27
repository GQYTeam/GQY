// 包 agent：agent loop —— 模型 ↔ 工具的多轮调用闭环。
//
// 流程：发消息 → 拿完整响应 → 若有工具调用则执行工具 → 回填 → 循环 → 最终答案。
// 工具执行结果通过回调吐给上层（HTTP 层转成 SSE tool 事件流）。
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
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
	// IncludeExternal 为 true 时把外部工具也注入模型；默认只注入核心工具。
	IncludeExternal bool
}

// ToolsSchema 把核心工具转成 OpenAI function calling 的 tools 数组。
// 外部工具（插件/skill/mcp）默认不注入，需要时通过 EnableExternal 开启。
func (l *Loop) ToolsSchema() []any {
	tools := l.Registry.ListCore()
	if l.IncludeExternal {
		tools = l.Registry.List()
	}
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

// systemPrompt 告诉模型它有哪些工具、怎么用。
// 没有它，模型会凭「自我认知」声称自己没有工具（即使 tools 已随请求发出）。
func (l *Loop) systemPrompt() string {
	var b strings.Builder
	b.WriteString("你是顾清影，运行在本机（macOS）的 AI 助手，拥有以下可主动调用的工具：\n")
	tools := l.Registry.ListCore()
	if l.IncludeExternal {
		tools = l.Registry.List()
	}
	for _, t := range tools {
		fmt.Fprintf(&b, "- %s: %s\n", t.Name(), t.Description())
	}
	b.WriteString(`
使用规则：
- 需要真实信息（文件内容、目录、命令输出）时必须调用工具获取，不要凭空编造。
- 被问到「你有没有工具/能不能联网」这类关于自身能力的问题时，回答「有」并按需演示。
- 工具结果已由系统执行并回填，直接基于结果继续推理。
- 简单寒暄或纯创作类问题不需要调用工具。`)
	return b.String()
}

// Run 跑一轮 agent loop：从 messages 出发，直到模型返回最终答案、需要询问用户、或达到轮次上限。
// 返回最终的 assistant 消息、累积 usage、以及可能的 ask（需要询问用户时非 nil）。
func (l *Loop) Run(ctx context.Context, messages []provider.Message, model, effort string) (provider.Message, *provider.Usage, *tool.Ask, error) {
	totalUsage := &provider.Usage{}
	msgs := make([]provider.Message, 0, len(messages)+1)
	// 系统提示插最前；若用户消息里已带 system 则替换
	hasSystem := len(messages) > 0 && messages[0].Role == "system"
	if !hasSystem {
		msgs = append(msgs, provider.Message{Role: "system", Content: l.systemPrompt()})
	}
	msgs = append(msgs, messages...)

	for round := 0; round < maxRounds; round++ {
		if ctx.Err() != nil {
			return provider.Message{}, totalUsage, nil, ctx.Err()
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
			return assistant, totalUsage, nil, err
		}

		// 没有工具调用 → 最终答案
		if len(assistant.ToolCalls) == 0 {
			return assistant, totalUsage, nil, nil
		}

		// 有工具调用 → 执行并回填
		msgs = append(msgs, assistant)
		for _, tc := range assistant.ToolCalls {
			result := l.execute(ctx, tc)
			if l.OnTool != nil {
				l.OnTool(result)
			}
			// ask_user：中断，把问题抛给上层
			var askErr *tool.ErrAskUser
			if errors.As(result.Error, &askErr) {
				return provider.Message{}, totalUsage, &askErr.Ask, nil
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
	return provider.Message{}, totalUsage, nil, fmt.Errorf("工具调用超过 %d 轮，已中止", maxRounds)
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
