// Package gemini 实现 Gemini generateContent 的协议翻译。
//
// 设计依据：docs/designs/03-协议翻译.md（C1、C6、C9、C10）。
package gemini

import (
	"encoding/json"
	"fmt"
	"strings"

	"gqy/internal/provider"
)

const protocolName = "gemini"

// DecodeRequest 把 generateContent 请求体翻译成内部请求。
func DecodeRequest(data []byte) (*provider.Request, error) {
	var raw struct {
		SystemInstruction *rawContent   `json:"systemInstruction"`
		Contents          []rawContent  `json:"contents"`
		Tools             []rawTools    `json:"tools"`
		GenerationConfig  *rawGenConfig `json:"generationConfig"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, invalid("请求体不是合法 JSON")
	}

	req := &provider.Request{Messages: make([]provider.Message, 0, len(raw.Contents))}
	if raw.SystemInstruction != nil {
		if sys := textOf(raw.SystemInstruction); sys != "" {
			req.Messages = append(req.Messages, provider.Message{Role: provider.RoleSystem, Content: sys})
		}
	}

	for i, content := range raw.Contents {
		msgs, err := convertContent(content)
		if err != nil {
			return nil, fmt.Errorf("contents[%d]: %w", i, err)
		}
		req.Messages = append(req.Messages, msgs...)
	}

	for _, t := range raw.Tools {
		for _, fd := range t.FunctionDeclarations {
			req.Tools = append(req.Tools, provider.Tool{
				Name:        fd.Name,
				Description: fd.Description,
				Parameters:  fd.Parameters,
			})
		}
	}
	if raw.GenerationConfig != nil {
		req.Temperature = raw.GenerationConfig.Temperature
		req.TopP = raw.GenerationConfig.TopP
		req.MaxTokens = raw.GenerationConfig.MaxOutputTokens
		req.Stop = raw.GenerationConfig.StopSequences
	}
	return req, nil
}

func textOf(c *rawContent) string {
	var b strings.Builder
	for _, p := range c.Parts {
		if p.Text != "" {
			b.WriteString(p.Text)
		}
	}
	return b.String()
}

func convertContent(raw rawContent) ([]provider.Message, error) {
	role := provider.RoleUser
	if strings.EqualFold(raw.Role, "model") {
		role = provider.RoleAssistant
	}

	var out []provider.Message
	var text strings.Builder
	var images []string
	var toolCalls []provider.ToolCall
	var responses []provider.Message

	for _, part := range raw.Parts {
		switch {
		case part.Text != "":
			text.WriteString(part.Text)
		case part.InlineData != nil && part.InlineData.Data != "":
			images = append(images, "data:"+part.InlineData.MimeType+";base64,"+part.InlineData.Data)
		case part.FunctionCall != nil:
			if part.FunctionCall.Name == "" {
				return nil, invalid("functionCall.name 不能为空")
			}
			args := part.FunctionCall.Args
			if args == nil {
				args = map[string]any{}
			}
			toolCalls = append(toolCalls, provider.ToolCall{Name: part.FunctionCall.Name, Arguments: args})
		case part.FunctionResponse != nil:
			content, _ := json.Marshal(part.FunctionResponse.Response)
			responses = append(responses, provider.Message{
				Role:    provider.RoleTool,
				Name:    part.FunctionResponse.Name,
				Content: string(content),
			})
		}
	}

	switch {
	case role == provider.RoleAssistant && len(toolCalls) > 0:
		out = append(out, provider.Message{Role: provider.RoleAssistant, Content: text.String(), ToolCalls: toolCalls})
	case len(responses) > 0:
		out = append(out, responses...)
	default:
		out = append(out, provider.Message{Role: role, Content: text.String(), Images: images})
	}
	return out, nil
}

type rawContent struct {
	Role  string    `json:"role"`
	Parts []rawPart `json:"parts"`
}

type rawPart struct {
	Text             string         `json:"text"`
	InlineData       *rawInlineData `json:"inlineData"`
	FunctionCall     *rawFuncCall   `json:"functionCall"`
	FunctionResponse *rawFuncResp   `json:"functionResponse"`
}

type rawInlineData struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"`
}

type rawFuncCall struct {
	Name string         `json:"name"`
	Args map[string]any `json:"args"`
}

type rawFuncResp struct {
	Name     string `json:"name"`
	Response any    `json:"response"`
}

type rawTools struct {
	FunctionDeclarations []rawFuncDecl `json:"functionDeclarations"`
}

type rawFuncDecl struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

type rawGenConfig struct {
	Temperature     *float64 `json:"temperature"`
	TopP            *float64 `json:"topP"`
	MaxOutputTokens int      `json:"maxOutputTokens"`
	StopSequences   []string `json:"stopSequences"`
}

// encodeRequest 把内部请求编码成 generateContent 请求体。
func encodeRequest(req *provider.Request) ([]byte, error) {
	if req == nil {
		return nil, invalid("请求为空")
	}
	body := wireRequest{Contents: make([]wireContent, 0, len(req.Messages))}

	var system strings.Builder
	for _, m := range req.Messages {
		if m.Role == provider.RoleSystem {
			if system.Len() > 0 {
				system.WriteString("\n\n")
			}
			system.WriteString(m.Content)
			continue
		}
		body.Contents = append(body.Contents, convertToWire(m))
	}
	if system.Len() > 0 {
		body.SystemInstruction = &wireContent{Parts: []wirePart{{Text: system.String()}}}
	}
	for _, t := range req.Tools {
		body.Tools = append(body.Tools, wireTools{FunctionDeclarations: []wireFuncDecl{{
			Name: t.Name, Description: t.Description, Parameters: t.Parameters,
		}}})
	}
	body.GenerationConfig = &wireGenConfig{
		Temperature:     req.Temperature,
		TopP:            req.TopP,
		MaxOutputTokens: req.MaxTokens,
		StopSequences:   req.Stop,
	}
	return json.Marshal(body)
}

func convertToWire(m provider.Message) wireContent {
	content := wireContent{}
	if m.Role == provider.RoleAssistant {
		content.Role = "model"
	} else {
		content.Role = "user"
	}

	switch m.Role {
	case provider.RoleAssistant:
		if m.Content != "" {
			content.Parts = append(content.Parts, wirePart{Text: m.Content})
		}
		for _, tc := range m.ToolCalls {
			args := tc.Arguments
			if args == nil {
				args = map[string]any{}
			}
			content.Parts = append(content.Parts, wirePart{FunctionCall: &wireFuncCall{Name: tc.Name, Args: args}})
		}
	case provider.RoleTool:
		var resp any
		var obj map[string]any
		if err := json.Unmarshal([]byte(m.Content), &obj); err == nil {
			resp = obj
		} else {
			resp = map[string]any{"content": m.Content}
		}
		content.Parts = append(content.Parts, wirePart{FunctionResponse: &wireFuncResp{Name: m.Name, Response: resp}})
	case provider.RoleUser:
		if m.Content != "" {
			content.Parts = append(content.Parts, wirePart{Text: m.Content})
		}
		for _, image := range m.Images {
			if media, data, ok := splitDataURL(image); ok {
				content.Parts = append(content.Parts, wirePart{InlineData: &wireInlineData{MimeType: media, Data: data}})
			}
		}
	}
	return content
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
	SystemInstruction *wireContent   `json:"systemInstruction,omitempty"`
	Contents          []wireContent  `json:"contents"`
	Tools             []wireTools    `json:"tools,omitempty"`
	GenerationConfig  *wireGenConfig `json:"generationConfig,omitempty"`
}

type wireContent struct {
	Role  string     `json:"role,omitempty"`
	Parts []wirePart `json:"parts"`
}

type wirePart struct {
	Text             string          `json:"text,omitempty"`
	InlineData       *wireInlineData `json:"inlineData,omitempty"`
	FunctionCall     *wireFuncCall   `json:"functionCall,omitempty"`
	FunctionResponse *wireFuncResp   `json:"functionResponse,omitempty"`
}

type wireInlineData struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"`
}

