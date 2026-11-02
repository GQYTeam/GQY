package gemini

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
	return provider.Endpoint{ProviderID: "g", BaseURL: srv.URL, APIKey: "key"}
}

func TestProtocolRegistered(t *testing.T) {
	if got := NewAdapter(nil).Protocol(); got != "gemini" {
		t.Fatalf("Protocol = %q", got)
	}
	registry := provider.NewRegistry(NewAdapter(nil))
	if _, ok := registry.Get("gemini"); !ok {
		t.Fatal("gemini 未注册")
	}
}

func TestGeminiChat(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models/gemini-1.5:generateContent" {
			t.Errorf("path = %q", r.URL.Path)
		}
		var req struct {
			SystemInstruction *struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"systemInstruction"`
			GenerationConfig *struct {
				MaxOutputTokens int `json:"maxOutputTokens"`
			} `json:"generationConfig"`
			Tools []struct {
				FunctionDeclarations []struct {
					Name string `json:"name"`
				} `json:"functionDeclarations"`
			} `json:"tools"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		if req.SystemInstruction == nil || req.SystemInstruction.Parts[0].Text != "你是 GQY" {
			t.Fatalf("systemInstruction 不对: %+v", req.SystemInstruction)
		}
		if req.GenerationConfig == nil || req.GenerationConfig.MaxOutputTokens != 100 {
			t.Fatalf("generationConfig 不对: %+v", req.GenerationConfig)
		}
		if len(req.Tools) != 1 || req.Tools[0].FunctionDeclarations[0].Name != "read" {
			t.Fatalf("tools 不对: %+v", req.Tools)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"candidates":[{"content":{"role":"model","parts":[
				{"text":"答案"},
				{"functionCall":{"name":"read","args":{"path":"b.md"}}}
			]},"finishReason":"STOP"}],
			"usageMetadata":{"promptTokenCount":4,"candidatesTokenCount":9,"totalTokenCount":13}
		}`))
	}))
	defer srv.Close()

	resp, err := testAdapter(srv).Chat(context.Background(), testEndpoint(srv), &provider.Request{
		Model:     "gemini-1.5",
		MaxTokens: 100,
		Messages: []provider.Message{
			{Role: provider.RoleSystem, Content: "你是 GQY"},
			{Role: provider.RoleUser, Content: "读 b.md"},
		},
		Tools: []provider.Tool{{Name: "read", Description: "读取文件", Parameters: map[string]any{"type": "object"}}},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if resp.Text != "答案" {
		t.Fatalf("Text = %q", resp.Text)
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Name != "read" || resp.ToolCalls[0].Arguments["path"] != "b.md" {
		t.Fatalf("ToolCalls = %+v", resp.ToolCalls)
	}
	if resp.StopReason != provider.StopToolUse || resp.Usage.PromptTokens != 4 || resp.Usage.CompletionTokens != 9 {
		t.Fatalf("stop/usage = %v / %+v", resp.StopReason, resp.Usage)
	}
}

func TestGeminiStream(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models/gemini-1.5:streamGenerateContent" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if !strings.Contains(r.URL.RawQuery, "alt=sse") {
			t.Errorf("缺少 alt=sse: %q", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(strings.Join([]string{
			`data: {"candidates":[{"content":{"role":"model","parts":[{"text":"你好"}]}}]}`,
			``,
			`data: {"candidates":[{"content":{"role":"model","parts":[{"text":"世界"}]}}]}`,
			``,
			`data: {"candidates":[{"content":{"role":"model","parts":[]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":2,"candidatesTokenCount":3,"totalTokenCount":5}}`,
			``,
		}, "\n")))
	}))
	defer srv.Close()

	var events []provider.Event
	resp, err := testAdapter(srv).Stream(context.Background(), testEndpoint(srv), &provider.Request{Model: "gemini-1.5", Messages: []provider.Message{{Role: provider.RoleUser, Content: "hi"}}}, func(ev provider.Event) error {
		events = append(events, ev)
		return nil
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if resp.Text != "你好世界" || resp.StopReason != provider.StopEndTurn {
		t.Fatalf("结果 = %+v", resp)
	}
	if resp.Usage.PromptTokens != 2 || resp.Usage.CompletionTokens != 3 {
		t.Fatalf("usage = %+v", resp.Usage)
	}
	var text []string
	var usageEmitted, doneEmitted bool
	for _, ev := range events {
		switch ev.Type {
		case provider.EventText:
			text = append(text, ev.Delta)
		case provider.EventUsage:
			usageEmitted = true
		case provider.EventDone:
			doneEmitted = true
		}
	}
	if strings.Join(text, "") != "你好世界" || !usageEmitted || !doneEmitted {
		t.Fatalf("事件流不对: %+v", events)
	}
}

func TestGemini400InvalidArgument(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"code":400,"message":"invalid argument","status":"INVALID_ARGUMENT"}}`))
	}))
	defer srv.Close()

	_, err := testAdapter(srv).Chat(context.Background(), testEndpoint(srv), &provider.Request{Model: "gemini-1.5", Messages: []provider.Message{{Role: provider.RoleUser, Content: "hi"}}})
	pe := provider.AsError(err)
	if pe.Code != provider.CodeInvalidRequest || pe.Retryable {
		t.Fatalf("错误 = %+v, want invalid_request 不可重试", pe)
	}
}

func TestGeminiStreamInterrupted(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"半截\"}]}}]}\n\n"))
	}))
	defer srv.Close()

	_, err := testAdapter(srv).Stream(context.Background(), testEndpoint(srv), &provider.Request{Model: "gemini-1.5", Messages: []provider.Message{{Role: provider.RoleUser, Content: "hi"}}}, func(provider.Event) error { return nil })
	pe := provider.AsError(err)
	if pe.Code != provider.CodeStreamInterrupted {
		t.Fatalf("错误 = %+v, want stream_interrupted", pe)
	}
}

func TestGeminiToolRoundtrip(t *testing.T) {
	req := &provider.Request{
		Model: "gemini-1.5",
		Messages: []provider.Message{
			{Role: provider.RoleUser, Content: "读 b.md"},
			{Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{{Name: "read", Arguments: map[string]any{"path": "b.md"}}}},
			{Role: provider.RoleTool, Name: "read", Content: "文件内容"},
		},
	}
	data, err := encodeRequest(req)
	if err != nil {
		t.Fatalf("encodeRequest: %v", err)
	}
	text := string(data)
	for _, want := range []string{`"functionCall"`, `"functionResponse"`, `"b.md"`} {
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
		if m.Role == provider.RoleAssistant && len(m.ToolCalls) == 1 && m.ToolCalls[0].Arguments["path"] == "b.md" {
			sawAssistant = true
		}
		if m.Role == provider.RoleTool && m.Name == "read" {
			sawTool = true
		}
	}
	if !sawAssistant || !sawTool {
		t.Fatalf("往返后消息不对: %+v", decoded.Messages)
	}
}
