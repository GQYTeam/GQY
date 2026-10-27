// 包 httpapi：Anthropic Messages / Gemini generateContent 到内部 Chat 的最小翻译。
// 目前只做非流式；流式请求返回 400，避免客户端误以为收到完整事件流。
package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"gqy/internal/provider"
)

type anthropicRequest struct {
	Model       string          `json:"model"`
	System      json.RawMessage `json:"system,omitempty"`
	Messages    []anthMessage   `json:"messages"`
	MaxTokens   int             `json:"max_tokens,omitempty"`
	Temperature *float64        `json:"temperature,omitempty"`
	Stream      bool            `json:"stream,omitempty"`
	Tools       []anthTool      `json:"tools,omitempty"`
}

type anthMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type anthTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
}

func (s *Server) handleAnthropicMessages(w http.ResponseWriter, r *http.Request) {
	var req anthropicRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20))
	if err := dec.Decode(&req); err != nil {
		writeAnthropicError(w, http.StatusBadRequest, "invalid_request_error", "请求体不是合法 JSON")
		return
	}
	if req.Stream {
		writeAnthropicError(w, http.StatusBadRequest, "unsupported", "当前网关只支持 Anthropic 非流式请求")
		return
	}
	messages, images, err := anthropicMessages(req)
	if err != nil {
		writeAnthropicError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}
	tools := anthropicTools(req.Tools)
	msg, usage, err := s.Gateway.Complete(r.Context(), provider.Request{
		Model: req.Model, Messages: appendImages(messages, images), Tools: tools,
	})
	if err != nil {
		writeAnthropicError(w, http.StatusBadGateway, "api_error", err.Error())
		return
	}
	content := []any{}
	if msg.Content != "" {
		content = append(content, map[string]any{"type": "text", "text": msg.Content})
	}
	for _, tc := range msg.ToolCalls {
		var input map[string]any
		_ = json.Unmarshal([]byte(tc.Function.Arguments), &input)
		if input == nil {
			input = map[string]any{}
		}
		content = append(content, map[string]any{"type": "tool_use", "id": tc.ID, "name": tc.Function.Name, "input": input})
	}
	stop := "end_turn"
	if len(msg.ToolCalls) > 0 {
		stop = "tool_use"
	}
	in, out := 0, 0
	if usage != nil {
		in, out = usage.PromptTokens, usage.CompletionTokens
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id": "msg_" + newRequestID(), "type": "message", "role": "assistant",
		"model": req.Model, "content": content, "stop_reason": stop,
		"usage": map[string]int{"input_tokens": in, "output_tokens": out},
	})
}

func anthropicMessages(req anthropicRequest) ([]provider.Message, []string, error) {
	var out []provider.Message
	var images []string
	if len(req.System) > 0 {
		text, imgs := anthropicContent(req.System)
		images = append(images, imgs...)
		out = append(out, provider.Message{Role: "system", Content: text})
	}
	for _, m := range req.Messages {
		role := "user"
		if m.Role == "assistant" {
			role = "assistant"
		}
		text, imgs := anthropicContent(m.Content)
		if role == "user" {
			images = append(images, imgs...)
		}
		out = append(out, provider.Message{Role: role, Content: text})
	}
	if len(out) == 0 {
		return nil, nil, fmt.Errorf("messages 不能为空")
	}
	return out, images, nil
}

func anthropicContent(raw json.RawMessage) (string, []string) {
	if len(raw) == 0 {
		return "", nil
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s, nil
	}
	var blocks []struct {
		Type   string `json:"type"`
		Text   string `json:"text"`
		Source *struct {
			Type      string `json:"type"`
			MediaType string `json:"media_type"`
			Data      string `json:"data"`
		} `json:"source"`
	}
	if json.Unmarshal(raw, &blocks) != nil {
		return "", nil
	}
	var b strings.Builder
	var images []string
	for _, block := range blocks {
		switch block.Type {
		case "text":
			b.WriteString(block.Text)
		case "image":
			if block.Source != nil && block.Source.Type == "base64" {
				images = append(images, "data:"+block.Source.MediaType+";base64,"+block.Source.Data)
			}
		}
	}
	return b.String(), images
}

func anthropicTools(tools []anthTool) []any {
	if len(tools) == 0 {
		return nil
	}
	out := make([]any, 0, len(tools))
	for _, t := range tools {
		out = append(out, map[string]any{
			"type": "function",
			"function": map[string]any{
				"name": t.Name, "description": t.Description, "parameters": t.InputSchema,
			},
		})
	}
	return out
}

