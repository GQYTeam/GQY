// 包 provider：把 OpenAI 兼容上游的 SSE 转成内部事件（thinking/tool/text/done）。
// 支持两种协议：/chat/completions（chat）与 /responses（responses）。
package provider

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const maxBodyChat = 256 << 10

type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	// Images 是 data URL；有图时请求体 content 会编码成 OpenAI 多模态数组。
	Images []string `json:"images,omitempty"`
}

// MarshalJSON 把 Images 转成 OpenAI chat 的多模态 content 数组。
func (m Message) MarshalJSON() ([]byte, error) {
	type alias Message
	if len(m.Images) == 0 {
		return json.Marshal(alias(m))
	}
	parts := make([]any, 0, len(m.Images)+1)
	if m.Content != "" {
		parts = append(parts, map[string]any{"type": "text", "text": m.Content})
	}
	for _, img := range m.Images {
		parts = append(parts, map[string]any{
			"type": "image_url", "image_url": map[string]string{"url": img},
		})
	}
	return json.Marshal(struct {
		Role       string     `json:"role"`
		Content    []any      `json:"content"`
		ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
		ToolCallID string     `json:"tool_call_id,omitempty"`
	}{m.Role, parts, m.ToolCalls, m.ToolCallID})
}

type ToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type Request struct {
	Messages []Message `json:"messages"`
	Model    string    `json:"model"`
	Effort   string    `json:"effort"`
	Tools    []any     `json:"tools,omitempty"`
	// Session 只用于网关亲和与日志，不会发给上游。
	Session string `json:"session,omitempty"`
}

type Usage struct {
	PromptTokens     int  `json:"prompt_tokens"`
	CompletionTokens int  `json:"completion_tokens"`
	TotalTokens      int  `json:"total_tokens"`
	Mock             bool `json:"mock,omitempty"`
}

type ToolEvent struct {
	Name   string     `json:"name"`
	Detail string     `json:"detail"`
	Icon   string     `json:"icon"`
	Status string     `json:"status"`
	MS     int64      `json:"ms,omitempty"`
	Args   [][]string `json:"args,omitempty"`
	Output string     `json:"output,omitempty"`
}

type Event struct {
	Type     string     `json:"type"`
	Delta    string     `json:"delta,omitempty"`
	Tool     *ToolEvent `json:"tool,omitempty"`
	Usage    *Usage     `json:"usage,omitempty"`
	Code     string     `json:"code,omitempty"`
	Message  string     `json:"message,omitempty"`
	Question *Question  `json:"question,omitempty"`
}

// Question 是 ask_user 事件的载荷。
type Question struct {
	Text    string   `json:"text"`
	Options []string `json:"options,omitempty"`
}

// Writer 串行化写 SSE，保证心跳和事件不交错。
type Writer struct {
	mu        sync.Mutex
	w         http.ResponseWriter
	fl        http.Flusher
	lastEvent atomic.Int64
}

func NewWriter(w http.ResponseWriter, fl http.Flusher) *Writer {
	out := &Writer{w: w, fl: fl}
	out.lastEvent.Store(time.Now().UnixMilli())
	return out
}

func (w *Writer) Send(ev Event) {
	payload, err := json.Marshal(ev)
	if err != nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.lastEvent.Store(time.Now().UnixMilli())
	_, _ = fmt.Fprintf(w.w, "data: %s\n\n", payload)
	w.fl.Flush()
}

func (w *Writer) Comment(text string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	_, _ = fmt.Fprintf(w.w, ": %s\n\n", text)
	w.fl.Flush()
}

func (w *Writer) LastEvent() time.Time {
	return time.UnixMilli(w.lastEvent.Load())
}

type Upstream struct {
	APIBase string
	APIKey  string
	APIUA   string
	Model   string
	Wire    string // auto | chat | responses
	Mock    bool
	Client  *http.Client
}

