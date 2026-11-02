package openai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sort"
	"strings"

	"gqy/internal/provider"
)

const protocolName = "openai-chat"

// Adapter 实现 OpenAI Chat Completions 适配器。
type Adapter struct {
	Client *http.Client
}

// NewAdapter 构造适配器；client 为 nil 时使用 http.DefaultClient。
func NewAdapter(client *http.Client) *Adapter {
	if client == nil {
		client = http.DefaultClient
	}
	return &Adapter{Client: client}
}

// Protocol 返回协议名。
func (a *Adapter) Protocol() string { return protocolName }

// Chat 发起非流式调用。
func (a *Adapter) Chat(ctx context.Context, ep provider.Endpoint, req *provider.Request) (*provider.Response, error) {
	body, err := encodeRequest(req, false)
	if err != nil {
		return nil, err
	}
	resp, err := a.do(ctx, ep, body, false)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	var raw wireResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 32<<20)).Decode(&raw); err != nil {
		return nil, invalidResponse("上游响应不是合法 JSON")
	}
	if len(raw.Choices) == 0 {
		return nil, invalidResponse("上游响应没有 choices")
	}

	out := &provider.Response{
		Thinking: raw.Choices[0].Message.ReasoningContent,
		Text:     raw.Choices[0].Message.Content,
		Usage: provider.Usage{
			PromptTokens:     raw.Usage.PromptTokens,
			CompletionTokens: raw.Usage.CompletionTokens,
		},
	}
	for _, tc := range raw.Choices[0].Message.ToolCalls {
		call, err := convertToolCall(tc)
		if err != nil {
			return nil, err
		}
		out.ToolCalls = append(out.ToolCalls, call)
	}
	out.StopReason = parseFinishReason(raw.Choices[0].FinishReason, len(out.ToolCalls) > 0)
	return out, nil
}

// Stream 发起流式调用并把上游 SSE 翻译成统一事件。
func (a *Adapter) Stream(ctx context.Context, ep provider.Endpoint, req *provider.Request, emit func(provider.Event) error) (*provider.Response, error) {
	body, err := encodeRequest(req, true)
	if err != nil {
		return nil, err
	}
	resp, err := a.do(ctx, ep, body, true)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	result := &provider.Response{}
	var thinking, text strings.Builder
	toolCalls := make(map[int]*toolAccumulator)
	var finish provider.StopReason
	sawDone := false
	usageEmitted := false

	reader := bufio.NewReaderSize(resp.Body, 64*1024)
	for {
		line, readErr := reader.ReadString('\n')
		if line != "" {
			payload, ok := parseSSELine(line)
			if ok {
				if payload == "[DONE]" {
					sawDone = true
					break
				}
				var chunk wireChunk
				if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
					return nil, invalidResponse("流式帧不是合法 JSON")
				}
				if chunk.Error != nil && chunk.Error.Message != "" {
					return nil, &provider.Error{
						Code:       provider.CodeProviderError,
						Retryable:  false,
						HTTPStatus: http.StatusBadGateway,
						Message:    chunk.Error.Message,
					}
				}
				if chunk.Usage != nil {
					result.Usage = provider.Usage{
						PromptTokens:     chunk.Usage.PromptTokens,
						CompletionTokens: chunk.Usage.CompletionTokens,
					}
					if !usageEmitted {
						usageEmitted = true
						u := result.Usage
						if err := emit(provider.Event{Type: provider.EventUsage, Usage: &u}); err != nil {
							return nil, err
						}
					}
				}
				for _, choice := range chunk.Choices {
					delta := choice.Delta
					if delta.ReasoningContent != "" {
						thinking.WriteString(delta.ReasoningContent)
						if err := emit(provider.Event{Type: provider.EventThinking, Delta: delta.ReasoningContent}); err != nil {
							return nil, err
						}
					}
					if delta.Content != "" {
						text.WriteString(delta.Content)
						if err := emit(provider.Event{Type: provider.EventText, Delta: delta.Content}); err != nil {
							return nil, err
						}
					}
					for _, tc := range delta.ToolCalls {
						acc := toolCalls[tc.Index]
						if acc == nil {
							acc = &toolAccumulator{}
							toolCalls[tc.Index] = acc
						}
						if tc.ID != "" {
							acc.ID = tc.ID
						}
						if tc.Function.Name != "" {
							acc.Name = tc.Function.Name
						}
						acc.Arguments.WriteString(tc.Function.Arguments)
						if err := emit(provider.Event{
							Type:           provider.EventToolCall,
							Index:          tc.Index,
							ID:             tc.ID,
							Name:           tc.Function.Name,
							ArgumentsDelta: tc.Function.Arguments,
						}); err != nil {
							return nil, err
						}
					}
					if choice.FinishReason != "" {
						finish = parseFinishReason(choice.FinishReason, len(toolCalls) > 0)
					}
				}
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				break
			}
			return nil, streamInterrupted(readErr)
		}
	}

	if !sawDone && finish == "" {
		return nil, streamInterrupted(errors.New("上游未发送终止帧"))
	}

	result.Thinking = thinking.String()
	result.Text = text.String()
	indexes := make([]int, 0, len(toolCalls))
	for idx := range toolCalls {
		indexes = append(indexes, idx)
	}
	sort.Ints(indexes)
	for _, idx := range indexes {
		acc := toolCalls[idx]
		call := provider.ToolCall{ID: acc.ID, Name: acc.Name, Arguments: map[string]any{}}
		if strings.TrimSpace(acc.Arguments.String()) != "" {
			if err := json.Unmarshal([]byte(acc.Arguments.String()), &call.Arguments); err != nil {
				return nil, invalidResponse("工具调用参数拼接后不是合法 JSON")
			}
		}
		result.ToolCalls = append(result.ToolCalls, call)
	}
	if finish == "" {
		if len(result.ToolCalls) > 0 {
			finish = provider.StopToolUse
		} else {
			finish = provider.StopEndTurn
		}
	}
	result.StopReason = finish
	if err := emit(provider.Event{Type: provider.EventDone, StopReason: finish}); err != nil {
		return nil, err
	}
	return result, nil
}

