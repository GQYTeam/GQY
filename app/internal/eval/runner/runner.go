// 包 runner：单次对话循环——渲染 prompt/schema、请求模型、执行工具、渲染回填。
//
// Completer 是最小上游契约，便于测试替身；真实运行时由 CLI 注入
// provider.Upstream 的适配器。工具执行复用 gqy/internal/tool.Registry。
package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"gqy/internal/eval/cases"
	"gqy/internal/eval/config"
	"gqy/internal/eval/matrix"
	"gqy/internal/eval/result"
	"gqy/internal/provider"
	"gqy/internal/tool"
)

// Completer 是评测网关依赖的最小模型上游。
type Completer interface {
	Complete(ctx context.Context, req provider.Request) (provider.Message, *provider.Usage, error)
}

// Options 是单次运行的全部输入。
type Options struct {
	Config     *config.Config
	Case       cases.Case
	Experiment matrix.Experiment
	Registry   *tool.Registry
	Completer  Completer

	RepeatIdx   int
	RunID       string
	ModelVer    string
	CaseSetHash string

	// Injector 为空时按用例的 inject 字段自动构造；测试可替换。
	Injector Injector
	Now      func() time.Time
}

// RunOne 执行一次「用例 × 配置 × 重复」，返回完整轨迹。
// 返回的 error 只代表装配失败（工具未注册、变体非法等），运行期错误写进 Trace.Status。
func RunOne(ctx context.Context, opts Options) (*result.Trace, error) {
	if opts.Config == nil {
		return nil, errors.New("runner: Config 不能为空")
	}
	if opts.Registry == nil {
		return nil, errors.New("runner: Registry 不能为空")
	}
	if opts.Completer == nil {
		return nil, errors.New("runner: Completer 不能为空")
	}
	cfg := opts.Config
	now := opts.Now
	if now == nil {
		now = time.Now
	}

	realTool, ok := opts.Registry.Get(cfg.Tool)
	if !ok {
		return nil, fmt.Errorf("工具 %q 未注册", cfg.Tool)
	}
	view, err := RenderToolSchema(realTool, opts.Experiment.Schema)
	if err != nil {
		return nil, err
	}
	system, err := RenderSystemPrompt(opts.Experiment.Prompt, []ToolView{view})
	if err != nil {
		return nil, err
	}

	runID := opts.RunID
	if runID == "" {
		runID = result.NewRunID(cfg.Round, opts.Case.ID, 0)
	}
	tr := &result.Trace{
		RunID:     runID,
		CaseID:    opts.Case.ID,
		RepeatIdx: opts.RepeatIdx,
		Case:      opts.Case,
		Config: result.RunConfig{
			Prompt:       opts.Experiment.Prompt,
			Schema:       opts.Experiment.Schema,
			Return:       opts.Experiment.Return,
			Model:        cfg.Model,
			ModelVer:     opts.ModelVer,
			GatewayVer:   result.GatewayVersion,
			Tool:         cfg.Tool,
			CaseSetHash:  opts.CaseSetHash,
			StrictSchema: cfg.StrictSchema,
			JudgeVer:     result.JudgeVersion,
		},
		Round:     opts.Experiment.Round,
		Axis:      opts.Experiment.Axis,
		StartedAt: now(),
	}

	injector := opts.Injector
	if injector == nil && opts.Case.Inject != nil {
		injector = NewCaseInjector(opts.Case.Inject)
	}

	msgs := buildMessages(system, opts.Case)
	tools := []any{toolSchema(view)}

	runCtx := ctx
	var cancel context.CancelFunc
	if cfg.Timeouts.TotalMS > 0 {
		runCtx, cancel = context.WithTimeout(ctx, time.Duration(cfg.Timeouts.TotalMS)*time.Millisecond)
		defer cancel()
	}

	start := now()
	converged := false
	maxRounds := cfg.MaxRounds
	if maxRounds < 1 {
		maxRounds = 1
	}

	for round := 0; round < maxRounds; round++ {
		if err := runCtx.Err(); err != nil {
			tr.Escalate(classifyModelError(runCtx, err))
			tr.AddError("运行超时或取消: " + err.Error())
			break
		}
		reqMsgs := append([]provider.Message(nil), msgs...)
		calledAt := now()
		resp, usage, attempts, callErr := callModel(runCtx, cfg, opts.Completer, provider.Request{
			Model:    cfg.Model,
			Effort:   "Low", // 固定低温，稳定性靠重复次数而不是高温采样。
			Messages: reqMsgs,
			Tools:    tools,
		}, runID, round, tr)
		latency := now().Sub(calledAt).Milliseconds()

		ex := result.Exchange{
			Round:           round,
			RequestMessages: reqMsgs,
			LatencyMS:       latency,
			Attempts:        attempts,
		}
		if callErr != nil {
			ex.Error = callErr.Error()
			tr.Exchanges = append(tr.Exchanges, ex)
			tr.Escalate(classifyModelError(runCtx, callErr))
			tr.AddError("模型请求失败: " + callErr.Error())
			break
		}
		normalizeToolCallIDs(&resp, round)
		if usage != nil {
			tr.Tokens.Add(usage.PromptTokens, usage.CompletionTokens, usage.TotalTokens)
			ex.Usage = result.Tokens{In: usage.PromptTokens, Out: usage.CompletionTokens, Total: usage.TotalTokens}
		}
		ex.Response = resp
		tr.Exchanges = append(tr.Exchanges, ex)

		if len(resp.ToolCalls) == 0 {
			tr.FinalAnswer = resp.Content
			msgs = append(msgs, resp)
			converged = true
			tr.Escalate(result.StatusOK)
			break
		}

		msgs = append(msgs, resp)
		for _, tc := range resp.ToolCalls {
			rec := executeCall(runCtx, cfg, tr, realTool, view, tc, len(tr.ToolCalls), injector)
			tr.ToolCalls = append(tr.ToolCalls, rec)
			msgs = append(msgs, provider.Message{
				Role:       "tool",
				Content:    rec.RetRendered,
				ToolCallID: tc.ID,
			})
		}
		tr.RoundsUsed++
	}

	if !converged && tr.Status != result.StatusGatewayError && tr.Status != result.StatusTimeout {
		tr.Escalate(result.StatusNotConverged)
		tr.AddError(fmt.Sprintf("超过最大轮次 %d，未收敛", maxRounds))
	}
	if tr.Status == "" {
		tr.Status = result.StatusOK
	}
	tr.Messages = msgs
	tr.TotalMS = now().Sub(start).Milliseconds()
	return tr, nil
}