// Handle 跑完整个聊天流：写响应头、起心跳、选协议、转发事件、收尾 error/end。
func (u *Upstream) Handle(w http.ResponseWriter, r *http.Request, req Request) {
	var dec = json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyChat))
	if err := dec.Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid_json","message":"请求体不是合法 JSON"}`, http.StatusBadRequest)
		return
	}
	messages := normalizeMessages(req.Messages)
	if len(messages) == 0 {
		http.Error(w, `{"error":"messages_required","message":"messages 不能为空"}`, http.StatusBadRequest)
		return
	}
	if u.Wire == "" && u.APIKey == "" && !u.Mock {
		http.Error(w, `{"error":"no_provider","message":"服务端未配置 GQY_API_KEY"}`, http.StatusServiceUnavailable)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, `{"error":"no_flusher","message":"当前连接不支持流式"}`, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	out := NewWriter(w, flusher)

	go u.heartbeat(ctx, cancel, out)

	var err error
	switch {
	case u.Mock:
		err = u.mockStream(ctx, out)
	case u.Wire == "responses":
		err = u.responsesStream(ctx, req, messages, out)
	default:
		err = u.chatStream(ctx, req, messages, out)
		var ue *UpstreamError
		if u.Wire == "auto" && errors.As(err, &ue) && ue.Code == "need_responses" {
			err = u.responsesStream(ctx, req, messages, out)
		}
	}
	if err != nil && ctx.Err() == nil {
		code := "chat_failed"
		var ue *UpstreamError
		if errors.As(err, &ue) {
			code = ue.Code
		}
		out.Send(Event{Type: "error", Code: code, Message: err.Error()})
	}
	out.Send(Event{Type: "end"})
}

func (u *Upstream) heartbeat(ctx context.Context, cancel context.CancelFunc, out *Writer) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if time.Since(out.LastEvent()) > 120*time.Second {
				out.Send(Event{Type: "error", Code: "upstream_timeout", Message: "模型服务 120 秒没有响应"})
				cancel()
				return
			}
			out.Comment("ping")
		}
	}
}

type UpstreamError struct {
	Status int
	Code   string
	Detail string
}

func (e *UpstreamError) Error() string {
	if e.Detail != "" {
		return e.Detail
	}
	switch e.Status {
	case http.StatusUnauthorized:
		return "模型服务拒绝了密钥（401），检查 GQY_API_KEY"
	case http.StatusForbidden:
		return "模型服务拒绝访问（403），检查密钥权限或模型名"
	case http.StatusNotFound:
		return "模型或接口不存在（404），检查 GQY_API_BASE / GQY_MODEL"
	case http.StatusTooManyRequests:
		return "模型服务限流（429），稍后再试"
	}
	if e.Status >= 500 {
		return fmt.Sprintf("模型服务错误（%d）", e.Status)
	}
	return fmt.Sprintf("模型服务返回 %d", e.Status)
}

func (u *Upstream) setUserAgent(req *http.Request) {
	if u.APIUA != "" {
		req.Header.Set("User-Agent", u.APIUA)
	}
}

const mockReply = "这是服务端的本地模拟流（`GQY_MOCK=1`），用来在没有密钥时验证链路。\n\n" +
	"接入真实模型只需要三个环境变量：\n\n" +
	"```bash\n" +
	"export GQY_API_KEY=sk-...\n" +
	"export GQY_API_BASE=https://api.deepseek.com/v1\n" +
	"export GQY_MODEL=deepseek-chat\n" +
	"```\n\n" +
	"然后重新启动 `npm start`。前端不需要改动。"

func (u *Upstream) mockStream(ctx context.Context, out *Writer) error {
	sleep := func(d time.Duration) bool {
		select {
		case <-ctx.Done():
			return false
		case <-time.After(d):
			return true
		}
	}
	out.Send(Event{Type: "thinking", Delta: "服务端未配置密钥，走本地模拟流。\n"})
	if !sleep(120 * time.Millisecond) {
		return nil
	}
	tool := ToolEvent{Name: "读取环境变量", Detail: "GQY_API_KEY", Icon: "i-terminal", Status: "running"}
	out.Send(Event{Type: "tool", Tool: &tool})
	if !sleep(220 * time.Millisecond) {
		return nil
	}
	tool.Status = "done"
	tool.MS = 220
	tool.Output = "未设置（mock 模式）"
	out.Send(Event{Type: "tool", Tool: &tool})

	runes := []rune(mockReply)
	for i := 0; i < len(runes); i += 8 {
		end := i + 8
		if end > len(runes) {
			end = len(runes)
		}
		out.Send(Event{Type: "text", Delta: string(runes[i:end])})
		if !sleep(18 * time.Millisecond) {
			return nil
		}
	}
	out.Send(Event{Type: "done", Usage: &Usage{Mock: true}})
	return nil
}

