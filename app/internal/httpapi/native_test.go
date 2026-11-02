package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gqy/internal/provider"
)

func postPath(s *Server, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.RemoteAddr = "127.0.0.1:12345"
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	return rec
}

func TestAnthropicMessagesEndpoint(t *testing.T) {
	s := newChatServer(t, &chatFakeAdapter{})
	rec := postPath(s, "/v1/messages", `{
	  "model":"m1",
	  "max_tokens":100,
	  "messages":[{"role":"user","content":"你好"}]
	}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("X-GQY-Provider"); got != "p" {
		t.Fatalf("X-GQY-Provider = %q", got)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"type":"message"`) || !strings.Contains(body, `"text":"你好"`) {
		t.Fatalf("Anthropic 响应 = %s", body)
	}
	if !strings.Contains(body, `"stop_reason":"end_turn"`) {
		t.Fatalf("stop_reason 不对: %s", body)
	}
}

func TestGeminiGenerateEndpoint(t *testing.T) {
	s := newChatServer(t, &chatFakeAdapter{})
	rec := postPath(s, "/v1beta/models/m1:generateContent", `{
	  "contents":[{"role":"user","parts":[{"text":"你好"}]}]
	}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("X-GQY-Provider"); got != "p" {
		t.Fatalf("X-GQY-Provider = %q", got)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"text":"你好"`) || !strings.Contains(body, `"finishReason":"STOP"`) {
		t.Fatalf("Gemini 响应 = %s", body)
	}
}

func TestAnthropicMessagesToolAndThinking(t *testing.T) {
	fake := &chatFakeAdapter{chatFn: func(context.Context, provider.Endpoint, *provider.Request) (*provider.Response, error) {
		return &provider.Response{
			Thinking:   "想",
			Text:       "你好",
			ToolCalls:  []provider.ToolCall{{ID: "tu_1", Name: "get_weather", Arguments: map[string]any{"city": "北京"}}},
			StopReason: provider.StopToolUse,
		}, nil
	}}
	s := newChatServer(t, fake)
	rec := postPath(s, "/v1/messages", `{
	  "model":"m1",
	  "max_tokens":64,
	  "messages":[{"role":"user","content":"查天气"}]
	}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{`"type":"thinking"`, `"type":"text"`, `"type":"tool_use"`, `"input":{"city":"北京"}`, `"stop_reason":"tool_use"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("缺少 %q:\n%s", want, body)
		}
	}
	if strings.Index(body, `"type":"thinking"`) > strings.Index(body, `"type":"text"`) {
		t.Fatalf("thinking 块应在 text 之前:\n%s", body)
	}
}

func TestGeminiGenerateTool(t *testing.T) {
	fake := &chatFakeAdapter{chatFn: func(context.Context, provider.Endpoint, *provider.Request) (*provider.Response, error) {
		return &provider.Response{
			ToolCalls:  []provider.ToolCall{{ID: "c1", Name: "get_weather", Arguments: map[string]any{"city": "北京"}}},
			StopReason: provider.StopToolUse,
		}, nil
	}}
	s := newChatServer(t, fake)
	rec := postPath(s, "/v1beta/models/m1:generateContent", `{
	  "contents":[{"role":"user","parts":[{"text":"查天气"}]}]
	}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"functionCall"`) || !strings.Contains(body, `"name":"get_weather"`) {
		t.Fatalf("Gemini 工具响应 = %s", body)
	}
}

func TestAgentChatProjectsSessionEvents(t *testing.T) {
	fake := &chatFakeAdapter{streamFn: func(_ context.Context, _ provider.Endpoint, _ *provider.Request, emit func(provider.Event) error) (*provider.Response, error) {
		if err := emit(provider.Event{Type: provider.EventThinking, Delta: "想"}); err != nil {
			return nil, err
		}
		if err := emit(provider.Event{Type: provider.EventText, Delta: "你好"}); err != nil {
			return nil, err
		}
		if err := emit(provider.Event{Type: provider.EventDone, StopReason: provider.StopEndTurn}); err != nil {
			return nil, err
		}
		return &provider.Response{
			Thinking: "想", Text: "你好",
			Usage:      provider.Usage{PromptTokens: 3, CompletionTokens: 2},
			StopReason: provider.StopEndTurn,
		}, nil
	}}
	s := newChatServer(t, fake)
	rec := postPath(s, "/api/chat", `{
	  "model":"p/m1",
	  "messages":[{"role":"user","content":"hi"}]
	}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		`"type":"thinking"`,
		`"type":"text"`,
		`"type":"done"`,
		`"stop_reason":"end_turn"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("缺少 %q:\n%s", want, body)
		}
	}

	home := s.mgr.Current().Home
	dirs, err := filepath.Glob(filepath.Join(home, "sessions", "*"))
	if err != nil || len(dirs) != 1 {
		t.Fatalf("会话目录 = %v, %v", dirs, err)
	}
	data, err := os.ReadFile(filepath.Join(dirs[0], "io", "context", "messages.jsonl"))
	if err != nil {
		t.Fatalf("读事件流: %v", err)
	}
	events := string(data)
	for _, want := range []string{`"type":"user"`, `"type":"thinking"`, `"type":"text"`, `"type":"done"`} {
		if !strings.Contains(events, want) {
			t.Fatalf("事件流缺少 %q:\n%s", want, events)
		}
	}
}