func (a *Adapter) do(ctx context.Context, ep provider.Endpoint, body []byte, stream bool) (*http.Response, error) {
	base := strings.TrimRight(strings.TrimSpace(ep.BaseURL), "/")
	if base == "" {
		return nil, provider.NewError(provider.CodeConnectionError, false, 502, "provider 未配置 base_url")
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, provider.NewError(provider.CodeInvalidRequest, false, 400, "构造上游请求失败")
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if ep.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+ep.APIKey)
	}
	if ep.UserAgent != "" {
		httpReq.Header.Set("User-Agent", ep.UserAgent)
	}
	if stream {
		httpReq.Header.Set("Accept", "text/event-stream")
	} else {
		httpReq.Header.Set("Accept", "application/json")
	}

	resp, err := a.Client.Do(httpReq)
	if err != nil {
		return nil, provider.NetworkError(err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer func() { _ = resp.Body.Close() }()
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		return nil, provider.FromStatus(resp.StatusCode, data)
	}
	return resp, nil
}

type wireRequest struct {
	Model         string          `json:"model"`
	Messages      []wireMessage   `json:"messages"`
	Tools         []wireTool      `json:"tools,omitempty"`
	ToolChoice    any             `json:"tool_choice,omitempty"`
	Stream        bool            `json:"stream,omitempty"`
	StreamOptions *wireStreamOpts `json:"stream_options,omitempty"`
	MaxTokens     int             `json:"max_tokens,omitempty"`
	Temperature   *float64        `json:"temperature,omitempty"`
	TopP          *float64        `json:"top_p,omitempty"`
	Stop          []string        `json:"stop,omitempty"`
}

type wireStreamOpts struct {
	IncludeUsage bool `json:"include_usage"`
}

