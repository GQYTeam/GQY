package oneshot

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
)

// rpc 是一次性插件的单请求 JSON-RPC 客户端（NX3：call 语义）。
type rpc struct {
	conn io.ReadWriteCloser
	r    *bufio.Reader
	mu   sync.Mutex
}

func newRPC(conn io.ReadWriteCloser) *rpc {
	return &rpc{conn: conn, r: bufio.NewReaderSize(conn, 8<<20)}
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// call 发送 id=1 的请求并读取匹配响应；超时由 ctx 控制。
func (c *rpc) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	body := map[string]any{"jsonrpc": "2.0", "id": 1, "method": method}
	if params != nil {
		body["params"] = params
	}
	data, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	data = append(data, '\n')
	if _, err := c.conn.Write(data); err != nil {
		return nil, err
	}

	type result struct {
		raw json.RawMessage
		err error
	}
	ch := make(chan result, 1)
	go func() {
		line, err := c.r.ReadBytes('\n')
		if err != nil {
			ch <- result{err: err}
			return
		}
		var msg struct {
			ID     int             `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  *rpcError       `json:"error"`
		}
		if err := json.Unmarshal(line, &msg); err != nil {
			ch <- result{err: fmt.Errorf("oneshot: 响应不是合法 JSON: %w", err)}
			return
		}
		if msg.ID != 1 {
			ch <- result{err: fmt.Errorf("oneshot: 响应 id 不匹配: %d", msg.ID)}
			return
		}
		if msg.Error != nil {
			ch <- result{err: fmt.Errorf("call_failed: %s", msg.Error.Message)}
			return
		}
		ch <- result{raw: msg.Result}
	}()

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case res := <-ch:
		if res.err != nil && errors.Is(res.err, io.EOF) {
			return nil, errors.New("exited")
		}
		return res.raw, res.err
	}
}
