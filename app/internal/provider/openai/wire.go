// Package openai 实现 OpenAI Chat Completions 的协议翻译。
//
// 设计依据：docs/designs/03-协议翻译.md（C1、C3、C4、C9、C10）。
package openai

import (
	"encoding/json"
	"fmt"
	"strings"

	"gqy/internal/provider"
)

// DecodeChatRequest 把 OpenAI Chat 请求体翻译成内部请求。
func DecodeChatRequest(data []byte) (*provider.Request, error) {
	var raw struct {
		Model               string          `json:"model"`
		Messages            []rawMessage    `json:"messages"`
		Tools               []rawTool       `json:"tools"`
		ToolChoice          json.RawMessage `json:"tool_choice"`
		Stream              bool            `json:"stream"`
		StreamOptions       *rawStreamOpts  `json:"stream_options"`
		MaxTokens           int             `json:"max_tokens"`
		MaxCompletionTokens int             `json:"max_completion_tokens"`
		Temperature         *float64        `json:"temperature"`
		TopP                *float64        `json:"top_p"`
		Stop                json.RawMessage `json:"stop"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, invalid("请求体不是合法 JSON")
	}
	if len(raw.Messages) == 0 {
		return nil, invalid("messages 不能为空")
	}

	req := &provider.Request{
		Model:       strings.TrimSpace(raw.Model),
		Messages:    make([]provider.Message, 0, len(raw.Messages)),
		Stream:      raw.Stream,
		MaxTokens:   raw.MaxTokens,
		Temperature: raw.Temperature,
		TopP:        raw.TopP,
	}
	if req.MaxTokens == 0 {
		req.MaxTokens = raw.MaxCompletionTokens
	}
	if req.MaxTokens < 0 {
		return nil, invalid("max_tokens 不能为负数")
	}
	if raw.StreamOptions != nil {
		req.StreamIncludeUsage = raw.StreamOptions.IncludeUsage
	}

	for i, m := range raw.Messages {
		msg, err := convertMessage(m)
		if err != nil {
			return nil, fmt.Errorf("messages[%d]: %w", i, err)
		}
		req.Messages = append(req.Messages, msg)
	}

	for i, t := range raw.Tools {
		if t.Type != "" && t.Type != "function" {
			return nil, invalid(fmt.Sprintf("tools[%d]: 只支持 function 类型", i))
		}
		if t.Function.Name == "" {
			return nil, invalid(fmt.Sprintf("tools[%d]: function.name 不能为空", i))
		}
		params := t.Function.Parameters
		if params == nil {
			params = map[string]any{"type": "object"}
		}
		req.Tools = append(req.Tools, provider.Tool{
			Name:        t.Function.Name,
			Description: t.Function.Description,
			Parameters:  params,
		})
	}

	if len(raw.ToolChoice) > 0 && string(raw.ToolChoice) != "null" {
		var choice any
		if err := json.Unmarshal(raw.ToolChoice, &choice); err != nil {
			return nil, invalid("tool_choice 不合法")
		}
		req.ToolChoice = choice
	}

	stop, err := decodeStop(raw.Stop)
	if err != nil {
		return nil, err
	}
	req.Stop = stop
	return req, nil
}

type rawMessage struct {
	Role       string          `json:"role"`
	Content    json.RawMessage `json:"content"`
	Name       string          `json:"name"`
	ToolCalls  []rawToolCall   `json:"tool_calls"`
	ToolCallID string          `json:"tool_call_id"`
}

type rawToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

func convertMessage(raw rawMessage) (provider.Message, error) {
	role := provider.Role(strings.ToLower(strings.TrimSpace(raw.Role)))
	switch role {
	case provider.RoleSystem, provider.RoleUser, provider.RoleAssistant, provider.RoleTool:
	default:
		return provider.Message{}, invalid("不支持的角色 " + raw.Role)
	}

	text, images, err := decodeContent(raw.Content)
	if err != nil {
		return provider.Message{}, err
	}
	msg := provider.Message{
		Role:       role,
		Content:    text,
		Images:     images,
		Name:       raw.Name,
		ToolCallID: raw.ToolCallID,
	}
	for i, tc := range raw.ToolCalls {
		arguments := map[string]any{}
		if strings.TrimSpace(tc.Function.Arguments) != "" {
			if err := json.Unmarshal([]byte(tc.Function.Arguments), &arguments); err != nil {
				return provider.Message{}, invalid(fmt.Sprintf("tool_calls[%d].function.arguments 不是合法 JSON 对象", i))
			}
		}
		if tc.Function.Name == "" {
			return provider.Message{}, invalid(fmt.Sprintf("tool_calls[%d].function.name 不能为空", i))
		}
		msg.ToolCalls = append(msg.ToolCalls, provider.ToolCall{
			ID:        tc.ID,
			Name:      tc.Function.Name,
			Arguments: arguments,
		})
	}
	if role == provider.RoleTool && msg.ToolCallID == "" {
		return provider.Message{}, invalid("tool 消息缺少 tool_call_id")
	}
	return msg, nil
}

type rawContentPart struct {
	Type     string          `json:"type"`
	Text     string          `json:"text"`
	ImageURL json.RawMessage `json:"image_url"`
}

func decodeContent(raw json.RawMessage) (string, []string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", nil, nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text, nil, nil
	}

	var parts []rawContentPart
	if err := json.Unmarshal(raw, &parts); err != nil {
		return "", nil, invalid("content 必须是字符串或内容块数组")
	}
	var b strings.Builder
	images := make([]string, 0)
	for _, part := range parts {
		switch part.Type {
		case "text":
			b.WriteString(part.Text)
		case "image_url":
			url, err := decodeImageURL(part.ImageURL)
			if err != nil {
				return "", nil, err
			}
			images = append(images, url)
		}
	}
	return b.String(), images, nil
}

func decodeImageURL(raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		return "", invalid("image_url 不能为空")
	}
	var obj struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(raw, &obj); err == nil && obj.URL != "" {
		return obj.URL, nil
	}
	var url string
	if err := json.Unmarshal(raw, &url); err == nil && url != "" {
		return url, nil
	}
	return "", invalid("image_url 不合法")
}

type rawStreamOpts struct {
	IncludeUsage bool `json:"include_usage"`
}

type rawTool struct {
	Type     string `json:"type"`
	Function struct {
		Name        string         `json:"name"`
		Description string         `json:"description"`
		Parameters  map[string]any `json:"parameters"`
	} `json:"function"`
}

func decodeStop(raw json.RawMessage) ([]string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var one string
	if err := json.Unmarshal(raw, &one); err == nil {
		if one == "" {
			return nil, nil
		}
		return []string{one}, nil
	}
	var many []string
	if err := json.Unmarshal(raw, &many); err != nil {
		return nil, invalid("stop 必须是字符串或字符串数组")
	}
	out := make([]string, 0, len(many))
	for _, s := range many {
		if s != "" {
			out = append(out, s)
		}
	}
	return out, nil
}

// EncodeChatResponse 生成非流式 OpenAI 响应。
func EncodeChatResponse(resp *provider.Response, id, model string, created int64) ([]byte, error) {
	if resp == nil {
		resp = &provider.Response{}
	}
	message := responseMessage{Role: "assistant"}
	if resp.Text != "" || len(resp.ToolCalls) == 0 {
		message.Content = resp.Text
	}
	if resp.Thinking != "" {
		message.ReasoningContent = resp.Thinking
	}
	for _, tc := range resp.ToolCalls {
		args, err := json.Marshal(tc.Arguments)
		if err != nil {
			return nil, err
		}
		message.ToolCalls = append(message.ToolCalls, responseToolCall{
			ID:   tc.ID,
			Type: "function",
			Function: responseFunction{
				Name:      tc.Name,
				Arguments: string(args),
			},
		})
	}
	body := chatResponse{
		ID:      id,
		Object:  "chat.completion",
		Created: created,
		Model:   model,
		Choices: []responseChoice{{
			Index:        0,
			Message:      message,
			FinishReason: FinishReason(resp.StopReason),
		}},
		Usage: UsageJSON{
			PromptTokens:     resp.Usage.PromptTokens,
			CompletionTokens: resp.Usage.CompletionTokens,
			TotalTokens:      resp.Usage.Total(),
		},
	}
	return json.Marshal(body)
}

type chatResponse struct {
	ID      string           `json:"id"`
	Object  string           `json:"object"`
	Created int64            `json:"created"`
	Model   string           `json:"model"`
	Choices []responseChoice `json:"choices"`
	Usage   UsageJSON        `json:"usage"`
}

type responseChoice struct {
	Index        int             `json:"index"`
	Message      responseMessage `json:"message"`
	FinishReason string          `json:"finish_reason"`
}

type responseMessage struct {
	Role             string             `json:"role"`
	Content          any                `json:"content"`
	ReasoningContent string             `json:"reasoning_content,omitempty"`
	ToolCalls        []responseToolCall `json:"tool_calls,omitempty"`
}

type responseToolCall struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"`
	Function responseFunction `json:"function"`
}

type responseFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type UsageJSON struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// ChatChunk 是流式响应的一帧。
type ChatChunk struct {
	ID      string            `json:"id"`
	Object  string            `json:"object"`
	Created int64             `json:"created"`
	Model   string            `json:"model"`
	Choices []ChatChunkChoice `json:"choices"`
	Usage   *UsageJSON        `json:"usage,omitempty"`
}

// ChatChunkChoice 是一个流式选择。
type ChatChunkChoice struct {
	Index        int       `json:"index"`
	Delta        ChatDelta `json:"delta"`
	FinishReason string    `json:"finish_reason,omitempty"`
}

// ChatDelta 是流式增量。
type ChatDelta struct {
	Role             string          `json:"role,omitempty"`
	Content          string          `json:"content,omitempty"`
	ReasoningContent string          `json:"reasoning_content,omitempty"`
	ToolCalls        []ToolCallDelta `json:"tool_calls,omitempty"`
}

// ToolCallDelta 是工具调用的增量分片。
type ToolCallDelta struct {
	Index    int           `json:"index"`
	ID       string        `json:"id,omitempty"`
	Type     string        `json:"type,omitempty"`
	Function FunctionDelta `json:"function"`
}

// FunctionDelta 是函数名/参数的增量。
type FunctionDelta struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments"`
}

// EncodeChatChunk 序列化一帧 SSE data。
func EncodeChatChunk(chunk ChatChunk) ([]byte, error) {
	if chunk.Object == "" {
		chunk.Object = "chat.completion.chunk"
	}
	if chunk.Choices == nil {
		chunk.Choices = []ChatChunkChoice{}
	}
	return json.Marshal(chunk)
}

// NewUsageJSON 把内部用量转成 OpenAI 形状。
func NewUsageJSON(u provider.Usage) *UsageJSON {
	return &UsageJSON{
		PromptTokens:     u.PromptTokens,
		CompletionTokens: u.CompletionTokens,
		TotalTokens:      u.Total(),
	}
}

// FinishReason 把内部终止原因映射成 OpenAI 的 finish_reason。
func FinishReason(reason provider.StopReason) string {
	switch reason {
	case provider.StopToolUse:
		return "tool_calls"
	case provider.StopMaxTokens:
		return "length"
	case provider.StopSafety:
		return "content_filter"
	case provider.StopEndTurn, provider.StopStopSequence:
		return "stop"
	default:
		return "stop"
	}
}

func invalid(message string) error {
	return &provider.Error{Code: provider.CodeInvalidRequest, Message: message}
}