func buildMessages(system string, c cases.Case) []provider.Message {
	var out []provider.Message
	if strings.TrimSpace(system) != "" {
		out = append(out, provider.Message{Role: "system", Content: system})
	}
	if len(c.Messages) > 0 {
		for _, m := range c.Messages {
			out = append(out, provider.Message{Role: m.Role, Content: m.Content})
		}
		return out
	}
	out = append(out, provider.Message{Role: "user", Content: c.Input})
	return out
}

func toolSchema(view ToolView) map[string]any {
	return map[string]any{
		"type": "function",
		"function": map[string]any{
			"name":        view.ExposedName,
			"description": view.Description,
			"parameters":  view.Parameters,
		},
	}
}

func normalizeToolCallIDs(resp *provider.Message, round int) {
	for i := range resp.ToolCalls {
		if resp.ToolCalls[i].ID == "" {
			resp.ToolCalls[i].ID = fmt.Sprintf("call-%d-%d", round, i)
		}
		if resp.ToolCalls[i].Type == "" {
			resp.ToolCalls[i].Type = "function"
		}
	}
}

func executeCall(ctx context.Context, cfg *config.Config, tr *result.Trace, realTool tool.Tool, view ToolView, tc provider.ToolCall, callIdx int, injector Injector) result.ToolCallRecord {
	start := time.Now()
	rec := result.ToolCallRecord{
		Name:     realTool.Name(),
		CalledAs: tc.Function.Name,
		ArgsRaw:  tc.Function.Arguments,
	}

	// 解析参数。
	var args map[string]any
	parseErr := json.Unmarshal([]byte(tc.Function.Arguments), &args)
	if parseErr != nil || args == nil {
		if parseErr == nil {
			parseErr = errors.New("参数必须是 JSON 对象")
		}
	}
	var schemaErrs []string
	if parseErr == nil {
		schemaErrs = ValidateArgs(view.Parameters, args)
	}
	rec.ArgsParsed = args
	rec.SchemaErrors = schemaErrs
	rec.SchemaValid = parseErr == nil && len(schemaErrs) == 0

	// 工具名必须与注入的暴露名或真实名一致。
	if tc.Function.Name != view.ExposedName && tc.Function.Name != view.RealName {
		err := fmt.Errorf("未知工具 %q", tc.Function.Name)
		rec.Name = tc.Function.Name
		rec.Error = err.Error()
		rec.RetRendered = renderOrFallback(tr.Config.Return, "", err)
		rec.LatencyMS = time.Since(start).Milliseconds()
		tr.Escalate(result.StatusExecError)
		tr.AddError(err.Error())
		return rec
	}

	// 严格校验：参数不合法时不执行，让模型看到本地错误。
	if cfg.StrictSchema && !rec.SchemaValid {
		var reasons []string
		if parseErr != nil {
			reasons = append(reasons, parseErr.Error())
		}
		reasons = append(reasons, schemaErrs...)
		err := fmt.Errorf("参数校验失败：%s", strings.Join(reasons, "；"))
		rec.Error = err.Error()
		rec.RetRendered = renderOrFallback(tr.Config.Return, "", err)
		rec.LatencyMS = time.Since(start).Milliseconds()
		tr.Escalate(result.StatusExecError)
		tr.AddError(fmt.Sprintf("工具 %s %s", realTool.Name(), err))
		return rec
	}

	execArgs := args
	if parseErr != nil {
		// 非严格模式沿用 agent loop 的做法：把原始参数交给工具，让它自己报错。
		execArgs = map[string]any{"_raw": tc.Function.Arguments}
	}

	toolCtx := ctx
	var cancel context.CancelFunc
	if cfg.Timeouts.ToolMS > 0 {
		toolCtx, cancel = context.WithTimeout(ctx, time.Duration(cfg.Timeouts.ToolMS)*time.Millisecond)
	}

	var out string
	var execErr error
	if injector != nil {
		var injected bool
		out, execErr, injected = injector.Inject(toolCtx, tr.CaseID, realTool.Name(), callIdx)
		rec.Injected = injected
	}
	if !rec.Injected {
		out, execErr = realTool.Execute(toolCtx, execArgs)
	}
	if toolCtx.Err() != nil && errors.Is(toolCtx.Err(), context.DeadlineExceeded) {
		rec.TimedOut = true
		if execErr == nil {
			execErr = context.DeadlineExceeded
		}
	}
	if errors.Is(execErr, context.DeadlineExceeded) {
		rec.TimedOut = true
	}
	if cancel != nil {
		cancel()
	}

	rec.ExecOK = execErr == nil && !rec.TimedOut
	rec.RetRaw = out
	rec.RetRendered = renderOrFallback(tr.Config.Return, out, execErr)
	if execErr != nil {
		rec.Error = execErr.Error()
		if rec.TimedOut {
			tr.Escalate(result.StatusTimeout)
			tr.AddError(fmt.Sprintf("工具 %s 执行超时", realTool.Name()))
		} else {
			tr.Escalate(result.StatusExecError)
			tr.AddError(fmt.Sprintf("工具 %s 执行失败: %v", realTool.Name(), execErr))
		}
	}
	rec.LatencyMS = time.Since(start).Milliseconds()
	return rec
}

