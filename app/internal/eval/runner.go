package eval

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"gqy/internal/provider"
	"gqy/internal/tool"
)

// Completer 是评测网关依赖的模型契约。
type Completer interface {
	Complete(ctx context.Context, req provider.Request) (provider.Message, *provider.Usage, error)
}

// ToolCallRecord 是一次工具调用。
type ToolCallRecord struct {
	Name        string `json:"name"`
	ArgsRaw     string `json:"args_raw"`
	SchemaValid bool   `json:"schema_valid"`
	ExecOK      bool   `json:"exec_ok"`
	RetRaw      string `json:"ret_raw,omitempty"`
	RetRendered string `json:"ret_rendered"`
	LatencyMS   int64  `json:"latency_ms"`
	Error       string `json:"error,omitempty"`
}

// JudgeResult 是规则判定结果。
type JudgeResult struct {
	Pass    bool            `json:"pass"`
	Reasons []string        `json:"reasons,omitempty"`
	Rules   map[string]bool `json:"rules"`
}

// RunRecord 是一次完整执行。
type RunRecord struct {
	RunID       string             `json:"run_id"`
	CaseID      string             `json:"case_id"`
	Case        Case               `json:"case"`
	Repeat      int                `json:"repeat_idx"`
	Config      Variant            `json:"config"`
	Round       string             `json:"round"`
	Axis        string             `json:"axis"`
	Messages    []provider.Message `json:"messages"`
	ToolCalls   []ToolCallRecord   `json:"tool_calls"`
	FinalAnswer string             `json:"final_answer"`
	RoundsUsed  int                `json:"rounds_used"`
	Tokens      struct {
		In  int `json:"in"`
		Out int `json:"out"`
	} `json:"tokens"`
	TotalMS int64       `json:"total_ms"`
	Judge   JudgeResult `json:"judge"`
	Status  string      `json:"status"`
}

// Runner 执行矩阵。
type Runner struct {
	Completer Completer
	Registry  *tool.Registry
}

// Run 跑完 variants × cases × repeats。
func (r *Runner) Run(ctx context.Context, cfg Config, set CaseSet, variants []Variant) ([]RunRecord, error) {
	t, ok := r.Registry.Get(cfg.Tool)
	if !ok {
		return nil, fmt.Errorf("工具不存在：%s", cfg.Tool)
	}
	var out []RunRecord
	for _, v := range variants {
		for _, c := range set.Cases {
			for repeat := 0; repeat < cfg.Repeats; repeat++ {
				rec := r.runOne(ctx, cfg, v, c, repeat, t)
				out = append(out, rec)
			}
		}
	}
	return out, nil
}

func (r *Runner) runOne(ctx context.Context, cfg Config, v Variant, c Case, repeat int, t tool.Tool) RunRecord {
	start := time.Now()
	rec := RunRecord{
		RunID:  fmt.Sprintf("%s__%s__%d", v.Key(), c.ID, repeat),
		CaseID: c.ID, Case: c, Repeat: repeat, Config: v, Round: v.Round, Axis: v.Axis,
		Status: "ok",
	}
	prompt := PromptFor(v.Prompt, t)
	toolName, schema := ToolSchemaFor(t, v.Schema)
	if prompt != "" {
		rec.Messages = append(rec.Messages, provider.Message{Role: "system", Content: prompt})
	}
	rec.Messages = append(rec.Messages, provider.Message{Role: "user", Content: c.Input})

	var lastContent string
	for round := 1; round <= cfg.MaxRounds; round++ {
		rec.RoundsUsed = round
		resp, usage, err := r.Completer.Complete(ctx, provider.Request{
			Messages: rec.Messages,
			Model:    cfg.Model,
			Tools:    []any{schema},
		})
		if usage != nil {
			rec.Tokens.In += usage.PromptTokens
			rec.Tokens.Out += usage.CompletionTokens
		}
		if err != nil {
			rec.Status = "gateway_error"
			rec.Judge = Judge(c, rec.ToolCalls, lastContent, rec.Status)
			rec.TotalMS = time.Since(start).Milliseconds()
			return rec
		}
		if resp.Content != "" {
			lastContent = resp.Content
		}
		rec.Messages = append(rec.Messages, resp)
		if len(resp.ToolCalls) == 0 {
			break
		}
		for _, tc := range resp.ToolCalls {
			actual := tc.Function.Name
			if toolName != t.Name() && actual == toolName {
				actual = t.Name()
			}
			rec.ToolCalls = append(rec.ToolCalls, r.execute(ctx, cfg, c, actual, tc, v.Return))
			last := rec.ToolCalls[len(rec.ToolCalls)-1]
			rec.Messages = append(rec.Messages, provider.Message{
				Role:       "tool",
				Content:    last.RetRendered,
				ToolCallID: tc.ID,
			})
		}
		if round == cfg.MaxRounds {
			rec.Status = "not_converged"
		}
	}
	rec.FinalAnswer = lastContent
	rec.Judge = Judge(c, rec.ToolCalls, lastContent, rec.Status)
	rec.TotalMS = time.Since(start).Milliseconds()
	return rec
}

