package plugin

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"
)

// rpcMessage 是换行 JSON-RPC 2.0 的一行。
type rpcMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// transport 在一条双向字节流上做「请求序号 → 等待通道」多路复用（P6）。
type transport struct {
	conn   io.ReadWriteCloser
	reader *bufio.Reader
	writer io.Writer

	mu      sync.Mutex
	writeMu sync.Mutex
	nextID  int64
	pending map[int64]chan rpcMessage
}

func newTransport(conn io.ReadWriteCloser) *transport {
	return &transport{
		conn:    conn,
		reader:  bufio.NewReaderSize(conn, 8<<20),
		writer:  conn,
		pending: make(map[int64]chan rpcMessage),
	}
}

// start 启动读循环，按 id 派发响应；晚到/未知 id 直接丢弃。
func (t *transport) start() {
	for {
		line, err := t.reader.ReadBytes('\n')
		if len(line) > 0 {
			var msg rpcMessage
			if json.Unmarshal(line, &msg) == nil {
				id, ok := msg.ID.(float64)
				if ok {
					t.mu.Lock()
					ch := t.pending[int64(id)]
					delete(t.pending, int64(id))
					t.mu.Unlock()
					if ch != nil {
						ch <- msg
					}
				}
			}
		}
		if err != nil {
			t.closePending()
			return
		}
	}
}

func (t *transport) closePending() {
	t.mu.Lock()
	defer t.mu.Unlock()
	for id, ch := range t.pending {
		close(ch)
		delete(t.pending, id)
	}
}

// call 发送请求并等待对应 id 的响应；ctx 超时则删除 pending。
func (t *transport) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	t.mu.Lock()
	t.nextID++
	id := t.nextID
	ch := make(chan rpcMessage, 1)
	t.pending[id] = ch
	t.mu.Unlock()

	body := map[string]any{"jsonrpc": "2.0", "id": id, "method": method}
	if params != nil {
		body["params"] = params
	}
	if err := t.writeJSON(body); err != nil {
		t.mu.Lock()
		delete(t.pending, id)
		t.mu.Unlock()
		return nil, err
	}

	select {
	case <-ctx.Done():
		t.mu.Lock()
		delete(t.pending, id)
		t.mu.Unlock()
		return nil, ctx.Err()
	case msg, ok := <-ch:
		if !ok {
			return nil, ErrExited
		}
		if msg.Error != nil {
			return nil, &callError{code: msg.Error.Code, message: msg.Error.Message}
		}
		return msg.Result, nil
	}
}

// notify 发送无 id 通知。
func (t *transport) notify(method string, params any) error {
	body := map[string]any{"jsonrpc": "2.0", "method": method}
	if params != nil {
		body["params"] = params
	}
	return t.writeJSON(body)
}

func (t *transport) writeJSON(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	t.writeMu.Lock()
	defer t.writeMu.Unlock()
	_, err = t.writer.Write(data)
	return err
}

type callError struct {
	code    int
	message string
}

func (e *callError) Error() string {
	return fmt.Sprintf("call_failed: %s", e.message)
}
