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
	"sync"
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

// Completer 是 agent loop 依赖的最小上游契约。
// *provider.Upstream 和 *gateway.Router 都实现它。
type Completer interface {
	Complete(ctx context.Context, req provider.Request) (provider.Message, *provider.Usage, error)
}

type Loop struct {
	Upstream Completer
	Registry *tool.Registry
	OnTool   OnTool
	// IncludeExternal 为 true 时把外部工具也注入模型；默认只注入核心工具。
	IncludeExternal bool
	// ToolTimeout 是单次工具调用的上限；零值表示不额外限制（仍受请求 context 约束）。
	// 超时只影响本次调用，错误回填后 loop 继续。
	ToolTimeout time.Duration
	extMu       sync.RWMutex
}

// SetExternal 运行时切换外部工具注入，供设置页使用。
func (l *Loop) SetExternal(v bool) {
	l.extMu.Lock()
	l.IncludeExternal = v
	l.extMu.Unlock()
}

func (l *Loop) externalEnabled() bool {
	l.extMu.RLock()
	defer l.extMu.RUnlock()
	return l.IncludeExternal
}

// ExternalEnabled 返回当前是否注入外部工具。
func (l *Loop) ExternalEnabled() bool { return l.externalEnabled() }

// ToolsSchema 把核心工具转成 OpenAI function calling 的 tools 数组。
// 外部工具（插件/skill/mcp）默认不注入，需要时通过 EnableExternal 开启。
func (l *Loop) ToolsSchema() []any {
	tools := l.Registry.ListCore()
	if l.externalEnabled() {
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
	if l.externalEnabled() {
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
	// 工具系统提示与客户端自带的 system（人设/记忆/知识）合并成一条，
	// 避免部分上游只读取第一条 system 而丢掉人设/记忆。
	system := l.systemPrompt()
	for _, m := range messages {
		if m.Role == "system" {
			if m.Content != "" {
				system += "\n\n" + m.Content
			}
			continue
		}
		msgs = append(msgs, m)
	}
	msgs = append([]provider.Message{{Role: "system", Content: system}}, msgs...)

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

	// ask_user 是中断 loop 的交互工具，不受单工具超时约束。
	timeout := l.ToolTimeout
	if t.Name() == "ask_user" {
		timeout = 0
	}

	execCtx := ctx
	if timeout > 0 {
		var cancel context.CancelFunc
		execCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	type outcome struct {
		out string
		err error
	}
	// 单独 goroutine + select：即使工具不响应 context，也能按上限返回。
	// 工具若不响应取消，其 goroutine 会自行结束，不阻塞 loop。
	ch := make(chan outcome, 1)
	go func() {
		out, err := t.Execute(execCtx, args)
		ch <- outcome{out: out, err: err}
	}()

	var out string
	var err error
	select {
	case r := <-ch:
		out, err = r.out, r.err
		// 工具可能忽略了 context 并“成功”返回；以截止时间为准确认超时。
		// ask_user 等有意义的返回保持原样。
		if timeout > 0 && errors.Is(execCtx.Err(), context.DeadlineExceeded) &&
			(err == nil || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled)) {
			err = fmt.Errorf("工具 %s 超时（%s），已取消", tc.Function.Name, timeout)
		}
	case <-execCtx.Done():
		if ctx.Err() != nil {
			err = ctx.Err()
		} else {
			err = fmt.Errorf("工具 %s 超时（%s），已取消", tc.Function.Name, timeout)
		}
	}

	result.Output = out
	result.Error = err
	result.Duration = time.Since(start)
	if err != nil {
		log.Printf("[agent] 工具 %s 失败: %v", tc.Function.Name, err)
	}
	return result
}