func appendImages(messages []provider.Message, images []string) []provider.Message {
	if len(images) == 0 {
		return messages
	}
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" {
			messages[i].Images = append(messages[i].Images, images...)
			return messages
		}
	}
	return messages
}

func writeAnthropicError(w http.ResponseWriter, status int, typ, message string) {
	writeJSON(w, status, map[string]any{"type": "error", "error": map[string]string{"type": typ, "message": message}})
}

/* ---------------- Gemini ---------------- */

type geminiRequest struct {
	SystemInstruction *struct {
		Parts []struct {
			Text string `json:"text"`
		} `json:"parts"`
	} `json:"systemInstruction,omitempty"`
	Contents []struct {
		Role  string `json:"role"`
		Parts []struct {
			Text       string `json:"text,omitempty"`
			InlineData *struct {
				MimeType string `json:"mimeType"`
				Data     string `json:"data"`
			} `json:"inlineData,omitempty"`
		} `json:"parts"`
	} `json:"contents"`
	Tools []struct {
		FunctionDeclarations []struct {
			Name        string         `json:"name"`
			Description string         `json:"description"`
			Parameters  map[string]any `json:"parameters"`
		} `json:"functionDeclarations"`
	} `json:"tools,omitempty"`
}

func (s *Server) handleGeminiGenerate(w http.ResponseWriter, r *http.Request) {
	model, action, ok := strings.Cut(strings.TrimPrefix(r.URL.Path, "/v1beta/models/"), ":")
	if !ok || action != "generateContent" {
		writeGeminiError(w, http.StatusBadRequest, "只支持 :generateContent")
		return
	}
	model = strings.TrimPrefix(model, "models/")
	var req geminiRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20))
	if err := dec.Decode(&req); err != nil {
		writeGeminiError(w, http.StatusBadRequest, "请求体不是合法 JSON")
		return
	}
	var messages []provider.Message
	var images []string
	if req.SystemInstruction != nil {
		var b strings.Builder
		for _, p := range req.SystemInstruction.Parts {
			b.WriteString(p.Text)
		}
		messages = append(messages, provider.Message{Role: "system", Content: b.String()})
	}
	for _, c := range req.Contents {
		role := "user"
		if c.Role == "model" {
			role = "assistant"
		}
		var b strings.Builder
		var imgs []string
		for _, p := range c.Parts {
			b.WriteString(p.Text)
			if p.InlineData != nil && role == "user" {
				imgs = append(imgs, "data:"+p.InlineData.MimeType+";base64,"+p.InlineData.Data)
			}
		}
		images = append(images, imgs...)
		messages = append(messages, provider.Message{Role: role, Content: b.String()})
	}
	if len(messages) == 0 {
		writeGeminiError(w, http.StatusBadRequest, "contents 不能为空")
		return
	}
	var tools []any
	for _, t := range req.Tools {
		for _, fn := range t.FunctionDeclarations {
			tools = append(tools, map[string]any{"type": "function", "function": map[string]any{
				"name": fn.Name, "description": fn.Description, "parameters": fn.Parameters,
			}})
		}
	}
	msg, usage, err := s.Gateway.Complete(r.Context(), provider.Request{Model: model, Messages: appendImages(messages, images), Tools: tools})
	if err != nil {
		writeGeminiError(w, http.StatusBadGateway, err.Error())
		return
	}
	parts := []any{}
	if msg.Content != "" {
		parts = append(parts, map[string]any{"text": msg.Content})
	}
	for _, tc := range msg.ToolCalls {
		var args map[string]any
		if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil || args == nil {
			args = map[string]any{}
		}
		parts = append(parts, map[string]any{"functionCall": map[string]any{"name": tc.Function.Name, "args": args}})
	}
	in, out := 0, 0
	if usage != nil {
		in, out = usage.PromptTokens, usage.CompletionTokens
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"candidates": []any{map[string]any{
			"content":      map[string]any{"role": "model", "parts": parts},
			"finishReason": "STOP", "index": 0,
		}},
		"usageMetadata": map[string]int{"promptTokenCount": in, "candidatesTokenCount": out, "totalTokenCount": in + out},
	})
}

func writeGeminiError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]any{"code": status, "message": message, "status": "INVALID_ARGUMENT"}})
}
