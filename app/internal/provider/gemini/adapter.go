package gemini

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"

	"gqy/internal/provider"
)

// Adapter 实现 Gemini generateContent 适配器。
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
	body, err := encodeRequest(req)
	if err != nil {
		return nil, err
	}
	resp, err := a.do(ctx, ep, req.Model, body, false)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	data, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, provider.NetworkError(err)
	}
	result := &provider.Response{}
	if _, err := decodeChunk(data, result, func(provider.Event) error { return nil }); err != nil {
		return nil, err
	}
	if result.StopReason == "" {
		result.StopReason = FinishReason("", len(result.ToolCalls) > 0)
	}
	return result, nil
}

// Stream 发起 alt=sse 流式调用，逐帧翻译成统一事件流。
func (a *Adapter) Stream(ctx context.Context, ep provider.Endpoint, req *provider.Request, emit func(provider.Event) error) (*provider.Response, error) {
	body, err := encodeRequest(req)
	if err != nil {
		return nil, err
	}
	resp, err := a.do(ctx, ep, req.Model, body, true)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	result := &provider.Response{}
	sawFinal := false

	reader := bufio.NewReaderSize(resp.Body, 64*1024)
	for {
		line, readErr := reader.ReadString('\n')
		if line != "" {
			payload, ok := parseSSELine(line)
			if ok {
				final, err := decodeChunk([]byte(payload), result, emit)
				if err != nil {
					return nil, err
				}
				if final {
					sawFinal = true
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

	if !sawFinal {
		return nil, streamInterrupted(errors.New("上游未发送终止帧"))
	}
	if result.StopReason == "" {
		result.StopReason = FinishReason("", len(result.ToolCalls) > 0)
	}
	if err := emit(provider.Event{Type: provider.EventDone, StopReason: result.StopReason}); err != nil {
		return nil, err
	}
	return result, nil
}

func (a *Adapter) do(ctx context.Context, ep provider.Endpoint, model string, body []byte, stream bool) (*http.Response, error) {
	base := strings.TrimRight(strings.TrimSpace(ep.BaseURL), "/")
	if base == "" {
		return nil, provider.NewError(provider.CodeConnectionError, false, 502, "provider 未配置 base_url")
	}
	method := "generateContent"
	if stream {
		method = "streamGenerateContent?alt=sse"
	}
	url := base + "/models/" + strings.TrimSpace(model) + ":" + method
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, provider.NewError(provider.CodeInvalidRequest, false, 400, "构造上游请求失败")
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if ep.APIKey != "" {
		httpReq.Header.Set("x-goog-api-key", ep.APIKey)
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