func (r *Runner) execute(ctx context.Context, cfg Config, c Case, actual string, tc provider.ToolCall, returnVariant string) ToolCallRecord {
	start := time.Now()
	out := ToolCallRecord{Name: actual, ArgsRaw: tc.Function.Arguments}
	var args map[string]any
	if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
		out.ArgsRaw = tc.Function.Arguments
		args = map[string]any{}
	} else {
		out.SchemaValid = validateArgs(r.Registry, actual, args)
	}
	if actual != cfg.Tool {
		out.Error = "模型调用了非目标工具"
		out.RetRendered = RenderReturn("", errors.New(out.Error), returnVariant)
		out.LatencyMS = time.Since(start).Milliseconds()
		return out
	}

	switch c.Inject {
	case "empty":
		out.ExecOK = true
	case "error":
		out.Error = "injected error"
	case "timeout":
		out.Error = "context deadline exceeded (injected)"
	case "slow":
		select {
		case <-time.After(200 * time.Millisecond):
			out.ExecOK = true
			out.RetRaw = "slow result"
		case <-ctx.Done():
			out.Error = ctx.Err().Error()
		}
	default:
		t, ok := r.Registry.Get(actual)
		if !ok {
			out.Error = "未知工具"
		} else {
			runCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			raw, err := t.Execute(runCtx, args)
			cancel()
			if err != nil {
				out.Error = err.Error()
			} else {
				out.ExecOK = true
				out.RetRaw = raw
			}
		}
	}
	if out.Error != "" && out.RetRendered == "" {
		out.RetRendered = RenderReturn("", errors.New(out.Error), returnVariant)
	} else if out.RetRendered == "" {
		out.RetRendered = RenderReturn(out.RetRaw, nil, returnVariant)
	}
	out.LatencyMS = time.Since(start).Milliseconds()
	return out
}

func validateArgs(reg *tool.Registry, name string, args map[string]any) bool {
	t, ok := reg.Get(name)
	if !ok {
		return false
	}
	schema := t.Schema()
	required, _ := schema["required"].([]any)
	for _, r := range required {
		key, _ := r.(string)
		if _, ok := args[key]; !ok {
			return false
		}
	}
	return true
}

// Judge 按用例约束判定。
func Judge(c Case, calls []ToolCallRecord, final, status string) JudgeResult {
	j := JudgeResult{Pass: true, Rules: map[string]bool{}}
	fail := func(name, reason string) {
		j.Rules[name] = false
		j.Pass = false
		j.Reasons = append(j.Reasons, reason)
	}
	if status == "gateway_error" {
		fail("status", "网关错误")
	} else if status == "not_converged" {
		fail("status", "超过轮次上限")
	}

	called := len(calls) > 0
	if c.Expect.ShouldCall != nil {
		if *c.Expect.ShouldCall && !called {
			fail("should_call", "期望调用工具，但没有调用")
		} else if !*c.Expect.ShouldCall && called {
			fail("should_call", "期望不调用工具，但调用了")
		} else {
			j.Rules["should_call"] = true
		}
	}
	if c.Expect.MustNotCall && called {
		fail("must_not_call", "禁止调用工具，但调用了")
	}
	if c.Expect.Tool != "" {
		hit := false
		for _, call := range calls {
			if call.Name == c.Expect.Tool {
				hit = true
			}
		}
		if !hit {
			fail("tool", "没有调用期望工具 "+c.Expect.Tool)
		} else {
			j.Rules["tool"] = true
		}
	}
	for key, want := range c.Expect.ArgContains {
		hit := false
		for _, call := range calls {
			var args map[string]any
			if json.Unmarshal([]byte(call.ArgsRaw), &args) == nil {
				if got, ok := args[key]; ok && strings.Contains(fmt.Sprint(got), want) {
					hit = true
				}
			}
			if hit {
				break
			}
		}
		if !hit {
			fail("arg:"+key, "参数 "+key+" 未包含 "+want)
		} else {
			j.Rules["arg:"+key] = true
		}
	}
	for _, want := range c.Expect.FinalContains {
		if !strings.Contains(final, want) {
			fail("final:"+want, "最终答案缺少 "+want)
		} else {
			j.Rules["final:"+want] = true
		}
	}
	return j
}
