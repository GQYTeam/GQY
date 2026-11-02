package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gqy/internal/config"
	"gqy/internal/gateway"
	"gqy/internal/provider"
)

type chatFakeAdapter struct {
	chatFn   func(ctx context.Context, ep provider.Endpoint, req *provider.Request) (*provider.Response, error)
	streamFn func(ctx context.Context, ep provider.Endpoint, req *provider.Request, emit func(provider.Event) error) (*provider.Response, error)
}

func (f *chatFakeAdapter) Protocol() string { return config.ProtocolOpenAIChat }

func (f *chatFakeAdapter) Chat(ctx context.Context, ep provider.Endpoint, req *provider.Request) (*provider.Response, error) {
	if f.chatFn != nil {
		return f.chatFn(ctx, ep, req)
	}
	return &provider.Response{Text: "你好", StopReason: provider.StopEndTurn}, nil
}

func (f *chatFakeAdapter) Stream(ctx context.Context, ep provider.Endpoint, req *provider.Request, emit func(provider.Event) error) (*provider.Response, error) {
	if f.streamFn != nil {
		return f.streamFn(ctx, ep, req, emit)
	}
	if err := emit(provider.Event{Type: provider.EventText, Delta: "你好"}); err != nil {
		return nil, err
	}
	if err := emit(provider.Event{Type: provider.EventDone, StopReason: provider.StopEndTurn}); err != nil {
		return nil, err
	}
	return &provider.Response{Text: "你好", StopReason: provider.StopEndTurn}, nil
}

