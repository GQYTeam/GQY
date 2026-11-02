package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gqy/internal/persona"
	"gqy/internal/provider"
)

func TestPersonaEndpoints(t *testing.T) {
	e := newTestServer(t, "", nil)

	rec := e.request(http.MethodGet, "/api/persona", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET persona = %d", rec.Code)
	}
	var got persona.Persona
	decodeJSON(t, rec, &got)
	if got.Name != "GQY" {
		t.Fatalf("默认人格 = %+v", got)
	}

	body := `{"name":"小影","tagline":"本机助手","avatar":"data:image/png;base64,AAAA","system_prompt":"回答要短。"}`
	rec = e.request(http.MethodPut, "/api/persona", strings.NewReader(body), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT persona = %d: %s", rec.Code, rec.Body.String())
	}
	rec = e.request(http.MethodGet, "/api/persona", nil, nil)
	decodeJSON(t, rec, &got)
	if got.Name != "小影" || got.SystemPrompt != "回答要短。" {
		t.Fatalf("人格 = %+v", got)
	}
	if _, err := os.Stat(filepath.Join(e.home, "persona.json")); err != nil {
		t.Fatalf("persona.json 未落盘: %v", err)
	}

	rec = e.request(http.MethodPut, "/api/persona", strings.NewReader(`{"name":"x","avatar":"https://example.com/a.png"}`), nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("非法头像 = %d, want 400", rec.Code)
	}
}

func TestPersonaSystemPromptInjected(t *testing.T) {
	fake := &chatFakeAdapter{streamFn: func(_ context.Context, _ provider.Endpoint, req *provider.Request, emit func(provider.Event) error) (*provider.Response, error) {
		if len(req.Messages) == 0 || req.Messages[0].Role != provider.RoleSystem || !strings.Contains(req.Messages[0].Content, "回答要短") {
			t.Errorf("系统提示词未注入: %+v", req.Messages)
		}
		if err := emit(provider.Event{Type: provider.EventText, Delta: "好"}); err != nil {
			return nil, err
		}
		if err := emit(provider.Event{Type: provider.EventDone, StopReason: provider.StopEndTurn}); err != nil {
			return nil, err
		}
		return &provider.Response{Text: "好", StopReason: provider.StopEndTurn}, nil
	}}
	s := newChatServer(t, fake)
	home := s.mgr.Current().Home
	if err := persona.Save(home, persona.Persona{Name: "小影", SystemPrompt: "回答要短。"}); err != nil {
		t.Fatal(err)
	}

	rec := postPath(s, "/api/chat", `{"model":"p/m1","messages":[{"role":"user","content":"hi"}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("/api/chat = %d: %s", rec.Code, rec.Body.String())
	}
	var events []map[string]any
	for _, line := range strings.Split(rec.Body.String(), "\n") {
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var ev map[string]any
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &ev); err == nil {
			events = append(events, ev)
		}
	}
	if len(events) == 0 {
		t.Fatalf("没有 SSE 事件: %s", rec.Body.String())
	}
}
