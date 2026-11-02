package anthropic

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"gqy/internal/provider"
)

func testAdapter(srv *httptest.Server) *Adapter {
	return &Adapter{Client: srv.Client()}
}

func testEndpoint(srv *httptest.Server) provider.Endpoint {
	return provider.Endpoint{ProviderID: "ant", BaseURL: srv.URL, APIKey: "key"}
}

func TestProtocolRegistered(t *testing.T) {
	if got := NewAdapter(nil).Protocol(); got != "anthropic-messages" {
		t.Fatalf("Protocol = %q", got)
	}
	registry := provider.NewRegistry(NewAdapter(nil))
	if _, ok := registry.Get("anthropic-messages"); !ok {
		t.Fatal("anthropic-messages 未注册")
	}
}

func TestAnthropicChat(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Errorf("path = %q", r.URL.Path)
		}
		var req struct {
			Model     string `json:"model"`
			MaxTokens int    `json:"max_tokens"`
			System    string `json:"system"`
			Stream    bool   `json:"stream"`
			Tools     []struct {
				Name string `json:"name"`
			} `json:"tools"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		if req.Model != "claude-x" || req.MaxTokens != 100 || req.System != "你是 GQY" || req.Stream {
			t.Fatalf("请求体不对: %+v", req)
		}
		if len(req.Tools) != 1 || req.Tools[0].Name != "read" {
			t.Fatalf("tools 不对: %+v", req.Tools)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"msg_1","type":"message","role":"assistant","model":"claude-x",
			"content":[
				{"type":"thinking","thinking":"想想","signature":"sig"},
				{"type":"text","text":"你好"},
				{"type":"tool_use","id":"tu_1","name":"read","input":{"path":"a.md"}}
			],
			"stop_reason":"tool_use",
			"usage":{"input_tokens":10,"output_tokens":5}
		}`))
	}))
	defer srv.Close()

	resp, err := testAdapter(srv).Chat(context.Background(), testEndpoint(srv), &provider.Request{
		Model:     "claude-x",
		MaxTokens: 100,
		Messages: []provider.Message{
			{Role: provider.RoleSystem, Content: "你是 GQY"},
			{Role: provider.RoleUser, Content: "读 a.md"},
		},
		Tools: []provider.Tool{{Name: "read", Description: "读取文件", Parameters: map[string]any{"type": "object"}}},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if resp.Thinking != "想想" || resp.Text != "你好" {
		t.Fatalf("thinking/text = %q/%q", resp.Thinking, resp.Text)
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Name != "read" || resp.ToolCalls[0].Arguments["path"] != "a.md" {
		t.Fatalf("tool_calls = %+v", resp.ToolCalls)
	}
	if resp.StopReason != provider.StopToolUse || resp.Usage.PromptTokens != 10 || resp.Usage.CompletionTokens != 5 {
		t.Fatalf("stop/usage = %v / %+v", resp.StopReason, resp.Usage)
	}
}

func TestAnthropicStream(t *testing.T) {
	sse := strings.Join([]string{
		`event: message_start`,
		`data: {"type":"message_start","message":{"id":"msg_1","usage":{"input_tokens":7}}}`,
		``,
		`event: content_block_start`,
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"你"}}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"好"}}`,
		``,
		`event: content_block_stop`,
		`data: {"type":"content_block_stop","index":0}`,
		``,
		`event: content_block_start`,
		`data: {"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"tu_1","name":"read","input":{}}}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"path\":\"a.md\"}"}}`,
		``,
		`event: content_block_stop`,
		`data: {"type":"content_block_stop","index":1}`,
		``,
		`event: message_delta`,
		`data: {"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":3}}`,
		``,
		`event: message_stop`,
		`data: {"type":"message_stop"}`,
		``,
	}, "\n")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(sse))
	}))
	defer srv.Close()

	var events []provider.Event
	resp, err := testAdapter(srv).Stream(context.Background(), testEndpoint(srv), &provider.Request{Model: "claude-x", Messages: []provider.Message{{Role: provider.RoleUser, Content: "hi"}}}, func(ev provider.Event) error {
		events = append(events, ev)
		return nil
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if resp.Text != "你好" {
		t.Fatalf("Text = %q", resp.Text)
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Arguments["path"] != "a.md" {
		t.Fatalf("ToolCalls = %+v", resp.ToolCalls)
	}
	if resp.StopReason != provider.StopToolUse || resp.Usage.PromptTokens != 7 || resp.Usage.CompletionTokens != 3 {
		t.Fatalf("stop/usage = %v / %+v", resp.StopReason, resp.Usage)
	}

	var text []string
	var usageEmitted bool
	var doneEmitted bool
	for _, ev := range events {
		switch ev.Type {
		case provider.EventText:
			text = append(text, ev.Delta)
		case provider.EventUsage:
			usageEmitted = true
		case provider.EventDone:
			doneEmitted = true
			if ev.StopReason != provider.StopToolUse {
				t.Fatalf("done stop = %v", ev.StopReason)
			}
		}
	}
	if strings.Join(text, "") != "你好" || !usageEmitted || !doneEmitted {
		t.Fatalf("事件流不对: %+v", events)
	}
}

func TestAnthropic429(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"type":"error","error":{"type":"rate_limit_error","message":"slow down"}}`))
	}))
	defer srv.Close()

	_, err := testAdapter(srv).Chat(context.Background(), testEndpoint(srv), &provider.Request{Model: "claude-x", Messages: []provider.Message{{Role: provider.RoleUser, Content: "hi"}}})
	pe := provider.AsError(err)
	if pe.Code != provider.CodeRateLimited || !pe.Retryable {
		t.Fatalf("错误 = %+v, want rate_limited 可重试", pe)
	}
}

