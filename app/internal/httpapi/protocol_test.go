package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gqy/internal/config"
	"gqy/internal/gateway"
)

func protocolServer(t *testing.T) *Server {
	t.Helper()
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": "你好"}}},
			"usage":   map[string]any{"prompt_tokens": 3, "completion_tokens": 2, "total_tokens": 5},
		})
	}))
	t.Cleanup(up.Close)
	home := t.TempDir()
	raw, _ := json.Marshal(map[string]any{"providers": []map[string]any{
		{"id": "fake", "base_url": up.URL, "default_model": "m", "api_key": "k"},
	}})
	if err := os.WriteFile(filepath.Join(home, "providers.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{Home: home}
	return &Server{Cfg: cfg, Gateway: gateway.New(cfg, http.DefaultClient)}
}

func TestAnthropicMessagesEndpoint(t *testing.T) {
	s := protocolServer(t)
	body := `{"model":"fake/m","max_tokens":100,"messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}]}`
	req := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(body))
	rec := httptest.NewRecorder()
	s.handleAnthropicMessages(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var out struct {
		Type    string `json:"type"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Usage struct {
			In  int `json:"input_tokens"`
			Out int `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Type != "message" || len(out.Content) != 1 || out.Content[0].Text != "你好" || out.Usage.In != 3 {
		t.Fatalf("response: %+v", out)
	}
}

func TestGeminiGenerateEndpoint(t *testing.T) {
	s := protocolServer(t)
	body := `{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`
	req := httptest.NewRequest("POST", "/v1beta/models/fake/m:generateContent", strings.NewReader(body))
	rec := httptest.NewRecorder()
	s.handleGeminiGenerate(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var out struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
		Usage struct {
			Total int `json:"totalTokenCount"`
		} `json:"usageMetadata"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Candidates) != 1 || len(out.Candidates[0].Content.Parts) != 1 || out.Candidates[0].Content.Parts[0].Text != "你好" || out.Usage.Total != 5 {
		t.Fatalf("response: %+v", out)
	}
}

func TestAnthropicStream(t *testing.T) {
	s := protocolServer(t)
	body := `{"model":"fake/m","stream":true,"messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(body))
	rec := httptest.NewRecorder()
	s.handleAnthropicMessages(rec, req)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "message_start") || !strings.Contains(rec.Body.String(), "text_delta") {
		t.Fatalf("stream: %d %s", rec.Code, rec.Body.String())
	}
}

func TestGeminiStream(t *testing.T) {
	s := protocolServer(t)
	body := `{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`
	req := httptest.NewRequest("POST", "/v1beta/models/fake/m:streamGenerateContent", strings.NewReader(body))
	rec := httptest.NewRecorder()
	s.handleGeminiGenerate(rec, req)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "data:") || !strings.Contains(rec.Body.String(), "finishReason") {
		t.Fatalf("stream: %d %s", rec.Code, rec.Body.String())
	}
}

func TestAnthropicContentBlocks(t *testing.T) {
	var raw json.RawMessage = []byte(`[{"type":"text","text":"看"},{"type":"image","source":{"type":"base64","media_type":"image/png","data":"AA=="}}]`)
	text, images := anthropicContent(raw)
	if text != "看" || len(images) != 1 || !strings.HasPrefix(images[0], "data:image/png") {
		t.Fatalf("text=%q images=%v", text, images)
	}
}