type wireMessage struct {
	Role       string         `json:"role"`
	Content    any            `json:"content,omitempty"`
	Name       string         `json:"name,omitempty"`
	ToolCalls  []wireToolCall `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
}

type wireToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function wireFunction `json:"function"`
}

type wireFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type wireTool struct {
	Type     string `json:"type"`
	Function struct {
		Name        string         `json:"name"`
		Description string         `json:"description"`
		Parameters  map[string]any `json:"parameters"`
	} `json:"function"`
}

type wireContentPart struct {
	Type     string        `json:"type"`
	Text     string        `json:"text,omitempty"`
	ImageURL *wireImageURL `json:"image_url,omitempty"`
}

type wireImageURL struct {
	URL string `json:"url"`
}

func encodeRequest(req *provider.Request, stream bool) ([]byte, error) {
	if req == nil {
		return nil, &provider.Error{Code: provider.CodeInvalidRequest, Message: "请求为空"}
	}
	body := wireRequest{
		Model:       req.Model,
		Messages:    make([]wireMessage, 0, len(req.Messages)),
		ToolChoice:  req.ToolChoice,
		Stream:      stream,
		MaxTokens:   req.MaxTokens,
		Temperature: req.Temperature,
		TopP:        req.TopP,
		Stop:        req.Stop,
	}
	if stream {
		// 内部总是请求 usage，用于配额记账；对外是否输出由 HTTP 层决定。
		body.StreamOptions = &wireStreamOpts{IncludeUsage: true}
	}
	for _, m := range req.Messages {
		wm := wireMessage{
			Role:       string(m.Role),
			Name:       m.Name,
			ToolCallID: m.ToolCallID,
		}
		switch {
		case len(m.Images) > 0 && m.Role == provider.RoleUser:
			parts := make([]wireContentPart, 0, len(m.Images)+1)
			if m.Content != "" {
				parts = append(parts, wireContentPart{Type: "text", Text: m.Content})
			}
			for _, image := range m.Images {
				parts = append(parts, wireContentPart{Type: "image_url", ImageURL: &wireImageURL{URL: image}})
			}
			wm.Content = parts
		case m.Content != "" || m.Role == provider.RoleTool:
			wm.Content = m.Content
		}
		for _, tc := range m.ToolCalls {
			args, err := json.Marshal(tc.Arguments)
			if err != nil {
				return nil, &provider.Error{Code: provider.CodeInvalidRequest, Message: "工具参数无法序列化"}
			}
			wm.ToolCalls = append(wm.ToolCalls, wireToolCall{
				ID:   tc.ID,
				Type: "function",
				Function: wireFunction{
					Name:      tc.Name,
					Arguments: string(args),
				},
			})
		}
		body.Messages = append(body.Messages, wm)
	}
	for _, t := range req.Tools {
		var wt wireTool
		wt.Type = "function"
		wt.Function.Name = t.Name
		wt.Function.Description = t.Description
		wt.Function.Parameters = t.Parameters
		if wt.Function.Parameters == nil {
			wt.Function.Parameters = map[string]any{"type": "object"}
		}
		body.Tools = append(body.Tools, wt)
	}
	return json.Marshal(body)
}

type wireResponse struct {
	Choices []struct {
		Message struct {
			Content          string `json:"content"`
			ReasoningContent string `json:"reasoning_content"`
			ToolCalls        []struct {
				ID       string `json:"id"`
				Type     string `json:"type"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
}

type wireChunk struct {
	Choices []struct {
		Delta struct {
			Content          string `json:"content"`
			ReasoningContent string `json:"reasoning_content"`
			ToolCalls        []struct {
				Index    int    `json:"index"`
				ID       string `json:"id"`
				Type     string `json:"type"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

type toolAccumulator struct {
	ID        string
	Name      string
	Arguments strings.Builder
}

func convertToolCall(tc struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}) (provider.ToolCall, error) {
	args := map[string]any{}
	if strings.TrimSpace(tc.Function.Arguments) != "" {
		if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
			return provider.ToolCall{}, invalidResponse("工具调用参数不是合法 JSON")
		}
	}
	return provider.ToolCall{ID: tc.ID, Name: tc.Function.Name, Arguments: args}, nil
}

func parseFinishReason(reason string, hasTool bool) provider.StopReason {
	switch strings.ToLower(strings.TrimSpace(reason)) {
	case "tool_calls", "function_call":
		return provider.StopToolUse
	case "length", "max_tokens":
		return provider.StopMaxTokens
	case "content_filter", "safety":
		return provider.StopSafety
	case "stop_sequence":
		return provider.StopStopSequence
	case "stop", "end_turn":
		return provider.StopEndTurn
	case "":
		if hasTool {
			return provider.StopToolUse
		}
		return provider.StopEndTurn
	default:
		return provider.StopOther
	}
}

func parseSSELine(line string) (string, bool) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, ":") {
		return "", false
	}
	if !strings.HasPrefix(line, "data:") {
		return "", false
	}
	return strings.TrimSpace(strings.TrimPrefix(line, "data:")), true
}

func invalidResponse(message string) error {
	return &provider.Error{
		Code:       provider.CodeInvalidResponse,
		Retryable:  false,
		HTTPStatus: http.StatusBadGateway,
		Message:    message,
	}
}

func streamInterrupted(err error) error {
	return &provider.Error{
		Code:       provider.CodeStreamInterrupted,
		Retryable:  true,
		HTTPStatus: http.StatusBadGateway,
		Message:    "上游流中断",
		Err:        err,
	}
}

var _ provider.Adapter = (*Adapter)(nil)
