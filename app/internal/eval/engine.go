package eval

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"gqy/internal/provider"
	"gqy/internal/tool"
)

// ToolDecl 是工具声明（用于 schema 渲染）。
type ToolDecl struct {
	Name   string         `json:"name"`
	Schema map[string]any `json:"schema"`
}

// Executor 执行一次工具调用（tool / plugin 统一入口）。
type Executor interface {
	Execute(ctx context.Context, name string, args map[string]any) (string, error)
}

// Host 是评测宿主：持有一组工具声明与执行器（E4）。
type Host struct {
	exec  Executor
	decls []ToolDecl
}

// NewHost 构造宿主。
func NewHost(exec Executor, decls []ToolDecl) *Host { return &Host{exec: exec, decls: decls} }

// Decls 返回工具声明。
func (h *Host) Decls() []ToolDecl { return h.decls }

// Execute 执行工具。
func (h *Host) Execute(ctx context.Context, name string, args map[string]any) (string, error) {
	if h.exec == nil {
		return "", fmt.Errorf("eval: 工具 %s 未注册", name)
	}
	return h.exec.Execute(ctx, name, args)
}

// RegistryExecutor 把 tool.Registry 适配成 Executor。
type RegistryExecutor struct {
	Reg *tool.Registry
}

// Execute 从注册表取工具执行。
func (e RegistryExecutor) Execute(ctx context.Context, name string, args map[string]any) (string, error) {
	t, ok := e.Reg.Get(name)
	if !ok {
		return "", tool.ErrNotFound
	}
	return t.Execute(ctx, args)
}

// Rounder 是一次模型往返的抽象（E4）。
type Rounder interface {
	Round(ctx context.Context, model string, messages []provider.Message, tools []provider.Tool) (provider.Response, error)
}

// Engine 是单次对话循环（E4）。
type Engine struct {
	Rounder   Rounder
	Host      *Host
	MaxRounds int
	TimeoutMS int
}

// Run 跑一次「渲染 → 请求 → 工具执行 → 回填 → 最终答案」。
func (e *Engine) Run(ctx context.Context, combo Combo, tc Case, repeatIdx int, round, axis, runID string) *RunResult {
	start := time.Now()
	timeout := time.Duration(e.TimeoutMS) * time.Millisecond
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	result := &RunResult{
		RunID:     runID,
		CaseID:    tc.ID,
		RepeatIdx: repeatIdx,
		Round:     round,
		Axis:      axis,
		Config: map[string]string{
			"prompt": combo.Prompt, "schema": combo.Schema, "return": combo.Return,
		},
		Messages:  []MaskedMessage{},
		ToolCalls: []ToolCallRecord{},
	}

	messages := []provider.Message{
		{Role: provider.RoleSystem, Content: renderPrompt(combo.Prompt)},
		{Role: provider.RoleUser, Content: tc.Input},
	}
	tools := e.toolList(combo.Schema)
	injectCount := 0

	for result.RoundsUsed = 1; result.RoundsUsed <= e.MaxRounds; result.RoundsUsed++ {
		if ctx.Err() != nil {
			result.Status = StatusNotConverge
			break
		}
		resp, err := e.Rounder.Round(ctx, "", messages, tools)
		result.Tokens.In += resp.Usage.PromptTokens
		result.Tokens.Out += resp.Usage.CompletionTokens
		if err != nil {
			result.Status = StatusGatewayErr
			break
		}

		if len(resp.ToolCalls) == 0 {
			result.FinalAnswer = resp.Text
			result.Status = StatusOK
			break
		}

		for _, call := range resp.ToolCalls {
			argsRaw, _ := json.Marshal(call.Arguments)
			rec := ToolCallRecord{Name: call.Name, ArgsRaw: string(argsRaw), SchemaValid: true}
			ts := time.Now()

			// 故障注入
			if tc.Inject != nil && (tc.Inject.Tool == "" || tc.Inject.Tool == call.Name) && injectCount < tc.Inject.Times {
				injectCount++
				out, status := applyInject(ctx, tc.Inject)
				rec.ExecOK = status == ""
				rec.LatencyMS = time.Since(ts).Milliseconds()
				result.ToolCalls = append(result.ToolCalls, rec)
				if status != "" {
					result.Status = status
					result.TotalMS = time.Since(start).Milliseconds()
					return result
				}
				messages = append(messages, provider.Message{Role: provider.RoleTool, ToolCallID: call.ID, Name: call.Name, Content: out})
				continue
			}

			out, execErr := e.Host.Execute(ctx, call.Name, call.Arguments)
			rec.LatencyMS = time.Since(ts).Milliseconds()
			if execErr != nil {
				rec.ExecOK = false
				result.ToolCalls = append(result.ToolCalls, rec)
				result.Status = StatusExecErr
				result.TotalMS = time.Since(start).Milliseconds()
				return result
			}
			rec.ExecOK = true
			rec.RetRendered = renderReturn(combo.Return, out)
			result.ToolCalls = append(result.ToolCalls, rec)
			messages = append(messages, provider.Message{Role: provider.RoleTool, ToolCallID: call.ID, Name: call.Name, Content: rec.RetRendered})
		}
	}

	if result.Status == "" {
		result.Status = StatusNotConverge
	}
	result.TotalMS = time.Since(start).Milliseconds()
	for _, m := range messages {
		result.Messages = append(result.Messages, MaskedMessage{Role: string(m.Role), Content: m.Content})
	}
	return result
}

func (e *Engine) toolList(schemaVariant string) []provider.Tool {
	out := make([]provider.Tool, 0, len(e.Host.decls))
	for _, d := range e.Host.decls {
		schema := d.Schema
		if schema == nil {
			schema = map[string]any{"type": "object"}
		}
		out = append(out, provider.Tool{Name: d.Name, Description: "", Parameters: schema})
	}
	_ = schemaVariant
	return out
}

func renderPrompt(variant string) string {
	switch variant {
	case "none":
		return ""
	case "when":
		return "你是一个助手。只有在确实需要时才调用工具。"
	case "when-how":
		return "你是一个助手。只有在确实需要时才调用工具，并说明为什么。"
	case "fewshot":
		return "你是一个助手。示例：用户要回显时调用 echo 工具。"
	case "strict":
		return "你是一个助手。严格按工具 schema 传参。"
	default:
		return "你是一个助手。"
	}
}

func renderReturn(variant, out string) string {
	switch variant {
	case "trim":
		return strings.TrimSpace(out)
	case "summary":
		if len(out) > 40 {
			return out[:40] + "…"
		}
		return out
	case "typed":
		return "工具返回: " + out
	case "error-rich":
		return out
	default:
		return out
	}
}

// applyInject 应用故障注入；返回输出与状态（空状态=成功）。
func applyInject(ctx context.Context, inj *Inject) (string, string) {
	switch inj.Mode {
	case "empty":
		return "", ""
	case "error":
		return "", StatusExecErr
	case "timeout":
		<-ctx.Done()
		return "", StatusNotConverge
	case "slow":
		d := time.Duration(inj.DelayMS) * time.Millisecond
		if d <= 0 {
			d = 200 * time.Millisecond
		}
		time.Sleep(d)
		return "slow-ok", ""
	default:
		return "", ""
	}
}
