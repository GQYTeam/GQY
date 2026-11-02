package anthropic

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

// Adapter 实现 Anthropic Messages 适配器。
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

	data, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, provider.NetworkError(err)
	}
	return decodeResponse(data)
}

// Stream 发起流式调用，把上游事件族翻译成统一事件流。
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
	var text, thinking strings.Builder
	tools := make(map[int]*toolAcc)
	var finish provider.StopReason
	sawMessageStop := false
	usageEmitted := false
	promptTokens, outputTokens := 0, 0

	err = scanEvents(resp.Body, func(eventType string, data []byte) error {
		switch eventType {
		case "message_start":
			var ev struct {
				Message struct {
					Usage struct {
						InputTokens int `json:"input_tokens"`
					} `json:"usage"`
				} `json:"message"`
			}
			if err := json.Unmarshal(data, &ev); err != nil {
				return invalidResponse("message_start 不是合法 JSON")
			}
			promptTokens = ev.Message.Usage.InputTokens
		case "content_block_start":
			var ev struct {
				Index        int `json:"index"`
				ContentBlock struct {
					Type  string         `json:"type"`
					ID    string         `json:"id"`
					Name  string         `json:"name"`
					Input map[string]any `json:"input"`
				} `json:"content_block"`
			}
			if err := json.Unmarshal(data, &ev); err != nil {
				return invalidResponse("content_block_start 不是合法 JSON")
			}
			if ev.ContentBlock.Type == "tool_use" {
				tools[ev.Index] = &toolAcc{ID: ev.ContentBlock.ID, Name: ev.ContentBlock.Name}
			}
		case "content_block_delta":
			var ev struct {
				Index int `json:"index"`
				Delta struct {
					Type        string `json:"type"`
					Text        string `json:"text"`
					Thinking    string `json:"thinking"`
					PartialJSON string `json:"partial_json"`
				} `json:"delta"`
			}
			if err := json.Unmarshal(data, &ev); err != nil {
				return invalidResponse("content_block_delta 不是合法 JSON")
			}
			switch ev.Delta.Type {
			case "text_delta":
				text.WriteString(ev.Delta.Text)
				if err := emit(provider.Event{Type: provider.EventText, Delta: ev.Delta.Text}); err != nil {
					return err
				}
			case "thinking_delta":
				thinking.WriteString(ev.Delta.Thinking)
				if err := emit(provider.Event{Type: provider.EventThinking, Delta: ev.Delta.Thinking}); err != nil {
					return err
				}
			case "input_json_delta":
				acc := tools[ev.Index]
				if acc == nil {
					acc = &toolAcc{}
					tools[ev.Index] = acc
				}
				acc.args.WriteString(ev.Delta.PartialJSON)
				if err := emit(provider.Event{
					Type:           provider.EventToolCall,
					Index:          ev.Index,
					ID:             acc.ID,
					Name:           acc.Name,
					ArgumentsDelta: ev.Delta.PartialJSON,
				}); err != nil {
					return err
				}
			}
		case "message_delta":
			var ev struct {
				Delta struct {
					StopReason string `json:"stop_reason"`
				} `json:"delta"`
				Usage struct {
					OutputTokens int `json:"output_tokens"`
				} `json:"usage"`
			}
			if err := json.Unmarshal(data, &ev); err != nil {
				return invalidResponse("message_delta 不是合法 JSON")
			}
			outputTokens = ev.Usage.OutputTokens
			finish = StopReason(ev.Delta.StopReason, len(tools) > 0)
			if !usageEmitted {
				usageEmitted = true
				u := provider.Usage{PromptTokens: promptTokens, CompletionTokens: outputTokens}
				if err := emit(provider.Event{Type: provider.EventUsage, Usage: &u}); err != nil {
					return err
				}
			}
		case "message_stop":
			sawMessageStop = true
		case "ping":
			// 心跳，忽略。
		case "error":
			var ev struct {
				Error struct {
					Type    string `json:"type"`
					Message string `json:"message"`
				} `json:"error"`
			}
			_ = json.Unmarshal(data, &ev)
			return errorEvent(ev.Error.Type, ev.Error.Message)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if !sawMessageStop {
		return nil, streamInterrupted(errors.New("上游未发送 message_stop"))
	}

	result.Thinking = thinking.String()
	result.Text = text.String()
	indexes := make([]int, 0, len(tools))
	for idx := range tools {
		indexes = append(indexes, idx)
	}
	sort.Ints(indexes)
	for _, idx := range indexes {
		acc := tools[idx]
		call := provider.ToolCall{ID: acc.ID, Name: acc.Name, Arguments: map[string]any{}}
		if strings.TrimSpace(acc.args.String()) != "" {
			if err := json.Unmarshal([]byte(acc.args.String()), &call.Arguments); err != nil {
				return nil, invalidResponse("工具参数拼接后不是合法 JSON")
			}
		}
		result.ToolCalls = append(result.ToolCalls, call)
	}
	result.Usage = provider.Usage{PromptTokens: promptTokens, CompletionTokens: outputTokens}
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
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return nil, provider.NewError(provider.CodeInvalidRequest, false, 400, "构造上游请求失败")
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if ep.APIKey != "" {
		httpReq.Header.Set("x-api-key", ep.APIKey)
	}
	httpReq.Header.Set("anthropic-version", "2023-06-01")
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

type toolAcc struct {
	ID   string
	Name string
	args strings.Builder
}

// scanEvents 解析 Anthropic 风格 SSE（event: / data: 行，空行分隔）。
func scanEvents(r io.Reader, yield func(eventType string, data []byte) error) error {
	reader := bufio.NewReaderSize(r, 64*1024)
	eventType := ""
	var data []byte
	flush := func() error {
		if eventType == "" && len(data) == 0 {
			return nil
		}
		err := yield(eventType, data)
		eventType = ""
		data = nil
		return err
	}

	for {
		line, readErr := reader.ReadString('\n')
		line = strings.TrimSuffix(line, "\n")
		line = strings.TrimSuffix(line, "\r")
		trimmed := strings.TrimSpace(line)

		if trimmed == "" {
			if err := flush(); err != nil {
				return err
			}
			if readErr != nil {
				if errors.Is(readErr, io.EOF) {
					return nil
				}
				return readErr
			}
			continue
		}
		switch {
		case strings.HasPrefix(line, "event:"):
			eventType = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			data = append(data, []byte(strings.TrimSpace(strings.TrimPrefix(line, "data:")))...)
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				return flush()
			}
			return readErr
		}
	}
}

func errorEvent(eventType, message string) error {
	switch eventType {
	case "rate_limit_error":
		return &provider.Error{Code: provider.CodeRateLimited, Retryable: true, HTTPStatus: http.StatusTooManyRequests, Message: message}
	case "overloaded_error":
		return &provider.Error{Code: provider.CodeProviderError, Retryable: true, HTTPStatus: 529, Message: message}
	case "api_error":
		return &provider.Error{Code: provider.CodeProviderError, Retryable: true, HTTPStatus: http.StatusInternalServerError, Message: message}
	case "authentication_error":
		return &provider.Error{Code: provider.CodeAuthRequired, Retryable: false, HTTPStatus: http.StatusUnauthorized, Message: message}
	case "permission_error":
		return &provider.Error{Code: provider.CodePermissionDenied, Retryable: false, HTTPStatus: http.StatusForbidden, Message: message}
	case "not_found_error":
		return &provider.Error{Code: provider.CodeModelNotFound, Retryable: false, HTTPStatus: http.StatusNotFound, Message: message}
	default:
		return &provider.Error{Code: provider.CodeInvalidRequest, Retryable: false, HTTPStatus: http.StatusBadRequest, Message: message}
	}
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
