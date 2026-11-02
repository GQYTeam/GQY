package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"gqy/internal/agent"
	"gqy/internal/config"
	"gqy/internal/emote"
	"gqy/internal/gateway"
	"gqy/internal/goal"
	"gqy/internal/persona"
	"gqy/internal/plugin"
	"gqy/internal/provider"
	"gqy/internal/provider/openai"
	"gqy/internal/session"
	"gqy/internal/tool"
)

const maxChatBody = 32 << 20

func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	snap := snapshotFrom(r)
	if snap == nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "配置快照不可用")
		return
	}
	if s.gw == nil {
		writeError(w, http.StatusServiceUnavailable, provider.CodeNoCandidate, "网关未初始化")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxChatBody)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, provider.CodeInvalidRequest, "请求体读取失败或超过上限")
		return
	}
	req, err := openai.DecodeChatRequest(body)
	if err != nil {
		writeProviderError(w, err)
		return
	}

	meta := metaFor(r)
	if req.Stream {
		s.streamChat(w, r, snap, req, meta)
		return
	}
	s.completeChat(w, r, snap, req, meta)
}

func (s *Server) completeChat(w http.ResponseWriter, r *http.Request, snap *config.Snapshot, req *provider.Request, meta gateway.Meta) {
	res, err := s.gw.Complete(r.Context(), snap, req, meta)
	if err != nil {
		writeProviderError(w, err)
		return
	}
	w.Header().Set("X-GQY-Provider", res.Provider)
	data, err := openai.EncodeChatResponse(res.Response, chatResponseID(meta.RequestID), res.Model, time.Now().Unix())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "编码响应失败")
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data) // #nosec G705 -- JSON 响应，Content-Type 已显式设置
}

func (s *Server) streamChat(w http.ResponseWriter, r *http.Request, snap *config.Snapshot, req *provider.Request, meta gateway.Meta) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeProviderError(w, provider.NewError(provider.CodeInvalidRequest, false, 500, "当前连接不支持流式响应"))
		return
	}

	id := chatResponseID(meta.RequestID)
	created := time.Now().Unix()
	includeUsage := req.StreamIncludeUsage
	var selectedProvider string
	var selectedModel string
	var usage *openai.UsageJSON
	var lastStop provider.StopReason
	started := false

	meta.OnSelect = func(providerID, _, model string) {
		selectedProvider = providerID
		selectedModel = model
	}

	writeHeaders := func() {
		started = true
		w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")
		if selectedProvider != "" {
			w.Header().Set("X-GQY-Provider", selectedProvider)
		}
	}

	emit := func(ev provider.Event) error {
		if !started {
			writeHeaders()
			if err := writeChatChunk(w, openai.ChatChunk{
				ID:      id,
				Created: created,
				Model:   selectedModel,
				Choices: []openai.ChatChunkChoice{{Index: 0, Delta: openai.ChatDelta{Role: "assistant"}}},
			}); err != nil {
				return err
			}
		}
		switch ev.Type {
		case provider.EventThinking:
			if err := writeChatChunk(w, openai.ChatChunk{
				ID:      id,
				Created: created,
				Model:   selectedModel,
				Choices: []openai.ChatChunkChoice{{Index: 0, Delta: openai.ChatDelta{ReasoningContent: ev.Delta}}},
			}); err != nil {
				return err
			}
		case provider.EventText:
			if err := writeChatChunk(w, openai.ChatChunk{
				ID:      id,
				Created: created,
				Model:   selectedModel,
				Choices: []openai.ChatChunkChoice{{Index: 0, Delta: openai.ChatDelta{Content: ev.Delta}}},
			}); err != nil {
				return err
			}
		case provider.EventToolCall:
			delta := openai.ToolCallDelta{
				Index: ev.Index,
				ID:    ev.ID,
				Function: openai.FunctionDelta{
					Name:      ev.Name,
					Arguments: ev.ArgumentsDelta,
				},
			}
			if ev.ID != "" || ev.Name != "" {
				delta.Type = "function"
			}
			if err := writeChatChunk(w, openai.ChatChunk{
				ID:      id,
				Created: created,
				Model:   selectedModel,
				Choices: []openai.ChatChunkChoice{{Index: 0, Delta: openai.ChatDelta{ToolCalls: []openai.ToolCallDelta{delta}}}},
			}); err != nil {
				return err
			}
		case provider.EventUsage:
			if ev.Usage != nil {
				usage = openai.NewUsageJSON(*ev.Usage)
			}
		case provider.EventDone:
			lastStop = ev.StopReason
		}
		flusher.Flush()
		return nil
	}

	res, err := s.gw.Stream(r.Context(), snap, req, meta, emit)
	if err != nil {
		if !started {
			writeProviderError(w, err)
			return
		}
		pe := provider.AsError(err)
		payload, _ := json.Marshal(apiError{Error: errorDetail{
			Message: pe.Message,
			Type:    "server_error",
			Code:    pe.Code,
		}})
		_ = writeSSE(w, payload)
		flusher.Flush()
		return
	}

	if !started {
		writeHeaders()
		if err := writeChatChunk(w, openai.ChatChunk{
			ID:      id,
			Created: created,
			Model:   selectedModel,
			Choices: []openai.ChatChunkChoice{{Index: 0, Delta: openai.ChatDelta{Role: "assistant"}}},
		}); err != nil {
			return
		}
	}

	stop := provider.StopEndTurn
	if res != nil && res.Response != nil && res.Response.StopReason != "" {
		stop = res.Response.StopReason
	} else if lastStop != "" {
		stop = lastStop
	}
	model := selectedModel
	if res != nil && res.Model != "" {
		model = res.Model
	}
	if err := writeChatChunk(w, openai.ChatChunk{
		ID:      id,
		Created: created,
		Model:   model,
		Choices: []openai.ChatChunkChoice{{Index: 0, FinishReason: openai.FinishReason(stop)}},
	}); err != nil {
		return
	}
	if includeUsage && usage != nil {
		if err := writeChatChunk(w, openai.ChatChunk{
			ID:      id,
			Created: created,
			Model:   model,
			Usage:   usage,
		}); err != nil {
			return
		}
	}
	_ = writeSSE(w, []byte("[DONE]"))
	flusher.Flush()
}