func renderOrFallback(variant, raw string, err error) string {
	out, rerr := RenderReturn(variant, raw, err)
	if rerr != nil {
		if err != nil {
			return "错误: " + err.Error()
		}
		return raw
	}
	return out
}

func callModel(ctx context.Context, cfg *config.Config, compl Completer, req provider.Request, runID string, round int, tr *result.Trace) (provider.Message, *provider.Usage, int, error) {
	retries := cfg.Timeouts.Retries
	if retries < 0 {
		retries = 0
	}
	var lastErr error
	for attempt := 0; attempt <= retries; attempt++ {
		if err := ctx.Err(); err != nil {
			return provider.Message{}, nil, attempt, err
		}
		attemptID := fmt.Sprintf("%s#r%d-a%d", runID, round, attempt+1)
		callCtx := ctx
		var cancel context.CancelFunc
		if cfg.Timeouts.RequestMS > 0 {
			callCtx, cancel = context.WithTimeout(ctx, time.Duration(cfg.Timeouts.RequestMS)*time.Millisecond)
		}
		msg, usage, err := compl.Complete(callCtx, req)
		if cancel != nil {
			cancel()
		}
		lastErr = err
		if err == nil {
			return msg, usage, attempt + 1, nil
		}
		if ctx.Err() != nil {
			return provider.Message{}, nil, attempt + 1, ctx.Err()
		}
		if attempt >= retries || !retryable(err) {
			return msg, usage, attempt + 1, err
		}
		delay := backoffDelay(attempt, cfg.Timeouts.BackoffMS, cfg.Timeouts.BackoffMaxMS)
		tr.Retries = append(tr.Retries, result.RetryRecord{
			Attempt: attempt + 1,
			RetryOf: attemptID,
			DelayMS: delay,
			Error:   err.Error(),
		})
		if tr.RetryOf == "" {
			tr.RetryOf = attemptID
		}
		if delay > 0 {
			timer := time.NewTimer(time.Duration(delay) * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return provider.Message{}, nil, attempt + 1, ctx.Err()
			case <-timer.C:
			}
		}
	}
	return provider.Message{}, nil, retries + 1, lastErr
}

func backoffDelay(attempt, base, max int) int64 {
	if base <= 0 {
		return 0
	}
	delay := int64(base)
	for i := 0; i < attempt; i++ {
		delay *= 2
		if max > 0 && delay >= int64(max) {
			return int64(max)
		}
	}
	if max > 0 && delay > int64(max) {
		return int64(max)
	}
	return delay
}

func retryable(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var ue *provider.UpstreamError
	if errors.As(err, &ue) {
		return ue.Status == 429 || ue.Status >= 500
	}
	var ne net.Error
	if errors.As(err, &ne) {
		return true
	}
	return false
}

func classifyModelError(parent context.Context, err error) result.Status {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(parent.Err(), context.DeadlineExceeded) {
		return result.StatusTimeout
	}
	return result.StatusGatewayError
}