type wireFuncCall struct {
	Name string         `json:"name"`
	Args map[string]any `json:"args"`
}

type wireFuncResp struct {
	Name     string `json:"name"`
	Response any    `json:"response"`
}

type wireTools struct {
	FunctionDeclarations []wireFuncDecl `json:"functionDeclarations"`
}

type wireFuncDecl struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

type wireGenConfig struct {
	Temperature     *float64 `json:"temperature,omitempty"`
	TopP            *float64 `json:"topP,omitempty"`
	MaxOutputTokens int      `json:"maxOutputTokens,omitempty"`
	StopSequences   []string `json:"stopSequences,omitempty"`
}

// wireChunk 是流式与非流式共用的响应形状。
type wireChunk struct {
	Candidates []struct {
		Content struct {
			Role  string     `json:"role"`
			Parts []respPart `json:"parts"`
		} `json:"content"`
		FinishReason string `json:"finishReason"`
	} `json:"candidates"`
	UsageMetadata *struct {
		PromptTokenCount     int `json:"promptTokenCount"`
		CandidatesTokenCount int `json:"candidatesTokenCount"`
		TotalTokenCount      int `json:"totalTokenCount"`
	} `json:"usageMetadata"`
}

type respPart struct {
	Text         string       `json:"text"`
	Thought      string       `json:"thought"`
	FunctionCall *rawFuncCall `json:"functionCall"`
}

