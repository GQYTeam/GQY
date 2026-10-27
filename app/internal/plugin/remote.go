package plugin

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

// RemoteAdapter 连接已经运行的插件服务：Unix Socket 或 TCP。
// 协议与原生 stdio 相同（换行分隔 JSON-RPC），不负责拉起进程。
type RemoteAdapter struct{}

func (RemoteAdapter) ID() string { return "remote" }

func (RemoteAdapter) Match(m Manifest) bool {
	return m.Protocol == "unix" || m.Protocol == "tcp"
}

func (RemoteAdapter) Load(ctx context.Context, m Manifest, host Host) (Instance, error) {
	network := "unix"
	addr := m.Socket
	if m.Protocol == "tcp" {
		network = "tcp"
	} else if !filepath.IsAbs(addr) {
		addr = filepath.Join(m.Dir, addr)
	}
	timeout := time.Duration(m.TimeoutMS) * time.Millisecond
	dialCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var d net.Dialer
	conn, err := d.DialContext(dialCtx, network, addr)
	if err != nil {
		return nil, fmt.Errorf("连接插件服务失败：%w", err)
	}
	c := newRemoteClient(conn)
	var res struct {
		OK bool `json:"ok"`
	}
	if err := c.call(dialCtx, "initialize", map[string]any{"protocol": "gqy-jsonrpc-v1"}, &res); err != nil {
		_ = c.close()
		return nil, err
	}
	if !res.OK {
		_ = c.close()
		return nil, errors.New("插件 initialize 未返回 ok")
	}
	return &remoteInstance{manifest: m, client: c}, nil
}

type remoteClient struct {
	conn net.Conn
	done chan struct{}
	once sync.Once

	writeMu sync.Mutex
	pending map[int64]chan rpcMessage
	next    atomic.Int64
	errMu   sync.Mutex
	err     error
}

func newRemoteClient(conn net.Conn) *remoteClient {
	c := &remoteClient{conn: conn, done: make(chan struct{}), pending: map[int64]chan rpcMessage{}}
	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 64<<10), 4<<20)
	go func() {
		for scanner.Scan() {
			var msg rpcMessage
			if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
				continue
			}
			if msg.ID != nil {
				c.writeMu.Lock()
				ch := c.pending[*msg.ID]
				delete(c.pending, *msg.ID)
				c.writeMu.Unlock()
				if ch != nil {
					ch <- msg
				}
			}
		}
		c.errMu.Lock()
		c.err = scanner.Err()
		c.errMu.Unlock()
		c.failPending(ErrExited)
		c.once.Do(func() { close(c.done) })
	}()
	return c
}

func (c *remoteClient) call(ctx context.Context, method string, params any, out any) error {
	select {
	case <-c.done:
		return ErrExited
	default:
	}
	id := c.next.Add(1)
	ch := make(chan rpcMessage, 1)
	c.writeMu.Lock()
	c.pending[id] = ch
	c.writeMu.Unlock()
	raw, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	if err != nil {
		return err
	}
	c.writeMu.Lock()
	_, werr := c.conn.Write(append(raw, '\n'))
	if werr != nil {
		delete(c.pending, id)
	}
	c.writeMu.Unlock()
	if werr != nil {
		return werr
	}
	select {
	case <-ctx.Done():
		c.writeMu.Lock()
		delete(c.pending, id)
		c.writeMu.Unlock()
		return ctx.Err()
	case <-c.done:
		return ErrExited
	case msg := <-ch:
		if msg.Error != nil {
			return msg.Error
		}
		if out != nil && len(msg.Result) > 0 {
			return json.Unmarshal(msg.Result, out)
		}
		return nil
	}
}

func (c *remoteClient) failPending(err error) {
	c.writeMu.Lock()
	pending := c.pending
	c.pending = map[int64]chan rpcMessage{}
	c.writeMu.Unlock()
	for _, ch := range pending {
		ch <- rpcMessage{Error: &rpcError{Code: -32000, Message: err.Error()}}
	}
}

func (c *remoteClient) close() error {
	return c.conn.Close()
}

type remoteInstance struct {
	manifest Manifest
	client   *remoteClient
}

func (r *remoteInstance) Call(ctx context.Context, action string, args map[string]any) (string, error) {
	var res struct {
		Content string `json:"content"`
	}
	if err := r.client.call(ctx, "call", map[string]any{"action": action, "arguments": args}, &res); err != nil {
		return "", err
	}
	return res.Content, nil
}

func (r *remoteInstance) Close(ctx context.Context) error {
	shCtx, cancel := context.WithTimeout(ctx, time.Second)
	_ = r.client.call(shCtx, "shutdown", map[string]any{}, nil)
	cancel()
	return r.client.close()
}

func (r *remoteInstance) Info() Info {
	return Info{Protocol: r.manifest.Protocol, Actions: r.manifest.Actions, StartedAt: time.Now()}
}
