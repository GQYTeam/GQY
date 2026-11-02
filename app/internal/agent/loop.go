// Package agent 实现 agent loop：组装请求、消费 provider 事件流、
// 累积工具调用、执行核心工具并回填（01 agent 层、03 C2）。
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"gqy/internal/config"
	"gqy/internal/gateway"
	"gqy/internal/provider"
	"gqy/internal/tool"
)

// ErrAskPending 表示 ask_user 已向用户提问，等待回答回填（本轮不续跑）。
var ErrAskPending = errors.New("agent: ask_user 等待回答")

// ResolveAnswer 把用户回答解析成选项文本：数字键 1..n 选对应选项，其余按自定义答案。
func ResolveAnswer(answer string, options []string) string {
	answer = strings.TrimSpace(answer)
	if n, err := strconv.Atoi(answer); err == nil && n >= 1 && n <= len(options) {
		return options[n-1]
	}
	return answer
}

// Sink 是 loop 输出事件的接收方：/api/chat 用它投影五事件 SSE 并落盘。
type Sink interface {
	Thinking(delta string) error
	Text(delta string) error
	Tool(rec tool.Record) error
	// Question 暂停 loop 等用户回答；返回错误表示取消。
	Question(q tool.Question) (string, error)
	Done(usage *provider.Usage, stop provider.StopReason) error
}

// ModelStreamer 是 loop 的模型来源；gateway.Router.Stream 满足该接口。
type ModelStreamer interface {
	Stream(ctx context.Context, snap *config.Snapshot, req *provider.Request, meta gateway.Meta, emit func(provider.Event) error) (*gateway.Result, error)
}

// Branches 处理主线运行中排队的支线消息（013/014）。
// TakeQueued 取走队列（FIFO）；RecordBranch 回收一条支线的执行结果（写事件流/推送）。
type Branches interface {
	TakeQueued() []string
	RecordBranch(message, output string) error
	// TakeAdoptions 取走「已采纳未注入」的支线摘要（017），每条只返回一次。
	TakeAdoptions() []string
}

// Loop 是 agent 主循环。
type Loop struct {
	Streamer    ModelStreamer
	Tools       *tool.Registry
	MaxRounds   int
	ToolTimeout time.Duration
	// Branches 非 nil 时，每轮开始前处理排队支线，不打断当前轮（013）。
	Branches Branches
}

// NewLoop 构造 loop；maxRounds 与 toolTimeout 为零时用默认值。
func NewLoop(streamer ModelStreamer, tools *tool.Registry) *Loop {
	return &Loop{Streamer: streamer, Tools: tools, MaxRounds: 10, ToolTimeout: 120 * time.Second}
}

type toolAccumulator struct {
	id   string
	name string
	args strings.Builder
}

