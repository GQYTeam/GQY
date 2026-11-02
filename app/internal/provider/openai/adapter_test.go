package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"gqy/internal/provider"
)

func TestDecodeChatRequest(t *testing.T) {
	body := `{
	  "model": "p/m1",
	  "messages": [
	    {"role": "system", "content": "你是 GQY"},
	    {"role": "user", "content": [
	      {"type": "text", "text": "看图"},
	      {"type": "image_url", "image_url": {"url": "data:image/png;base64,AAAA"}}
	    ]},
	    {"role": "assistant", "content": "", "tool_calls": [
	      {"id": "call_1", "type": "function", "function": {"name": "read", "arguments": "{\"path\":\"a.txt\"}"}}
	    ]},
	    {"role": "tool", "tool_call_id": "call_1", "name": "read", "content": "内容"}
	  ],
	  "tools": [{"type": "function", "function": {
	    "name": "read", "description": "读文件",
	    "parameters": {"type": "object", "properties": {"path": {"type": "string"}}}
	  }}],
	  "tool_choice": "auto",
	  "stream": true,
	  "stream_options": {"include_usage": true},
	  "max_tokens": 128,
	  "temperature": 0.2,
	  "top_p": 0.9,
	  "stop": ["END"]
	}`
	req, err := DecodeChatRequest([]byte(body))
	if err != nil {
		t.Fatalf("DecodeChatRequest: %v", err)
	}
	if req.Model != "p/m1" || !req.Stream || !req.StreamIncludeUsage || req.MaxTokens != 128 {
		t.Fatalf("基础字段不对: %+v", req)
	}
	if req.Temperature == nil || *req.Temperature != 0.2 || req.TopP == nil || *req.TopP != 0.9 {
		t.Fatalf("采样参数不对: %+v", req)
	}
	if len(req.Stop) != 1 || req.Stop[0] != "END" {
		t.Fatalf("stop 不对: %+v", req.Stop)
	}
	if len(req.Messages) != 4 {
		t.Fatalf("消息数 = %d", len(req.Messages))
	}
	if req.Messages[1].Content != "看图" || len(req.Messages[1].Images) != 1 {
		t.Fatalf("多模态消息不对: %+v", req.Messages[1])
	}
	if len(req.Messages[2].ToolCalls) != 1 || req.Messages[2].ToolCalls[0].Arguments["path"] != "a.txt" {
		t.Fatalf("工具调用不对: %+v", req.Messages[2])
	}
	if req.Messages[3].ToolCallID != "call_1" || req.Messages[3].Content != "内容" {
		t.Fatalf("tool 消息不对: %+v", req.Messages[3])
	}
	if len(req.Tools) != 1 || req.Tools[0].Name != "read" {
		t.Fatalf("tools 不对: %+v", req.Tools)
	}
	if req.ToolChoice != "auto" {
		t.Fatalf("tool_choice 不对: %+v", req.ToolChoice)
	}
}

