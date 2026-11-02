package httpapi

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"gqy/internal/config"
	"gqy/internal/gateway"
	"gqy/internal/provider"
	"gqy/internal/provider/gemini"
)

// handleGeminiGenerate 处理 POST /v1beta/models/{model}:generateContent 与 :streamGenerateContent。
func (s *Server) handleGeminiGenerate(w http.ResponseWriter, r *http.Request) {
	snap := snapshotFrom(r)
	if snap == nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "配置快照不可用")
		return
	}
	if s.gw == nil {
		writeError(w, http.StatusServiceUnavailable, provider.CodeNoCandidate, "网关未初始化")
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/v1beta/models/")
	stream := strings.HasSuffix(path, ":streamGenerateContent") || strings.EqualFold(r.URL.Query().Get("alt"), "sse")
	model := strings.TrimSuffix(strings.TrimSuffix(path, ":generateContent"), ":streamGenerateContent")
	model = strings.TrimPrefix(strings.TrimPrefix(model, "models/"), "")
	model = strings.TrimSpace(model)
	if model == "" {
		writeProviderError(w, provider.NewError(provider.CodeInvalidRequest, false, 400, "路径缺少模型名"))
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxChatBody)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, provider.CodeInvalidRequest, "请求体读取失败或超过上限")
		return
	}
	req, err := gemini.DecodeRequest(body)
	if err != nil {
		writeProviderError(w, err)
		return
	}
	req.Model = model
	req.Stream = stream

	meta := gateway.Meta{RequestID: requestIDFrom(r), SessionID: sessionIDFrom(r)}
	if stream {
		s.streamGemini(w, r, snap, req, meta)
		return
	}
	s.completeGemini(w, r, snap, req, meta)
}

func (s *Server) completeGemini(w http.ResponseWriter, r *http.Request, snap *config.Snapshot, req *provider.Request, meta gateway.Meta) {
	res, err := s.gw.Complete(r.Context(), snap, req, meta)
	if err != nil {
		writeProviderError(w, err)
		return
	}
	w.Header().Set("X-GQY-Provider", res.Provider)
	data, err := gemini.EncodeResponse(res.Response, res.Model)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "编码响应失败")
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data) // #nosec G705 -- JSON 响应，Content-Type 已显式设置
}

func (s *Server) streamGemini(w http.ResponseWriter, r *http.Request, snap *config.Snapshot, req *provider.Request, meta gateway.Meta) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeProviderError(w, provider.NewError(provider.CodeInvalidRequest, false, 500, "当前连接不支持流式响应"))
		return
	}

	started := false
	var stopReason provider.StopReason
	var usage *provider.Usage

	writeHeaders := func() {
		started = true
		w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")
	}

	emit := func(ev provider.Event) error {
		if !started {
			writeHeaders()
		}
		var frame map[string]any
		switch ev.Type {
		case provider.EventText:
			frame = geminiFrame(map[string]any{"text": ev.Delta}, "")
		case provider.EventThinking:
			frame = geminiFrame(map[string]any{"thought": ev.Delta}, "")
		case provider.EventToolCall:
			args := map[string]any{}
			if ev.ArgumentsDelta != "" {
				_ = json.Unmarshal([]byte(ev.ArgumentsDelta), &args)
			}
			frame = geminiFrame(map[string]any{"functionCall": map[string]any{"name": ev.Name, "args": args}}, "")
		case provider.EventUsage:
			if ev.Usage != nil {
				u := *ev.Usage
				usage = &u
			}
			return nil
		case provider.EventDone:
			stopReason = ev.StopReason
			return nil
		default:
			return nil
		}
		data, err := json.Marshal(frame)
		if err != nil {
			return err
		}
		if err := writeSSE(w, data); err != nil {
			return err
		}
		flusher.Flush()
		return nil
	}

	res, err := s.gw.Stream(r.Context(), snap, req, meta, emit)
	if err != nil {
		if !started {
			writeProviderError(w, err)
			return
		}
		pe := provider.AsError(err)
		payload, _ := json.Marshal(apiError{Error: errorDetail{Message: pe.Message, Type: "server_error", Code: pe.Code}})
		_ = writeSSE(w, payload)
		flusher.Flush()
		return
	}

	if !started {
		writeHeaders()
	}
	if res != nil && res.Response != nil && res.Response.StopReason != "" {
		stopReason = res.Response.StopReason
	}
	if stopReason == "" {
		stopReason = provider.StopEndTurn
	}
	if usage == nil && res != nil && res.Response != nil {
		u := res.Response.Usage
		usage = &u
	}
	final := map[string]any{
		"candidates": []map[string]any{{
			"content":      map[string]any{"role": "model", "parts": []map[string]any{}},
			"finishReason": gemini.FinishReasonString(stopReason),
		}},
	}
	if usage != nil {
		final["usageMetadata"] = map[string]any{
			"promptTokenCount":     usage.PromptTokens,
			"candidatesTokenCount": usage.CompletionTokens,
			"totalTokenCount":      usage.Total(),
		}
	}
	data, _ := json.Marshal(final)
	_ = writeSSE(w, data)
	flusher.Flush()
}

func geminiFrame(part map[string]any, finishReason string) map[string]any {
	frame := map[string]any{
		"candidates": []map[string]any{{
			"content": map[string]any{"role": "model", "parts": []map[string]any{part}},
		}},
	}
	if finishReason != "" {
		frame["candidates"].([]map[string]any)[0]["finishReason"] = finishReason
	}
	return frame
}