func writeChatChunk(w io.Writer, chunk openai.ChatChunk) error {
	data, err := openai.EncodeChatChunk(chunk)
	if err != nil {
		return err
	}
	return writeSSE(w, data)
}

func writeSSE(w io.Writer, data []byte) error {
	if _, err := io.WriteString(w, "data: "); err != nil {
		return err
	}
	if _, err := w.Write(data); err != nil {
		return err
	}
	_, err := io.WriteString(w, "\n\n")
	return err
}

func writeProviderError(w http.ResponseWriter, err error) {
	pe := provider.AsError(err)
	message := pe.Message
	if message == "" {
		message = pe.Code
	}
	writeError(w, pe.ClientStatus(), pe.Code, message)
}

func chatResponseID(requestID string) string {
	requestID = strings.TrimSpace(requestID)
	if requestID == "" {
		requestID = fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return "chatcmpl-" + requestID
}

func sessionIDFrom(r *http.Request) string {
	for _, key := range []string{"X-GQY-Session", "X-Session-Id"} {
		if v := strings.TrimSpace(r.Header.Get(key)); v != "" {
			if len(v) > 256 {
				v = v[:256]
			}
			return v
		}
	}
	return ""
}

func titleFromContent(content string) string {
	content = strings.Join(strings.Fields(content), " ")
	if content == "" {
		return ""
	}
	runes := []rune(content)
	if len(runes) > 40 {
		return string(runes[:40]) + "…"
	}
	return content
}

// lastUserContent 取请求里最后一条 user 消息正文，用作自动表情的语境（task-13）。
func lastUserContent(req *provider.Request) string {
	if req == nil {
		return ""
	}
	for i := len(req.Messages) - 1; i >= 0; i-- {
		if req.Messages[i].Role == provider.RoleUser {
			return req.Messages[i].Content
		}
	}
	return ""
}

// pickEmoteEvent 向表情包插件要一张表情并投影成 emote 事件；ok=false 表示本次不发。
func pickEmoteEvent(ctx context.Context, plugins *plugin.Manager, logger *slog.Logger, contextText string) (session.Event, bool) {
	if plugins == nil {
		return session.Event{}, false
	}
	picked, ok := (&emote.Picker{Plugins: plugins, Logger: logger}).Pick(ctx, contextText)
	if !ok {
		return session.Event{}, false
	}
	score, noneScore := picked.Score, picked.NoneScore
	return session.Event{
		Type:           session.EventEmote,
		EmoteName:      picked.Name,
		EmoteLabel:     picked.Label,
		EmoteFile:      picked.File,
		EmoteScore:     &score,
		EmoteNoneScore: &noneScore,
	}, true
}

// handleAgentChat 处理 POST /api/chat：不旁路网关；loop 存在时走工具执行闭环（S2）。
func (s *Server) handleAgentChat(w http.ResponseWriter, r *http.Request) {
	snap := snapshotFrom(r)
	if snap == nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "配置快照不可用")
		return
	}
	if s.gw == nil {
		writeError(w, http.StatusServiceUnavailable, provider.CodeNoCandidate, "网关未初始化")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxChatBody)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, provider.CodeInvalidRequest, "请求体读取失败或超过上限")
		return
	}
	var raw struct {
		SessionID string `json:"session_id"`
		Model     string `json:"model"`
		Answer    string `json:"answer"`
		Retry     bool   `json:"retry"`
		Messages  []struct {
			Role    string   `json:"role"`
			Content string   `json:"content"`
			Images  []string `json:"images"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &raw); err != nil || len(raw.Messages) == 0 {
		writeError(w, http.StatusBadRequest, provider.CodeInvalidRequest, "请求体需要非空 messages")
		return
	}

	store := session.NewStore(snap.Home)
	var sess *session.Session
	if raw.SessionID != "" {
		sess, err = store.Open(raw.SessionID)
		if err != nil {
			writeError(w, http.StatusNotFound, "session_not_found", err.Error())
			return
		}
	} else {
		sess, err = store.Create(snap.Workspace)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "session_create_failed", err.Error())
			return
		}
	}

	w.Header().Set("X-GQY-Session", sess.ID())

	preq := &provider.Request{Model: raw.Model, Stream: true, Messages: make([]provider.Message, 0, len(raw.Messages))}
	haveUserEvent := false
	var userEvent session.Event
	for _, m := range raw.Messages {
		role := provider.Role(strings.ToLower(strings.TrimSpace(m.Role)))
		preq.Messages = append(preq.Messages, provider.Message{
			Role:    role,
			Content: m.Content,
			Images:  m.Images,
		})
		// 只把本轮最后一条用户消息落进事件流；历史由客户端回传，只为模型上下文。
		if role == provider.RoleUser {
			haveUserEvent = true
			userEvent = session.Event{Type: session.EventUser, Content: m.Content}
		}
	}
	if msg := persona.Load(snap.Home).SystemMessage(); msg != "" && (len(preq.Messages) == 0 || preq.Messages[0].Role != provider.RoleSystem) {
		preq.Messages = append([]provider.Message{{Role: provider.RoleSystem, Content: msg}}, preq.Messages...)
	}
	if haveUserEvent && !raw.Retry {
		if err := session.AppendEvent(sess.EventsPath(), userEvent); err != nil {
			writeError(w, http.StatusInternalServerError, "event_append_failed", err.Error())
			return
		}
	}
	if haveUserEvent && sess.Manifest.Title == "" {
		if title := titleFromContent(userEvent.Content); title != "" {
			sess.Manifest.Title = title
			if err := sess.SaveManifest(); err != nil {
				s.log.Warn("会话标题保存失败", "session", sess.ID(), "error", err)
			}
		}
	}
	// 主线抢占：goal active 且该会话的 loop 正在跑时，新消息进支线队列，不打断主线（013）。
	if haveUserEvent && s.goals != nil && s.isSessionRunning(sess.ID()) {
		if g, ok := s.goals.Active(); ok && g != nil {
			if queued, err := s.goals.QueueOnce(g.ID, requestIDFrom(r), userEvent.Content); err == nil && queued {
				_ = session.AppendEvent(sess.EventsPath(), session.Event{Type: session.EventBranch, Content: userEvent.Content})
				writeJSON(w, http.StatusOK, map[string]any{"ok": true, "queued": true, "goal": g.ID})
				return
			}
		}
	}

	if s.agentLoop != nil {
		preq.Tools = s.agentLoop.ToolDecls()
	}

	meta := gateway.Meta{
		RequestID: requestIDFrom(r),
		SessionID: sess.ID(),
		OnSelect: func(providerID, accountID, model string) {
			w.Header().Set("X-GQY-Provider", providerID+"/"+accountID)
		},
	}
	if s.agentLoop != nil {
		goalID := ""
		if s.goals != nil {
			if g, ok := s.goals.Active(); ok && g != nil {
				goalID = g.ID
			}
		}
		s.streamAgentLoop(w, r, snap, preq, meta, sess, raw.Answer, goalID)
		return
	}
	s.streamAgentChat(w, r, snap, preq, meta, sess)
}

func (s *Server) streamAgentLoop(w http.ResponseWriter, r *http.Request, snap *config.Snapshot, req *provider.Request, meta gateway.Meta, sess *session.Session, answer, goalID string) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeProviderError(w, provider.NewError(provider.CodeInvalidRequest, false, 500, "当前连接不支持流式响应"))
		return
	}
	started := false
	writeHeaders := func() {
		started = true
		w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")
		if goalID != "" {
			w.Header().Set("X-GQY-Goal", goalID)
		}
	}
	if sess != nil {
		s.setSessionRunning(sess.ID(), true)
		defer s.setSessionRunning(sess.ID(), false)
	}
	sink := &agentSink{
		w:            w,
		flusher:      flusher,
		sess:         sess,
		started:      &started,
		writeHeaders: writeHeaders,
		answer:       answer,
		goals:        s.goals,
		goalID:       goalID,
		// 自动表情（task-13）：请求 ctx、插件运行时与用户文本。
		ctx:          r.Context(),
		plugins:      s.plugins,
		log:          s.log,
		lastUserText: lastUserContent(req),
	}
	if _, err := s.agentLoop.Run(r.Context(), snap, req, meta, sink); err != nil {
		if !started {
			writeProviderError(w, err)
		}
		return
	}
	if !started {
		writeHeaders()
	}
	flusher.Flush()
}

// agentSink 把 loop 输出投影成 /api/chat 五事件 SSE 并落 messages.jsonl。
type agentSink struct {
	w            http.ResponseWriter
	flusher      http.Flusher
	sess         *session.Session
	started      *bool
	writeHeaders func()
	answer       string
	goals        *goal.Manager
	goalID       string

	// 自动表情（task-13）：Done 在写 done 之前据此挑一张表情。
	ctx           context.Context
	plugins       *plugin.Manager
	log           *slog.Logger
	lastUserText  string
	wroteText     bool
	questionAsked bool
	emoteSent     bool
}

// TakeQueued 取走排队的支线消息（013/014）。
func (a *agentSink) TakeQueued() []string {
	if a.goals == nil || a.goalID == "" {
		return nil
	}
	return a.goals.TakeQueued(a.goalID)
}

// RecordBranch 登记支线并写成 branch 事件（content=原消息，output=支线回答，branch_id 供采纳）。
func (a *agentSink) RecordBranch(message, output string) error {
	branchID := ""
	if a.goals != nil && a.goalID != "" {
		branchID = a.goals.RegisterBranch(a.goalID, message, output)
	}
	return a.appendAndWrite(session.Event{Type: session.EventBranch, Content: message, Output: output, BranchID: branchID})
}

// TakeAdoptions 取走已采纳未注入的支线摘要（017）。
func (a *agentSink) TakeAdoptions() []string {
	if a.goals == nil || a.goalID == "" {
		return nil
	}
	return a.goals.TakeAdoptions(a.goalID)
}

func (a *agentSink) ensure() error {
	if !*a.started {
		a.writeHeaders()
		*a.started = true
	}
	return nil
}

func (a *agentSink) appendAndWrite(se session.Event) error {
	if err := a.ensure(); err != nil {
		return err
	}
	if err := session.AppendEvent(a.sess.EventsPath(), se); err != nil {
		return err
	}
	if err := writeSessionEvent(a.w, se); err != nil {
		return err
	}
	a.flusher.Flush()
	return nil
}

func (a *agentSink) Thinking(delta string) error {
	return a.appendAndWrite(session.Event{Type: session.EventThinking, Delta: delta})
}

func (a *agentSink) Text(delta string) error {
	if strings.TrimSpace(delta) != "" {
		a.wroteText = true
	}
	return a.appendAndWrite(session.Event{Type: session.EventText, Delta: delta})
}

func (a *agentSink) Tool(rec tool.Record) error {
	se := session.Event{
		Type:          session.EventTool,
		Phase:         "result",
		ToolName:      rec.Name,
		Arguments:     rec.Arguments,
		Output:        rec.Result,
		DurationMS:    rec.DurationMS,
		Status:        rec.Status,
		ArtifactPaths: rec.Artifacts,
	}
	if rec.Status != tool.StatusOK {
		se.Output = rec.Error
	}
	if a.sess != nil {
		// 工具日志（S5）：输入/输出摘要脱敏后落 logs/tools/YYYY-MM-DD.jsonl。
		_ = a.sess.AppendToolLog(session.ToolLogRecord{
			ToolName:     rec.Name,
			Status:       rec.Status,
			DurationMS:   rec.DurationMS,
			InputSummary: session.SummaryJSON(rec.Arguments),
			OutputSummary: func() string {
				if rec.Status == tool.StatusOK {
					return rec.Result
				}
				return rec.Error
			}(),
			ArtifactPaths: rec.Artifacts,
		})
	}
	return a.appendAndWrite(se)
}

func (a *agentSink) Question(q tool.Question) (string, error) {
	// 问过问题的回复不自动发表情（task-13）。
	a.questionAsked = true
	if err := a.appendAndWrite(session.Event{
		Type:        session.EventQuestion,
		Question:    q.Text,
		Options:     q.Options,
		AllowCustom: q.AllowCustom,
	}); err != nil {
		return "", err
	}
	if a.answer == "" {
		return "", agent.ErrAskPending
	}
	return agent.ResolveAnswer(a.answer, q.Options), nil
}

func (a *agentSink) Done(usage *provider.Usage, stop provider.StopReason) error {
	// 自动表情（task-13）：写了正文、没问过问题、且插件可选出一张时，
	// 在最终文本之后、done 之前追加一条 emote 事件；任何失败都不影响回复流。
	if a.canEmote() {
		if ev, ok := pickEmoteEvent(a.ctx, a.plugins, a.log, a.lastUserText); ok {
			a.emoteSent = true
			if err := a.appendAndWrite(ev); err != nil {
				a.logger().Warn("自动表情事件写入失败", "error", err)
			}
		}
	}
	se := session.Event{Type: session.EventDone, StopReason: string(stop)}
	if usage != nil {
		se.Usage = &session.Usage{PromptTokens: usage.PromptTokens, CompletionTokens: usage.CompletionTokens}
	}
	return a.appendAndWrite(se)
}

// canEmote 报告本次回复是否还该尝试自动表情：写了正文、没问过问题、同一次回复只发一条。
func (a *agentSink) canEmote() bool {
	return !a.emoteSent && a.wroteText && !a.questionAsked && a.sess != nil
}

// logger 返回可用日志器（未注入时用默认）。
func (a *agentSink) logger() *slog.Logger {
	if a.log != nil {
		return a.log
	}
	return slog.Default()
}

// streamAgentChat 是未装配 agent loop 时的直通投影（保留 005 行为）。
func (s *Server) streamAgentChat(w http.ResponseWriter, r *http.Request, snap *config.Snapshot, req *provider.Request, meta gateway.Meta, sess *session.Session) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeProviderError(w, provider.NewError(provider.CodeInvalidRequest, false, 500, "当前连接不支持流式响应"))
		return
	}

	started := false
	var usage *session.Usage
	stopReason := ""
	wroteText := false

	writeHeaders := func() {
		started = true
		w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")
	}

	emit := func(ev provider.Event) error {
		if !started {
			writeHeaders()
		}
		var se session.Event
		switch ev.Type {
		case provider.EventThinking:
			se = session.Event{Type: session.EventThinking, Delta: ev.Delta}
		case provider.EventText:
			if strings.TrimSpace(ev.Delta) != "" {
				wroteText = true
			}
			se = session.Event{Type: session.EventText, Delta: ev.Delta}
		case provider.EventToolCall:
			se = session.Event{Type: session.EventTool, Phase: "start", ToolName: ev.Name, Arguments: ev.ArgumentsDelta}
		case provider.EventUsage:
			if ev.Usage != nil {
				usage = &session.Usage{PromptTokens: ev.Usage.PromptTokens, CompletionTokens: ev.Usage.CompletionTokens}
			}
			return nil
		case provider.EventDone:
			stopReason = string(ev.StopReason)
			return nil
		default:
			return nil
		}
		if err := session.AppendEvent(sess.EventsPath(), se); err != nil {
			return err
		}
		return writeSessionEvent(w, se)
	}

	res, err := s.gw.Stream(r.Context(), snap, req, meta, emit)
	if err != nil && !started {
		writeProviderError(w, err)
		return
	}

	if !started {
		writeHeaders()
	}
	// 自动表情（task-13）：与 loop 路径一致，最终文本之后、done 之前追加 emote 事件。
	if wroteText {
		if ev, ok := pickEmoteEvent(r.Context(), s.plugins, s.log, lastUserContent(req)); ok {
			_ = session.AppendEvent(sess.EventsPath(), ev)
			_ = writeSessionEvent(w, ev)
			flusher.Flush()
		}
	}
	done := session.Event{Type: session.EventDone, Usage: usage, StopReason: stopReason}
	if res != nil && res.Response != nil {
		if res.Response.StopReason != "" {
			done.StopReason = string(res.Response.StopReason)
		}
		if done.Usage == nil {
			done.Usage = &session.Usage{
				PromptTokens:     res.Response.Usage.PromptTokens,
				CompletionTokens: res.Response.Usage.CompletionTokens,
			}
		}
	}
	if done.StopReason == "" {
		done.StopReason = "end_turn"
	}
	_ = session.AppendEvent(sess.EventsPath(), done)
	_ = writeSessionEvent(w, done)
	flusher.Flush()
}

func writeSessionEvent(w io.Writer, ev session.Event) error {
	data, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	return writeSSE(w, data)
}