func TestDecodeChatRequestInvalid(t *testing.T) {
	cases := map[string]string{
		"空 messages":          `{"messages":[]}`,
		"未知角色":                `{"messages":[{"role":"robot","content":"x"}]}`,
		"工具参数不是 JSON":         `{"messages":[{"role":"assistant","tool_calls":[{"id":"c","function":{"name":"f","arguments":"{"}}]}]}`,
		"tool 缺 tool_call_id": `{"messages":[{"role":"tool","content":"x"}]}`,
		"content 类型错误":        `{"messages":[{"role":"user","content":123}]}`,
		"stop 类型错误":           `{"messages":[{"role":"user","content":"x"}],"stop":123}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeChatRequest([]byte(body)); err == nil {
				t.Fatal("应返回错误")
			}
		})
	}
}

func TestEncodeChatResponse(t *testing.T) {
	resp := &provider.Response{
		Thinking:   "想一想",
		Text:       "好的",
		ToolCalls:  []provider.ToolCall{{ID: "call_1", Name: "read", Arguments: map[string]any{"path": "a.txt"}}},
		Usage:      provider.Usage{PromptTokens: 10, CompletionTokens: 4},
		StopReason: provider.StopToolUse,
	}
	data, err := EncodeChatResponse(resp, "chatcmpl-1", "p/m1", 100)
	if err != nil {
		t.Fatalf("EncodeChatResponse: %v", err)
	}
	var body struct {
		ID      string `json:"id"`
		Object  string `json:"object"`
		Choices []struct {
			Message struct {
				Role             string `json:"role"`
				Content          string `json:"content"`
				ReasoningContent string `json:"reasoning_content"`
				ToolCalls        []struct {
					ID       string `json:"id"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage struct {
			TotalTokens int `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(data, &body); err != nil {
		t.Fatalf("解析响应: %v", err)
	}
	if body.Object != "chat.completion" || body.Choices[0].Message.Content != "好的" ||
		body.Choices[0].Message.ReasoningContent != "想一想" ||
		body.Choices[0].FinishReason != "tool_calls" || body.Usage.TotalTokens != 14 {
		t.Fatalf("响应形状不对: %s", data)
	}
	args := body.Choices[0].Message.ToolCalls[0].Function.Arguments
	var parsed map[string]any
	if err := json.Unmarshal([]byte(args), &parsed); err != nil || parsed["path"] != "a.txt" {
		t.Fatalf("工具参数编码不对: %q", args)
	}
}

func TestChatSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/chat/completions" {
			t.Errorf("请求 = %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer key-1" {
			t.Errorf("Authorization = %q", got)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("请求体解析失败: %v", err)
		}
		if body["model"] != "m1" || body["stream"] != nil {
			t.Errorf("请求体 = %+v", body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{
		  "choices": [{
		    "message": {
		      "content": "你好",
		      "reasoning_content": "思考",
		      "tool_calls": [{"id": "c1", "type": "function", "function": {"name": "read", "arguments": "{\"path\":\"a.txt\"}"}}]
		    },
		    "finish_reason": "tool_calls"
		  }],
		  "usage": {"prompt_tokens": 7, "completion_tokens": 3}
		}`)
	}))
	defer server.Close()

	adapter := NewAdapter(server.Client())
	resp, err := adapter.Chat(context.Background(), provider.Endpoint{
		BaseURL: server.URL + "/v1",
		APIKey:  "key-1",
	}, &provider.Request{
		Model:    "m1",
		Messages: []provider.Message{{Role: provider.RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if resp.Text != "你好" || resp.Thinking != "思考" || resp.StopReason != provider.StopToolUse {
		t.Fatalf("响应不对: %+v", resp)
	}
	if resp.Usage.PromptTokens != 7 || resp.Usage.CompletionTokens != 3 {
		t.Fatalf("usage 不对: %+v", resp.Usage)
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Arguments["path"] != "a.txt" {
		t.Fatalf("工具调用不对: %+v", resp.ToolCalls)
	}
}

func TestChatHTTPErrors(t *testing.T) {
	cases := []struct {
		status    int
		body      string
		wantCode  string
		retryable bool
	}{
		{429, `{"error":{"message":"slow down"}}`, provider.CodeRateLimited, true},
		{500, `{"error":{"message":"boom"}}`, provider.CodeProviderError, true},
		{400, `{"error":{"message":"maximum context length exceeded"}}`, provider.CodeContextTooLong, false},
		{401, `{"error":{"message":"bad key"}}`, provider.CodeAuthRequired, false},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("%d", tc.status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer server.Close()

			adapter := NewAdapter(server.Client())
			_, err := adapter.Chat(context.Background(), provider.Endpoint{BaseURL: server.URL}, &provider.Request{
				Model:    "m1",
				Messages: []provider.Message{{Role: provider.RoleUser, Content: "hi"}},
			})
			pe := provider.AsError(err)
			if pe.Code != tc.wantCode || pe.Retryable != tc.retryable {
				t.Fatalf("错误 = %+v, want code=%s retryable=%v", pe, tc.wantCode, tc.retryable)
			}
		})
	}
}

func TestStreamSuccess(t *testing.T) {
	frames := []string{
		`data: {"choices":[{"delta":{"role":"assistant"}}]}`,
		`data: {"choices":[{"delta":{"reasoning_content":"想"}}]}`,
		`data: {"choices":[{"delta":{"content":"你"}}]}`,
		`data: {"choices":[{"delta":{"content":"好"}}]}`,
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c1","type":"function","function":{"name":"read","arguments":"{\"path\":"}}]}}]}`,
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"a.txt\"}"}}]}}]}`,
		`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`data: {"choices":[],"usage":{"prompt_tokens":10,"completion_tokens":5}}`,
		`data: [DONE]`,
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["stream"] != true {
			t.Errorf("stream = %v", body["stream"])
		}
		opts, _ := body["stream_options"].(map[string]any)
		if opts["include_usage"] != true {
			t.Errorf("stream_options = %+v", opts)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		for _, frame := range frames {
			_, _ = io.WriteString(w, frame+"\n\n")
			flusher.Flush()
		}
	}))
	defer server.Close()

	adapter := NewAdapter(server.Client())
	var events []provider.Event
	resp, err := adapter.Stream(context.Background(), provider.Endpoint{BaseURL: server.URL}, &provider.Request{
		Model:    "m1",
		Messages: []provider.Message{{Role: provider.RoleUser, Content: "hi"}},
	}, func(ev provider.Event) error {
		events = append(events, ev)
		return nil
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	gotTypes := make([]provider.EventType, 0, len(events))
	for _, ev := range events {
		gotTypes = append(gotTypes, ev.Type)
	}
	wantTypes := []provider.EventType{
		provider.EventThinking,
		provider.EventText,
		provider.EventText,
		provider.EventToolCall,
		provider.EventToolCall,
		provider.EventUsage,
		provider.EventDone,
	}
	if len(gotTypes) != len(wantTypes) {
		t.Fatalf("事件序列 = %+v, want %+v", gotTypes, wantTypes)
	}
	for i := range wantTypes {
		if gotTypes[i] != wantTypes[i] {
			t.Fatalf("第 %d 个事件 = %s, want %s（全部 %+v）", i, gotTypes[i], wantTypes[i], gotTypes)
		}
	}
	if resp.Thinking != "想" || resp.Text != "你好" || resp.StopReason != provider.StopToolUse {
		t.Fatalf("聚合响应不对: %+v", resp)
	}
	if resp.Usage.PromptTokens != 10 || resp.Usage.CompletionTokens != 5 {
		t.Fatalf("usage 不对: %+v", resp.Usage)
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Arguments["path"] != "a.txt" {
		t.Fatalf("工具参数拼接不对: %+v", resp.ToolCalls)
	}
}

func TestStreamInterrupted(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, `data: {"choices":[{"delta":{"content":"半截"}}]}`+"\n\n")
		w.(http.Flusher).Flush()
	}))
	defer server.Close()

	adapter := NewAdapter(server.Client())
	var gotText bool
	_, err := adapter.Stream(context.Background(), provider.Endpoint{BaseURL: server.URL}, &provider.Request{
		Model:    "m1",
		Messages: []provider.Message{{Role: provider.RoleUser, Content: "hi"}},
	}, func(ev provider.Event) error {
		if ev.Type == provider.EventText {
			gotText = true
		}
		return nil
	})
	if err == nil {
		t.Fatal("应返回流中断错误")
	}
	pe := provider.AsError(err)
	if pe.Code != provider.CodeStreamInterrupted || !pe.Retryable {
		t.Fatalf("错误 = %+v", pe)
	}
	if !gotText {
		t.Fatal("中断前的增量应已产生")
	}
}

func TestStreamHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, `{"error":{"message":"limit"}}`)
	}))
	defer server.Close()

	adapter := NewAdapter(server.Client())
	_, err := adapter.Stream(context.Background(), provider.Endpoint{BaseURL: server.URL}, &provider.Request{
		Model:    "m1",
		Messages: []provider.Message{{Role: provider.RoleUser, Content: "hi"}},
	}, func(provider.Event) error { return nil })
	pe := provider.AsError(err)
	if pe.Code != provider.CodeRateLimited || !pe.Retryable {
		t.Fatalf("错误 = %+v", pe)
	}
}

func TestParseSSELine(t *testing.T) {
	cases := []struct {
		line string
		data string
		ok   bool
	}{
		{"data: {\"a\":1}", `{"a":1}`, true},
		{" data: [DONE] ", "[DONE]", true},
		{": comment", "", false},
		{"event: message", "", false},
		{"", "", false},
	}
	for _, tc := range cases {
		data, ok := parseSSELine(tc.line)
		if data != tc.data || ok != tc.ok {
			t.Fatalf("parseSSELine(%q) = %q,%v", tc.line, data, ok)
		}
	}
}

func TestFinishReason(t *testing.T) {
	cases := map[provider.StopReason]string{
		provider.StopEndTurn:      "stop",
		provider.StopToolUse:      "tool_calls",
		provider.StopMaxTokens:    "length",
		provider.StopSafety:       "content_filter",
		provider.StopStopSequence: "stop",
		provider.StopOther:        "stop",
	}
	for in, want := range cases {
		if got := FinishReason(in); got != want {
			t.Fatalf("FinishReason(%s) = %s, want %s", in, got, want)
		}
	}
}