// Run 执行一次 agent 回复：流式累积 → 有工具调用就执行回填再请求，直到
// done+end_turn 或达到 max_rounds。
func (l *Loop) Run(ctx context.Context, snap *config.Snapshot, req *provider.Request, meta gateway.Meta, sink Sink) (*provider.Response, error) {
	maxRounds := l.MaxRounds
	if maxRounds <= 0 {
		maxRounds = 10
	}
	// 支线出口优先用显式设置的 Loop.Branches；否则看 sink 是否实现（生产路径）。
	branches := l.Branches
	if branches == nil {
		if b, ok := sink.(Branches); ok {
			branches = b
		}
	}
	toolTimeout := l.ToolTimeout
	if toolTimeout <= 0 {
		toolTimeout = 120 * time.Second
	}

	rounds := 0
	for rounds < maxRounds {
		rounds++
		// 轮边界：先注入已采纳的支线摘要，再执行排队的支线（013/014/017）。
		l.drainBranches(ctx, snap, meta, req, branches, true)
		var text, thinking strings.Builder
		accs := make(map[int]*toolAccumulator)
		var finish provider.StopReason
		var usage *provider.Usage

		result, err := l.Streamer.Stream(ctx, snap, req, meta, func(ev provider.Event) error {
			switch ev.Type {
			case provider.EventThinking:
				thinking.WriteString(ev.Delta)
				return sink.Thinking(ev.Delta)
			case provider.EventText:
				text.WriteString(ev.Delta)
				return sink.Text(ev.Delta)
			case provider.EventToolCall:
				acc := accs[ev.Index]
				if acc == nil {
					acc = &toolAccumulator{}
					accs[ev.Index] = acc
				}
				if ev.ID != "" {
					acc.id = ev.ID
				}
				if ev.Name != "" {
					acc.name = ev.Name
				}
				acc.args.WriteString(ev.ArgumentsDelta)
			case provider.EventUsage:
				usage = ev.Usage
			case provider.EventDone:
				finish = ev.StopReason
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
		if result != nil && result.Response != nil {
			if usage == nil {
				usage = &result.Response.Usage
			}
			if finish == "" {
				finish = result.Response.StopReason
			}
		}

		if len(accs) == 0 {
			if finish == "" {
				finish = provider.StopEndTurn
			}
			// 主线收尾前把排队支线执行掉（不注入采纳摘要：主线已结束）。
			l.drainBranches(ctx, snap, meta, req, branches, false)
			final := &provider.Response{Thinking: thinking.String(), Text: text.String(), Usage: derefUsage(usage), StopReason: finish}
			_ = sink.Done(usage, finish)
			return final, nil
		}

		// 有工具调用：按 index 顺序执行并回填。
		indexes := make([]int, 0, len(accs))
		for idx := range accs {
			indexes = append(indexes, idx)
		}
		sort.Ints(indexes)
		for _, idx := range indexes {
			acc := accs[idx]
			call, err := acc.call()
			if err != nil {
				_ = sink.Tool(tool.Record{Name: acc.name, Arguments: map[string]any{}, Error: err.Error(), Status: tool.StatusError})
				continue
			}
			rec, pending := l.executeTool(ctx, call, toolTimeout, sink)
			if rec.Status == tool.StatusOK {
				rec.Artifacts = artifactsFor(call.Name, call.Arguments)
			}
			if pending {
				return &provider.Response{Thinking: thinking.String(), Text: text.String(), StopReason: provider.StopOther}, nil
			}
			if err := sink.Tool(rec); err != nil {
				return nil, err
			}
			content := rec.Result
			if rec.Status != tool.StatusOK {
				content = rec.Error
			}
			req.Messages = append(req.Messages, provider.Message{
				Role:       provider.RoleTool,
				ToolCallID: call.ID,
				Name:       call.Name,
				Content:    content,
			})
		}
	}

	final := &provider.Response{StopReason: provider.StopOther}
	_ = sink.Done(nil, provider.StopOther)
	return final, nil
}

// drainBranches 在轮边界执行排队的支线（014）：
// 每条消息走一次独立的单轮模型调用（不带工具、不进主线上下文），结果写 branch 事件。
func (l *Loop) drainBranches(ctx context.Context, snap *config.Snapshot, meta gateway.Meta, req *provider.Request, branches Branches, includeAdoptions bool) {
	if branches == nil {
		return
	}
	// 017：显式采纳的支线摘要注入主线上下文（只注入一次）。轮末不再注入（主线已结束）。
	if includeAdoptions {
		for _, summary := range branches.TakeAdoptions() {
			req.Messages = append(req.Messages, provider.Message{
				Role:    provider.RoleUser,
				Content: "[支线已采纳，纳入主线考虑] " + summary,
			})
		}
	}
	for _, msg := range branches.TakeQueued() {
		out, err := l.runBranch(ctx, snap, meta, msg)
		if err != nil {
			out = "支线执行失败: " + err.Error()
		}
		_ = branches.RecordBranch(msg, out)
	}
}

// runBranch 为一条支线消息做单轮模型调用；不带工具，避免支线产生副作用。
func (l *Loop) runBranch(ctx context.Context, snap *config.Snapshot, meta gateway.Meta, msg string) (string, error) {
	req := &provider.Request{
		Stream:   true,
		Messages: []provider.Message{{Role: provider.RoleUser, Content: msg}},
	}
	var text strings.Builder
	res, err := l.Streamer.Stream(ctx, snap, req, meta, func(ev provider.Event) error {
		if ev.Type == provider.EventText {
			text.WriteString(ev.Delta)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if res != nil && res.Response != nil && res.Response.Text != "" {
		return res.Response.Text, nil
	}
	return text.String(), nil
}

func (l *Loop) executeTool(ctx context.Context, call provider.ToolCall, timeout time.Duration, sink Sink) (tool.Record, bool) {
	rec := tool.Record{Name: call.Name, Arguments: call.Arguments, Status: tool.StatusOK}
	t, ok := l.Tools.Get(call.Name)
	if !ok {
		rec.Status = tool.StatusError
		rec.Error = fmt.Sprintf("工具未找到: %s", call.Name)
		return rec, false
	}
	rec.Schema = t.Schema()

	start := time.Now()
	tctx, cancel := context.WithTimeout(ctx, timeout)
	result, err := t.Execute(tctx, call.Arguments)
	cancel()
	rec.DurationMS = time.Since(start).Milliseconds()

	if err != nil {
		var askErr *tool.AskError
		if errors.As(err, &askErr) {
			answer, qerr := sink.Question(askErr.Q)
			if errors.Is(qerr, ErrAskPending) {
				rec.Status = tool.StatusCanceled
				return rec, true
			}
			if qerr != nil {
				rec.Status = tool.StatusCanceled
				rec.Error = qerr.Error()
				return rec, false
			}
			rec.Result = answer
			rec.Status = tool.StatusOK
			return rec, false
		}
	}
	if err != nil {
		if errors.Is(tctx.Err(), context.DeadlineExceeded) {
			rec.Status = tool.StatusTimeout
			rec.Error = err.Error()
			return rec, false
		}
		if ctx.Err() == context.Canceled {
			rec.Status = tool.StatusCanceled
			rec.Error = err.Error()
			return rec, false
		}
		rec.Status = tool.StatusError
		rec.Error = err.Error()
		return rec, false
	}
	rec.Result = result
	return rec, false
}

func (a *toolAccumulator) call() (provider.ToolCall, error) {
	call := provider.ToolCall{ID: a.id, Name: a.name, Arguments: map[string]any{}}
	if strings.TrimSpace(a.args.String()) != "" {
		if err := json.Unmarshal([]byte(a.args.String()), &call.Arguments); err != nil {
			return call, fmt.Errorf("tool: 参数不是合法 JSON: %w", err)
		}
	}
	if call.Name == "" {
		return call, errors.New("tool: 工具名缺失")
	}
	return call, nil
}

func derefUsage(u *provider.Usage) provider.Usage {
	if u == nil {
		return provider.Usage{}
	}
	return *u
}