func TestAnthropicStreamInterrupted(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"半截\"}}\n\n"))
	}))
	defer srv.Close()

	_, err := testAdapter(srv).Stream(context.Background(), testEndpoint(srv), &provider.Request{Model: "claude-x", Messages: []provider.Message{{Role: provider.RoleUser, Content: "hi"}}}, func(provider.Event) error { return nil })
	pe := provider.AsError(err)
	if pe.Code != provider.CodeStreamInterrupted {
		t.Fatalf("错误 = %+v, want stream_interrupted", pe)
	}
}

func TestAnthropicToolRoundtrip(t *testing.T) {
	req := &provider.Request{
		Model: "claude-x",
		Messages: []provider.Message{
			{Role: provider.RoleUser, Content: "读 a.md"},
			{Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{{ID: "tu_1", Name: "read", Arguments: map[string]any{"path": "a.md"}}}},
			{Role: provider.RoleTool, ToolCallID: "tu_1", Name: "read", Content: "文件内容"},
		},
	}
	data, err := encodeRequest(req, false)
	if err != nil {
		t.Fatalf("encodeRequest: %v", err)
	}
	text := string(data)
	for _, want := range []string{`"tool_use"`, `"tu_1"`, `"tool_result"`} {
		if !strings.Contains(text, want) {
			t.Fatalf("编码结果缺少 %q: %s", want, text)
		}
	}

	decoded, err := DecodeRequest(data)
	if err != nil {
		t.Fatalf("DecodeRequest: %v", err)
	}
	var sawAssistant, sawTool bool
	for _, m := range decoded.Messages {
		if m.Role == provider.RoleAssistant && len(m.ToolCalls) == 1 {
			sawAssistant = true
		}
		if m.Role == provider.RoleTool && m.ToolCallID == "tu_1" && m.Content == "文件内容" {
			sawTool = true
		}
	}
	if !sawAssistant || !sawTool {
		t.Fatalf("往返后消息不对: %+v", decoded.Messages)
	}
}