func newChatServer(t *testing.T, fake *chatFakeAdapter) *Server {
	t.Helper()
	home := t.TempDir()
	for name, content := range map[string]string{
		"providers.json": testProviders,
		"routing.json":   testRouting,
		"settings.json":  `{}`,
	} {
		if err := os.WriteFile(filepath.Join(home, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	startup := config.Startup{
		Home:      home,
		Host:      config.DefaultHost,
		Port:      config.DefaultPort,
		Workspace: home,
		APIBase:   config.DefaultAPIBase,
		Model:     config.DefaultModel,
	}
	mgr, err := config.NewManager(startup, func(string) string { return "" })
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	gw := gateway.New(provider.NewRegistry(fake), logger, home)
	return New(mgr, gw, logger)
}

func postChat(s *Server, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.RemoteAddr = "127.0.0.1:12345"
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	return rec
}

func TestChatCompletionsNonStream(t *testing.T) {
	fake := &chatFakeAdapter{chatFn: func(_ context.Context, ep provider.Endpoint, req *provider.Request) (*provider.Response, error) {
		if ep.ProviderID != "p" {
			t.Errorf("provider = %q", ep.ProviderID)
		}
		if req.Model != "m1" || len(req.Messages) != 1 || req.Messages[0].Content != "hi" {
			t.Errorf("请求 = %+v", req)
		}
		return &provider.Response{
			Thinking:   "想",
			Text:       "你好",
			Usage:      provider.Usage{PromptTokens: 3, CompletionTokens: 2},
			StopReason: provider.StopEndTurn,
		}, nil
	}}
	s := newChatServer(t, fake)

	rec := postChat(s, `{"model":"p/m1","messages":[{"role":"user","content":"hi"}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("X-GQY-Provider"); got != "p" {
		t.Fatalf("X-GQY-Provider = %q", got)
	}
	var body struct {
		Object  string `json:"object"`
		Choices []struct {
			Message struct {
				Content          string `json:"content"`
				ReasoningContent string `json:"reasoning_content"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage struct {
			TotalTokens int `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("解析响应: %v\n%s", err, rec.Body.String())
	}
	if body.Object != "chat.completion" || body.Choices[0].Message.Content != "你好" ||
		body.Choices[0].Message.ReasoningContent != "想" ||
		body.Choices[0].FinishReason != "stop" || body.Usage.TotalTokens != 5 {
		t.Fatalf("响应形状不对: %s", rec.Body.String())
	}
}

func TestChatCompletionsStream(t *testing.T) {
	fake := &chatFakeAdapter{streamFn: func(_ context.Context, _ provider.Endpoint, _ *provider.Request, emit func(provider.Event) error) (*provider.Response, error) {
		if err := emit(provider.Event{Type: provider.EventThinking, Delta: "想"}); err != nil {
			return nil, err
		}
		if err := emit(provider.Event{Type: provider.EventText, Delta: "你"}); err != nil {
			return nil, err
		}
		if err := emit(provider.Event{Type: provider.EventText, Delta: "好"}); err != nil {
			return nil, err
		}
		usage := provider.Usage{PromptTokens: 3, CompletionTokens: 2}
		if err := emit(provider.Event{Type: provider.EventUsage, Usage: &usage}); err != nil {
			return nil, err
		}
		if err := emit(provider.Event{Type: provider.EventDone, StopReason: provider.StopEndTurn}); err != nil {
			return nil, err
		}
		return &provider.Response{Thinking: "想", Text: "你好", Usage: usage, StopReason: provider.StopEndTurn}, nil
	}}
	s := newChatServer(t, fake)

	rec := postChat(s, `{
	  "model":"p/m1",
	  "messages":[{"role":"user","content":"hi"}],
	  "stream":true,
	  "stream_options":{"include_usage":true}
	}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "text/event-stream") {
		t.Fatalf("Content-Type = %q", ct)
	}
	if got := rec.Header().Get("X-GQY-Provider"); got != "p" {
		t.Fatalf("X-GQY-Provider = %q", got)
	}
	body := rec.Body.String()
	for _, want := range []string{
		`"role":"assistant"`,
		`"reasoning_content":"想"`,
		`"content":"你"`,
		`"content":"好"`,
		`"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}`,
		`"finish_reason":"stop"`,
		`data: [DONE]`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("流式响应缺少 %q:\n%s", want, body)
		}
	}
}

func TestChatCompletionsError(t *testing.T) {
	fake := &chatFakeAdapter{chatFn: func(context.Context, provider.Endpoint, *provider.Request) (*provider.Response, error) {
		return nil, provider.NewError(provider.CodeInvalidRequest, false, 400, "坏参数")
	}}
	s := newChatServer(t, fake)
	rec := postChat(s, `{"model":"p/m1","messages":[{"role":"user","content":"hi"}]}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "invalid_request") {
		t.Fatalf("错误体 = %s", rec.Body.String())
	}
}

func TestChatCompletionsStreamErrorBeforeStart(t *testing.T) {
	fake := &chatFakeAdapter{streamFn: func(context.Context, provider.Endpoint, *provider.Request, func(provider.Event) error) (*provider.Response, error) {
		return nil, provider.NewError(provider.CodeInvalidRequest, false, 400, "坏参数")
	}}
	s := newChatServer(t, fake)
	rec := postChat(s, `{"model":"p/m1","messages":[{"role":"user","content":"hi"}],"stream":true}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
}

func TestChatCompletionsStreamErrorAfterStart(t *testing.T) {
	fake := &chatFakeAdapter{streamFn: func(_ context.Context, _ provider.Endpoint, _ *provider.Request, emit func(provider.Event) error) (*provider.Response, error) {
		if err := emit(provider.Event{Type: provider.EventText, Delta: "半截"}); err != nil {
			return nil, err
		}
		return nil, provider.NewError(provider.CodeProviderError, true, 502, "boom")
	}}
	s := newChatServer(t, fake)
	rec := postChat(s, `{"model":"p/m1","messages":[{"role":"user","content":"hi"}],"stream":true}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"半截"`) || !strings.Contains(body, `"code":"provider_error"`) {
		t.Fatalf("流式错误帧不对:\n%s", body)
	}
}
