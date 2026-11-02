package httpapi

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"gqy/internal/config"
	"gqy/internal/gateway"
	"gqy/internal/provider"
	"gqy/internal/provider/anthropic"
)

// handleAnthropicMessages 处理 POST /v1/messages。
func (s *Server) handleAnthropicMessages(w http.ResponseWriter, r *http.Request) {
	snap := snapshotFrom(r)
	if snap == nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "配置快照不可用")
		return
	}
	if s.gw == nil {
		writeError(w, http.StatusServiceUnavailable, provider.CodeNoCandidate, "网关未初始化")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxChatBody)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, provider.CodeInvalidRequest, "请求体读取失败或超过上限")
		return
	}
	req, err := anthropic.DecodeRequest(body)
	if err != nil {
		writeProviderError(w, err)
		return
	}

	meta := gateway.Meta{RequestID: requestIDFrom(r), SessionID: sessionIDFrom(r)}
	if req.Stream {
		s.streamAnthropic(w, r, snap, req, meta)
		return
	}
	s.completeAnthropic(w, r, snap, req, meta)
}

func (s *Server) completeAnthropic(w http.ResponseWriter, r *http.Request, snap *config.Snapshot, req *provider.Request, meta gateway.Meta) {
	res, err := s.gw.Complete(r.Context(), snap, req, meta)
	if err != nil {
		writeProviderError(w, err)
		return
	}
	w.Header().Set("X-GQY-Provider", res.Provider)
	data, err := anthropic.EncodeResponse(res.Response, messageID(meta.RequestID), res.Model)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "编码响应失败")
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data) // #nosec G705 -- JSON 响应，Content-Type 已显式设置
}

func (s *Server) streamAnthropic(w http.ResponseWriter, r *http.Request, snap *config.Snapshot, req *provider.Request, meta gateway.Meta) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeProviderError(w, provider.NewError(provider.CodeInvalidRequest, false, 500, "当前连接不支持流式响应"))
		return
	}

	id := messageID(meta.RequestID)
	var selectedModel string
	meta.OnSelect = func(_, _, model string) {
		selectedModel = model
	}

	started := false
	var writeHeaders func()
	var writePayload func(string, any) error
	writeHeaders = func() {
		started = true
		w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")
		_ = writePayload("message_start", anthropic.StreamPayload{
			Type: "message_start",
			Message: &anthropic.StreamMessage{
				ID:    id,
				Type:  "message",
				Role:  "assistant",
				Model: selectedModel,
			},
		})
	}

	type activeBlock struct {
		kind  string
		index int
	}
	var current *activeBlock
	blockIndex := 0
	textOpen, thinkingOpen := false, false
	var outputTokens int
	stopReason := provider.StopEndTurn

	writePayload = func(eventType string, payload any) error {
		if !started {
			writeHeaders()
		}
		data, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", eventType, data)
		return err
	}
	closeBlock := func() error {
		if current != nil {
			idx := current.index
			if err := writePayload("content_block_stop", anthropic.StreamPayload{Type: "content_block_stop", Index: &idx}); err != nil {
				return err
			}
			current = nil
		}
		return nil
	}

	emit := func(ev provider.Event) error {
		switch ev.Type {
		case provider.EventThinking:
			if err := closeBlock(); err != nil {
				return err
			}
			if !thinkingOpen {
				idx := blockIndex
				blockIndex++
				if err := writePayload("content_block_start", anthropic.StreamPayload{
					Type:  "content_block_start",
					Index: &idx,
					Block: &anthropic.StreamBlock{Type: "thinking", Thinking: ""},
				}); err != nil {
					return err
				}
				current = &activeBlock{kind: "thinking", index: idx}
				thinkingOpen = true
			}
			delta, _ := json.Marshal(map[string]any{"type": "thinking_delta", "thinking": ev.Delta})
			idx := current.index
			if err := writePayload("content_block_delta", anthropic.StreamPayload{Type: "content_block_delta", Index: &idx, Delta: delta}); err != nil {
				return err
			}
		case provider.EventText:
			if current != nil && current.kind != "text" {
				if err := closeBlock(); err != nil {
					return err
				}
			}
			if !textOpen {
				idx := blockIndex
				blockIndex++
				if err := writePayload("content_block_start", anthropic.StreamPayload{
					Type:  "content_block_start",
					Index: &idx,
					Block: &anthropic.StreamBlock{Type: "text", Text: ""},
				}); err != nil {
					return err
				}
				current = &activeBlock{kind: "text", index: idx}
				textOpen = true
			}
			delta, _ := json.Marshal(map[string]any{"type": "text_delta", "text": ev.Delta})
			idx := current.index
			if err := writePayload("content_block_delta", anthropic.StreamPayload{Type: "content_block_delta", Index: &idx, Delta: delta}); err != nil {
				return err
			}
		case provider.EventToolCall:
			if current != nil {
				if err := closeBlock(); err != nil {
					return err
				}
			}
			idx := ev.Index
			if idx < 0 {
				idx = blockIndex
			}
			blockIndex = idx + 1
			if err := writePayload("content_block_start", anthropic.StreamPayload{
				Type:  "content_block_start",
				Index: &idx,
				Block: &anthropic.StreamBlock{Type: "tool_use", ID: ev.ID, Name: ev.Name, Input: json.RawMessage("{}")},
			}); err != nil {
				return err
			}
			delta, _ := json.Marshal(map[string]any{"type": "input_json_delta", "partial_json": ev.ArgumentsDelta})
			if err := writePayload("content_block_delta", anthropic.StreamPayload{Type: "content_block_delta", Index: &idx, Delta: delta}); err != nil {
				return err
			}
			if err := writePayload("content_block_stop", anthropic.StreamPayload{Type: "content_block_stop", Index: &idx}); err != nil {
				return err
			}
		case provider.EventUsage:
			if ev.Usage != nil {
				outputTokens = ev.Usage.CompletionTokens
			}
		case provider.EventDone:
			stopReason = ev.StopReason
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
	if err := closeBlock(); err != nil {
		return
	}
	if res != nil && res.Response != nil && res.Response.StopReason != "" {
		stopReason = res.Response.StopReason
	}
	if stopReason == "" {
		stopReason = provider.StopEndTurn
	}
	if err := writePayload("message_delta", anthropic.StreamPayload{
		Type:  "message_delta",
		Delta: json.RawMessage(fmt.Sprintf(`{"stop_reason":%q}`, anthropic.StopReasonString(stopReason))),
		Usage: &anthropic.StreamUsage{OutputTokens: outputTokens},
	}); err != nil {
		return
	}
	_ = writePayload("message_stop", anthropic.StreamPayload{Type: "message_stop"})
	flusher.Flush()
}

func messageID(requestID string) string {
	if requestID == "" {
		requestID = fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return "msg_" + requestID
}
