package httpapi

// 持久连接（SSE）：把配置/插件/全局模型的变化实时推给 WebUI 与外部客户端（HR7）。
// 客户端只保持一条 EventSource 连接，不再轮询状态接口。

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"
)

// eventHub 是最小的事件广播：每个订阅者一个缓冲通道，慢客户端丢弃而不是阻塞发布方。
type eventHub struct {
	mu   sync.Mutex
	subs map[chan []byte]struct{}
}

func newEventHub() *eventHub {
	return &eventHub{subs: make(map[chan []byte]struct{})}
}

func (h *eventHub) subscribe() chan []byte {
	ch := make(chan []byte, 16)
	h.mu.Lock()
	h.subs[ch] = struct{}{}
	h.mu.Unlock()
	return ch
}

func (h *eventHub) unsubscribe(ch chan []byte) {
	h.mu.Lock()
	delete(h.subs, ch)
	h.mu.Unlock()
}

// closeAll 在服务退出时关掉所有 SSE 长连接：订阅者收到通道关闭后返回，http.Server.Shutdown 才等得到在途请求结束。
// 不关的话，SSE 的请求上下文不随 Shutdown 取消，优雅关闭会一直等到超时。
func (h *eventHub) closeAll() {
	h.mu.Lock()
	for ch := range h.subs {
		close(ch)
		delete(h.subs, ch)
	}
	h.mu.Unlock()
}

// publish 广播一个事件；data 会被编码成 JSON。
func (h *eventHub) publish(kind string, data any) {
	payload, err := json.Marshal(map[string]any{"type": kind, "data": data, "ts": time.Now().UTC().Format(time.RFC3339)})
	if err != nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs {
		select {
		case ch <- payload:
		default: // 慢客户端：丢弃这一条，绝不阻塞
		}
	}
}

// Publish 是对外发布入口（main 里的配置/插件钩子调用）。
func (s *Server) Publish(kind string, data any) {
	if s.events != nil {
		s.events.publish(kind, data)
	}
}

// CloseEvents 关掉所有 SSE 长连接，供进程退出时在 http.Server.Shutdown 之前调用。
func (s *Server) CloseEvents() {
	if s.events != nil {
		s.events.closeAll()
	}
}

// handleEvents 处理 GET /api/events：SSE 长连接，15s 心跳。
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "unsupported", "当前连接不支持流式响应")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	ch := s.events.subscribe()
	defer s.events.unsubscribe(ch)

	// 连接建立时先推一条 ready，客户端据此确认已连上。
	_, _ = w.Write([]byte("data: {\"type\":\"ready\"}\n\n"))
	flusher.Flush()

	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-heartbeat.C:
			if _, err := w.Write([]byte(": ping\n\n")); err != nil {
				return
			}
			flusher.Flush()
		case payload, ok := <-ch:
			if !ok {
				return
			}
			if _, err := w.Write(append(append([]byte("data: "), payload...), '\n', '\n')); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}
