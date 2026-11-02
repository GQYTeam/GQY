// Package anthropic 实现 Anthropic Messages 的协议翻译。
//
// 设计依据：docs/designs/03-协议翻译.md（C1、C3、C5、C9、C10）。
package anthropic

import (
	"encoding/json"
	"fmt"
	"strings"

	"gqy/internal/provider"
)

const protocolName = "anthropic-messages"

// DecodeRequest 把 Anthropic Messages 请求体翻译成内部请求。
func DecodeRequest(data []byte) (*provider.Request, error) {
	var raw struct {
		Model         string          `json:"model"`
		MaxTokens     int             `json:"max_tokens"`
		System        json.RawMessage `json:"system"`
		Messages      []rawMessage    `json:"messages"`
		Tools         []rawTool       `json:"tools"`
		Stream        bool            `json:"stream"`
		Temperature   *float64        `json:"temperature"`
		TopP          *float64        `json:"top_p"`
		StopSequences []string        `json:"stop_sequences"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, invalid("请求体不是合法 JSON")
	}
	req := &provider.Request{
		Model:       strings.TrimSpace(raw.Model),
		MaxTokens:   raw.MaxTokens,
		Stream:      raw.Stream,
		Temperature: raw.Temperature,
		TopP:        raw.TopP,
		Stop:        raw.StopSequences,
	}

	if sys, err := decodeSystem(raw.System); err != nil {
		return nil, err
	} else if sys != "" {
		req.Messages = append(req.Messages, provider.Message{Role: provider.RoleSystem, Content: sys})
	}

	for i, m := range raw.Messages {
		msg, err := convertMessage(m)
		if err != nil {
			return nil, fmt.Errorf("messages[%d]: %w", i, err)
		}
		req.Messages = append(req.Messages, msg)
	}

	for _, t := range raw.Tools {
		if t.Name == "" {
			return nil, invalid("tools[].name 不能为空")
		}
		req.Tools = append(req.Tools, provider.Tool{
			Name:        t.Name,
			Description: t.Description,
			Parameters:  t.InputSchema,
		})
	}
	return req, nil
}

func decodeSystem(raw json.RawMessage) (string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text, nil
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return "", invalid("system 必须是字符串或文本块数组")
	}
	var b strings.Builder
	for _, block := range blocks {
		if block.Type == "text" {
			b.WriteString(block.Text)
		}
	}
	return b.String(), nil
}

type rawMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

func convertMessage(raw rawMessage) (provider.Message, error) {
	role := provider.Role(strings.ToLower(strings.TrimSpace(raw.Role)))
	switch role {
	case provider.RoleUser, provider.RoleAssistant:
	default:
		return provider.Message{}, invalid("不支持的角色 " + raw.Role)
	}

	msg := provider.Message{Role: role}
	if raw.Content == nil {
		return msg, nil
	}

	// content 可能是字符串，也可能是内容块数组。
	var text string
	if err := json.Unmarshal(raw.Content, &text); err == nil {
		msg.Content = text
		return msg, nil
	}

	var blocks []rawBlock
	if err := json.Unmarshal(raw.Content, &blocks); err != nil {
		return provider.Message{}, invalid("content 必须是字符串或内容块数组")
	}
	var b strings.Builder
	for _, block := range blocks {
		switch block.Type {
		case "text":
			b.WriteString(block.Text)
		case "image":
			if block.Source != nil && block.Source.Type == "base64" && block.Source.Data != "" {
				msg.Images = append(msg.Images, "data:"+block.Source.MediaType+";base64,"+block.Source.Data)
			}
		case "tool_use":
			if block.ID == "" || block.Name == "" {
				return provider.Message{}, invalid("tool_use 块缺少 id 或 name")
			}
			msg.ToolCalls = append(msg.ToolCalls, provider.ToolCall{
				ID:        block.ID,
				Name:      block.Name,
				Arguments: block.Input,
			})
		case "tool_result":
			msg.Content = block.Content
			msg.ToolCallID = block.ToolUseID
		}
	}
	if msg.Content == "" {
		msg.Content = b.String()
	}
	if role == provider.RoleUser && msg.ToolCallID != "" {
		msg.Role = provider.RoleTool
	}
	return msg, nil
}

type rawBlock struct {
	Type      string         `json:"type"`
	Text      string         `json:"text"`
	ID        string         `json:"id"`
	Name      string         `json:"name"`
	Input     map[string]any `json:"input"`
	Content   string         `json:"content"`
	ToolUseID string         `json:"tool_use_id"`
	Source    *struct {
		Type      string `json:"type"`
		MediaType string `json:"media_type"`
		Data      string `json:"data"`
	} `json:"source"`
}

type rawTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
}

// encodeRequest 把内部请求编码成 Anthropic Messages 请求体。
func encodeRequest(req *provider.Request, stream bool) ([]byte, error) {
	if req == nil {
		return nil, invalid("请求为空")
	}
	body := wireRequest{
		Model:         req.Model,
		MaxTokens:     req.MaxTokens,
		Messages:      make([]wireMessage, 0, len(req.Messages)),
		Stream:        stream,
		Temperature:   req.Temperature,
		TopP:          req.TopP,
		StopSequences: req.Stop,
	}
	if body.MaxTokens <= 0 {
		body.MaxTokens = 4096
	}

	var system strings.Builder
	for _, m := range req.Messages {
		if m.Role == provider.RoleSystem {
			if system.Len() > 0 {
				system.WriteString("\n\n")
			}
			system.WriteString(m.Content)
			continue
		}
		body.Messages = append(body.Messages, convertToWire(m))
	}
	if system.Len() > 0 {
		body.System = system.String()
	}
	for _, t := range req.Tools {
		body.Tools = append(body.Tools, wireTool{Name: t.Name, Description: t.Description, InputSchema: t.Parameters})
	}
	return json.Marshal(body)
}

func convertToWire(m provider.Message) wireMessage {
	wm := wireMessage{Role: string(m.Role)}
	switch m.Role {
	case provider.RoleUser:
		content := make([]wireContentBlock, 0, 2)
		if m.Content != "" {
			content = append(content, wireContentBlock{Type: "text", Text: m.Content})
		}
		for _, image := range m.Images {
			if media, data, ok := splitDataURL(image); ok {
				content = append(content, wireContentBlock{
					Type:   "image",
					Source: &wireImageSource{Type: "base64", MediaType: media, Data: data},
				})
			}
		}
		wm.Content = content
	case provider.RoleAssistant:
		content := make([]wireContentBlock, 0, len(m.ToolCalls)+1)
		if m.Content != "" {
			content = append(content, wireContentBlock{Type: "text", Text: m.Content})
		}
		for _, tc := range m.ToolCalls {
			content = append(content, wireContentBlock{Type: "tool_use", ID: tc.ID, Name: tc.Name, Input: tc.Arguments})
		}
		wm.Content = content
	case provider.RoleTool:
		wm.Role = "user"
		text := m.Content
		if text == "" {
			text = "ok"
		}
		wm.Content = []wireContentBlock{{Type: "tool_result", ToolUseID: m.ToolCallID, Content: text}}
	}
	return wm
}

func splitDataURL(image string) (media, data string, ok bool) {
	image = strings.TrimSpace(image)
	if !strings.HasPrefix(image, "data:") {
		return "", "", false
	}
	rest := strings.TrimPrefix(image, "data:")
	comma := strings.Index(rest, ",")
	if comma < 0 {
		return "", "", false
	}
	meta := rest[:comma]
	if !strings.HasSuffix(meta, ";base64") {
		return "", "", false
	}
	return strings.TrimSuffix(meta, ";base64"), rest[comma+1:], true
}

type wireRequest struct {
	Model         string        `json:"model"`
	MaxTokens     int           `json:"max_tokens"`
	System        string        `json:"system,omitempty"`
	Messages      []wireMessage `json:"messages"`
	Tools         []wireTool    `json:"tools,omitempty"`
	Stream        bool          `json:"stream,omitempty"`
	Temperature   *float64      `json:"temperature,omitempty"`
	TopP          *float64      `json:"top_p,omitempty"`
	StopSequences []string      `json:"stop_sequences,omitempty"`
}

type wireMessage struct {
	Role    string             `json:"role"`
	Content []wireContentBlock `json:"content"`
}

type wireContentBlock struct {
	Type      string           `json:"type"`
	Text      string           `json:"text,omitempty"`
	ID        string           `json:"id,omitempty"`
	Name      string           `json:"name,omitempty"`
	Input     map[string]any   `json:"input,omitempty"`
	ToolUseID string           `json:"tool_use_id,omitempty"`
	Content   string           `json:"content,omitempty"`
	Source    *wireImageSource `json:"source,omitempty"`
}

type wireImageSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
}

type wireTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
}

// decodeResponse 把非流式 Messages 响应翻译成内部响应。
func decodeResponse(data []byte) (*provider.Response, error) {
	var raw wireResponse
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, invalid("上游响应不是合法 JSON")
	}
	out := &provider.Response{}
	for _, block := range raw.Content {
		switch block.Type {
		case "text":
			out.Text += block.Text
		case "thinking":
			out.Thinking += block.Thinking
		case "tool_use":
			out.ToolCalls = append(out.ToolCalls, provider.ToolCall{
				ID:        block.ID,
				Name:      block.Name,
				Arguments: block.Input,
			})
		}
	}
	out.Usage = provider.Usage{PromptTokens: raw.Usage.InputTokens, CompletionTokens: raw.Usage.OutputTokens}
	out.StopReason = StopReason(raw.StopReason, len(out.ToolCalls) > 0)
	return out, nil
}

type wireResponse struct {
	ID      string          `json:"id"`
	Type    string          `json:"type"`
	Role    string          `json:"role"`
	Content []wireRespBlock `json:"content"`
	Model   string          `json:"model"`

	StopReason string    `json:"stop_reason"`
	Usage      wireUsage `json:"usage"`
}

type wireRespBlock struct {
	Type      string         `json:"type"`
	Text      string         `json:"text"`
	Thinking  string         `json:"thinking"`
	Signature string         `json:"signature"`
	ID        string         `json:"id"`
	Name      string         `json:"name"`
	Input     map[string]any `json:"input"`
}

type wireUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// StopReason 把 Anthropic stop_reason 映射成内部 StopReason。
func StopReason(reason string, hasTool bool) provider.StopReason {
	switch strings.ToLower(strings.TrimSpace(reason)) {
	case "end_turn":
		return provider.StopEndTurn
	case "tool_use":
		return provider.StopToolUse
	case "max_tokens":
		return provider.StopMaxTokens
	case "stop_sequence":
		return provider.StopStopSequence
	case "refusal":
		return provider.StopSafety
	case "":
		if hasTool {
			return provider.StopToolUse
		}
		return provider.StopEndTurn
	default:
		return provider.StopOther
	}
}

// StopReasonString 把内部 StopReason 映射回 Anthropic stop_reason。
func StopReasonString(reason provider.StopReason) string {
	switch reason {
	case provider.StopToolUse:
		return "tool_use"
	case provider.StopMaxTokens:
		return "max_tokens"
	case provider.StopStopSequence:
		return "stop_sequence"
	case provider.StopSafety:
		return "refusal"
	default:
		return "end_turn"
	}
}

// wireEncodeResponse 与 decode 用的 wireResponse 分开：编码时工具 input 用
// RawMessage 保留空对象，文本/思考块则不输出无关字段。
type wireEncodeResponse struct {
	ID         string            `json:"id"`
	Type       string            `json:"type"`
	Role       string            `json:"role"`
	Content    []wireEncodeBlock `json:"content"`
	Model      string            `json:"model"`
	StopReason string            `json:"stop_reason"`
	Usage      wireUsage         `json:"usage"`
}

type wireEncodeBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	Thinking  string          `json:"thinking,omitempty"`
	Signature string          `json:"signature,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
}

// EncodeResponse 生成非流式 Messages 响应。
func EncodeResponse(resp *provider.Response, id, model string) ([]byte, error) {
	if resp == nil {
		resp = &provider.Response{}
	}
	out := wireEncodeResponse{
		ID:    id,
		Type:  "message",
		Role:  "assistant",
		Model: model,
	}
	if resp.Thinking != "" {
		out.Content = append(out.Content, wireEncodeBlock{Type: "thinking", Thinking: resp.Thinking})
	}
	if resp.Text != "" || len(resp.ToolCalls) == 0 {
		out.Content = append(out.Content, wireEncodeBlock{Type: "text", Text: resp.Text})
	}
	for _, tc := range resp.ToolCalls {
		args := tc.Arguments
		if args == nil {
			args = map[string]any{}
		}
		encoded, err := json.Marshal(args)
		if err != nil {
			return nil, err
		}
		if len(encoded) == 0 || string(encoded) == "null" {
			encoded = []byte("{}")
		}
		out.Content = append(out.Content, wireEncodeBlock{
			Type:  "tool_use",
			ID:    tc.ID,
			Name:  tc.Name,
			Input: json.RawMessage(encoded),
		})
	}
	out.StopReason = StopReasonString(resp.StopReason)
	out.Usage = wireUsage{InputTokens: resp.Usage.PromptTokens, OutputTokens: resp.Usage.CompletionTokens}
	return json.Marshal(out)
}

// StreamUsage 是流式事件里的 usage 字段。
type StreamUsage struct {
	InputTokens  int `json:"input_tokens,omitempty"`
	OutputTokens int `json:"output_tokens,omitempty"`
}

// StreamPayload 是发往客户端的单个 Anthropic SSE 事件载荷。
type StreamPayload struct {
	Type    string          `json:"type"`
	Message *StreamMessage  `json:"message,omitempty"`
	Index   *int            `json:"index,omitempty"`
	Block   *StreamBlock    `json:"content_block,omitempty"`
	Delta   json.RawMessage `json:"delta,omitempty"`
	Usage   *StreamUsage    `json:"usage,omitempty"`
}

// StreamMessage 是 message_start 的 message 字段。
type StreamMessage struct {
	ID    string      `json:"id"`
	Type  string      `json:"type"`
	Role  string      `json:"role"`
	Model string      `json:"model"`
	Usage StreamUsage `json:"usage"`
}

// StreamBlock 是 content_block_start 的 content_block 字段。
type StreamBlock struct {
	Type     string          `json:"type"`
	Text     string          `json:"text,omitempty"`
	Thinking string          `json:"thinking,omitempty"`
	ID       string          `json:"id,omitempty"`
	Name     string          `json:"name,omitempty"`
	Input    json.RawMessage `json:"input,omitempty"`
}

func invalid(message string) error {
	return &provider.Error{Code: provider.CodeInvalidRequest, Message: message}
}