// Complete 是非流式请求，供 agent loop 内部使用：拿完整文本 + 工具调用。
// 返回 assistant 消息（含 tool_calls）与 usage。
func (u *Upstream) Complete(ctx context.Context, req Request) (Message, *Usage, error) {
	model := req.Model
	if model == "" {
		model = u.Model
	}
	body := map[string]any{
		"model":       model,
		"messages":    req.Messages,
		"stream":      false,
		"temperature": temperatureFor(req.Effort),
	}
	if len(req.Tools) > 0 {
		body["tools"] = req.Tools
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return Message{}, nil, err
	}
	upReq, err := http.NewRequestWithContext(ctx, http.MethodPost, u.APIBase+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return Message{}, nil, err
	}
	upReq.Header.Set("Content-Type", "application/json")
	upReq.Header.Set("Authorization", "Bearer "+u.APIKey)
	u.setUserAgent(upReq)

	resp, err := u.Client.Do(upReq)
	if err != nil {
		return Message{}, nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		detail, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return Message{}, nil, &UpstreamError{Status: resp.StatusCode, Code: fmt.Sprintf("upstream_%d", resp.StatusCode), Detail: string(detail)}
	}

	var full struct {
		Choices []struct {
			Message Message `json:"message"`
		} `json:"choices"`
		Usage *Usage `json:"usage"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&full); err != nil {
		return Message{}, nil, err
	}
	if len(full.Choices) == 0 {
		return Message{}, full.Usage, nil
	}
	return full.Choices[0].Message, full.Usage, nil
}

func temperatureFor(effort string) float64 {
	switch effort {
	case "High":
		return 0.6
	case "Low":
		return 0.2
	}
	return 0.4
}

func (u *Upstream) chatStream(ctx context.Context, req Request, messages []Message, out *Writer) error {
	model := req.Model
	if model == "" {
		model = u.Model
	}
	payload, err := json.Marshal(map[string]any{
		"model":          model,
		"messages":       messages,
		"stream":         true,
		"stream_options": map[string]bool{"include_usage": true},
		"temperature":    temperatureFor(req.Effort),
	})
	if err != nil {
		return err
	}

	upReq, err := http.NewRequestWithContext(ctx, http.MethodPost, u.APIBase+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	upReq.Header.Set("Content-Type", "application/json")
	upReq.Header.Set("Authorization", "Bearer "+u.APIKey)
	upReq.Header.Set("Accept", "text/event-stream")
	u.setUserAgent(upReq)

	resp, err := u.Client.Do(upReq)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errors.New("连不上模型服务，检查 GQY_API_BASE 与网络")
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		detail, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		if resp.StatusCode == http.StatusBadRequest && strings.Contains(string(detail), "/v1/responses") {
			return &UpstreamError{Status: resp.StatusCode, Code: "need_responses"}
		}
		return &UpstreamError{Status: resp.StatusCode, Code: fmt.Sprintf("upstream_%d", resp.StatusCode)}
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 64<<10), 1<<20)

	var finalUsage *Usage
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" || payload == "[DONE]" {
			continue
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content          string `json:"content"`
					ReasoningContent string `json:"reasoning_content"`
				} `json:"delta"`
			} `json:"choices"`
			Usage *Usage `json:"usage"`
		}
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			continue
		}
		if chunk.Usage != nil {
			finalUsage = chunk.Usage
		}
		for _, choice := range chunk.Choices {
			if choice.Delta.ReasoningContent != "" {
				out.Send(Event{Type: "thinking", Delta: choice.Delta.ReasoningContent})
			}
			if choice.Delta.Content != "" {
				out.Send(Event{Type: "text", Delta: choice.Delta.Content})
			}
		}
	}
	if err := scanner.Err(); err != nil && ctx.Err() == nil {
		return errors.New("读取上游流失败：" + err.Error())
	}
	out.Send(Event{Type: "done", Usage: finalUsage})
	return nil
}

func (u *Upstream) responsesStream(ctx context.Context, req Request, messages []Message, out *Writer) error {
	model := req.Model
	if model == "" {
		model = u.Model
	}
	input := make([]map[string]string, 0, len(messages))
	instructions := ""
	for _, m := range messages {
		if m.Role == "system" {
			instructions = strings.TrimSpace(instructions + "\n" + m.Content)
			continue
		}
		input = append(input, map[string]string{"role": m.Role, "content": m.Content})
	}

	effort := "medium"
	switch req.Effort {
	case "High":
		effort = "high"
	case "Low":
		effort = "low"
	}
	body := map[string]any{
		"model":  model,
		"input":  input,
		"stream": true,
		"store":  false,
		"reasoning": map[string]any{
			"effort":  effort,
			"summary": "auto",
		},
	}
	if instructions != "" {
		body["instructions"] = instructions
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}

	upReq, err := http.NewRequestWithContext(ctx, http.MethodPost, u.APIBase+"/responses", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	upReq.Header.Set("Content-Type", "application/json")
	upReq.Header.Set("Authorization", "Bearer "+u.APIKey)
	upReq.Header.Set("Accept", "text/event-stream")
	u.setUserAgent(upReq)

	resp, err := u.Client.Do(upReq)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errors.New("连不上模型服务，检查 GQY_API_BASE 与网络")
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		detail, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		var apiErr struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal(detail, &apiErr)
		if apiErr.Error.Message != "" {
			return &UpstreamError{Status: resp.StatusCode, Code: fmt.Sprintf("upstream_%d", resp.StatusCode), Detail: apiErr.Error.Message}
		}
		return &UpstreamError{Status: resp.StatusCode, Code: fmt.Sprintf("upstream_%d", resp.StatusCode)}
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 64<<10), 4<<20)

	var finalUsage *Usage
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" || payload == "[DONE]" {
			continue
		}
		var event struct {
			Type  string `json:"type"`
			Delta string `json:"delta"`
			Error *struct {
				Message string `json:"message"`
				Code    string `json:"code"`
			} `json:"error"`
			Response *struct {
				Usage *struct {
					InputTokens  int `json:"input_tokens"`
					OutputTokens int `json:"output_tokens"`
					TotalTokens  int `json:"total_tokens"`
				} `json:"usage"`
			} `json:"response"`
		}
		if err := json.Unmarshal([]byte(payload), &event); err != nil {
			continue
		}
		switch event.Type {
		case "response.output_text.delta":
			if event.Delta != "" {
				out.Send(Event{Type: "text", Delta: event.Delta})
			}
		case "response.reasoning_summary_text.delta", "response.reasoning_text.delta":
			if event.Delta != "" {
				out.Send(Event{Type: "thinking", Delta: event.Delta})
			}
		case "response.completed":
			if event.Response != nil && event.Response.Usage != nil {
				finalUsage = &Usage{
					PromptTokens:     event.Response.Usage.InputTokens,
					CompletionTokens: event.Response.Usage.OutputTokens,
					TotalTokens:      event.Response.Usage.TotalTokens,
				}
			}
		case "response.failed", "error":
			message := "上游返回错误"
			if event.Error != nil && event.Error.Message != "" {
				message = event.Error.Message
			}
			return &UpstreamError{Status: http.StatusBadGateway, Code: "upstream_error", Detail: message}
		}
	}
	if err := scanner.Err(); err != nil && ctx.Err() == nil {
		return errors.New("读取上游流失败：" + err.Error())
	}
	out.Send(Event{Type: "done", Usage: finalUsage})
	return nil
}

func normalizeMessages(in []Message) []Message {
	const maxMessages = 200
	const maxChars = 200_000
	if len(in) == 0 {
		return nil
	}
	if len(in) > maxMessages {
		in = in[len(in)-maxMessages:]
	}
	kept := make([]Message, 0, len(in))
	for _, m := range in {
		role := "user"
		switch m.Role {
		case "assistant":
			role = "assistant"
		case "system":
			role = "system"
		}
		content := m.Content
		if runes := []rune(content); len(runes) > maxChars {
			content = string(runes[:maxChars])
		}
		if strings.TrimSpace(content) == "" {
			continue
		}
		kept = append(kept, Message{Role: role, Content: content})
	}
	total := 0
	start := len(kept)
	for i := len(kept) - 1; i >= 0; i-- {
		total += len([]rune(kept[i].Content))
		if total > maxChars {
			break
		}
		start = i
	}
	if start >= len(kept) && len(kept) > 0 {
		start = len(kept) - 1
	}
	return kept[start:]
}