// decodeChunk 把一份（流式帧或整段）响应翻译成内部响应与事件；final 表示该帧带终止信息。
func decodeChunk(data []byte, result *provider.Response, emit func(provider.Event) error) (bool, error) {
	var chunk wireChunk
	if err := json.Unmarshal(data, &chunk); err != nil {
		return false, invalid("上游响应不是合法 JSON")
	}
	if len(chunk.Candidates) == 0 {
		return false, invalid("上游响应没有 candidates")
	}

	candidate := chunk.Candidates[0]
	final := candidate.FinishReason != "" || chunk.UsageMetadata != nil
	for _, part := range candidate.Content.Parts {
		switch {
		case part.Text != "":
			result.Text += part.Text
			if err := emit(provider.Event{Type: provider.EventText, Delta: part.Text}); err != nil {
				return false, err
			}
		case part.Thought != "":
			result.Thinking += part.Thought
			if err := emit(provider.Event{Type: provider.EventThinking, Delta: part.Thought}); err != nil {
				return false, err
			}
		case part.FunctionCall != nil:
			call := provider.ToolCall{Name: part.FunctionCall.Name, Arguments: part.FunctionCall.Args}
			if call.Arguments == nil {
				call.Arguments = map[string]any{}
			}
			argsJSON, err := json.Marshal(call.Arguments)
			if err != nil {
				return false, invalid("functionCall.args 无法序列化")
			}
			if err := emit(provider.Event{
				Type:           provider.EventToolCall,
				Name:           call.Name,
				ArgumentsDelta: string(argsJSON),
			}); err != nil {
				return false, err
			}
			result.ToolCalls = append(result.ToolCalls, call)
		}
	}
	if chunk.UsageMetadata != nil {
		result.Usage = provider.Usage{
			PromptTokens:     chunk.UsageMetadata.PromptTokenCount,
			CompletionTokens: chunk.UsageMetadata.CandidatesTokenCount,
		}
		u := result.Usage
		if err := emit(provider.Event{Type: provider.EventUsage, Usage: &u}); err != nil {
			return false, err
		}
	}
	if candidate.FinishReason != "" {
		result.StopReason = FinishReason(candidate.FinishReason, len(result.ToolCalls) > 0)
	}
	return final, nil
}

// FinishReason 把 Gemini finishReason 映射成内部 StopReason。
func FinishReason(reason string, hasTool bool) provider.StopReason {
	switch strings.ToUpper(strings.TrimSpace(reason)) {
	case "STOP", "":
		if hasTool {
			return provider.StopToolUse
		}
		return provider.StopEndTurn
	case "MAX_TOKENS":
		return provider.StopMaxTokens
	case "SAFETY", "RECITATION", "BLOCKLIST", "PROHIBITED_CONTENT", "SPII":
		return provider.StopSafety
	case "STOP_SEQUENCE":
		return provider.StopStopSequence
	default:
		return provider.StopOther
	}
}

func invalid(message string) error {
	return &provider.Error{Code: provider.CodeInvalidRequest, Message: message}
}

// FinishReasonString 把内部 StopReason 映射回 Gemini finishReason。
func FinishReasonString(reason provider.StopReason) string {
	switch reason {
	case provider.StopMaxTokens:
		return "MAX_TOKENS"
	case provider.StopSafety:
		return "SAFETY"
	case provider.StopStopSequence:
		return "STOP_SEQUENCE"
	case provider.StopToolUse:
		return "STOP"
	default:
		return "STOP"
	}
}

// EncodeResponse 生成非流式 generateContent 响应。
func EncodeResponse(resp *provider.Response, model string) ([]byte, error) {
	if resp == nil {
		resp = &provider.Response{}
	}
	parts := make([]map[string]any, 0, 2)
	if resp.Thinking != "" {
		parts = append(parts, map[string]any{"thought": resp.Thinking})
	}
	if resp.Text != "" {
		parts = append(parts, map[string]any{"text": resp.Text})
	}
	for _, tc := range resp.ToolCalls {
		args := tc.Arguments
		if args == nil {
			args = map[string]any{}
		}
		parts = append(parts, map[string]any{"functionCall": map[string]any{"name": tc.Name, "args": args}})
	}
	if len(parts) == 0 {
		parts = []map[string]any{}
	}
	body := map[string]any{
		"candidates": []map[string]any{{
			"content":      map[string]any{"role": "model", "parts": parts},
			"finishReason": FinishReasonString(resp.StopReason),
		}},
		"usageMetadata": map[string]any{
			"promptTokenCount":     resp.Usage.PromptTokens,
			"candidatesTokenCount": resp.Usage.CompletionTokens,
			"totalTokenCount":      resp.Usage.Total(),
		},
	}
	return json.Marshal(body)
}
