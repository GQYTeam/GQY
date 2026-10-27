package plugin

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

func (e *rpcError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return fmt.Sprintf("插件错误 %d", e.Code)
}

type rpcMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int64          `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// stdioClient 是换行分隔 JSON-RPC 的多路复用客户端。
type stdioClient struct {
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	scanner *bufio.Scanner
	log     *os.File

	writeMu sync.Mutex
	pending map[int64]chan rpcMessage
	next    atomic.Int64

	done      chan struct{}
	doneOnce  sync.Once
	closeOnce sync.Once
	errMu     sync.Mutex
	err       error
}

func startStdio(ctx context.Context, m Manifest, host Host) (*stdioClient, error) {
	if len(m.Command) == 0 {
		return nil, errors.New("command 为空")
	}
	args := m.Command[1:]
	cmd := exec.Command(m.Command[0], args...)
	if !filepath.IsAbs(cmd.Path) && strings.Contains(cmd.Path, string(os.PathSeparator)) {
		cmd.Path = filepath.Join(m.Dir, cmd.Path)
	}
	cwd := m.Dir
	if m.Cwd != "" {
		if filepath.IsAbs(m.Cwd) {
			cwd = m.Cwd
		} else {
			cwd = filepath.Join(m.Dir, m.Cwd)
		}
	}
	cmd.Dir = cwd
	cmd.Env = os.Environ()
	for k, v := range m.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	setProcAttr(cmd)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	log, _ := openPluginLog(host, m.ID)
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("启动插件失败：%w", err)
	}

	c := &stdioClient{
		cmd: cmd, stdin: stdin, log: log,
		pending: map[int64]chan rpcMessage{},
		done:    make(chan struct{}),
	}
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64<<10), 4<<20)
	c.scanner = scanner

	go func() {
		for scanner.Scan() {
			line := scanner.Bytes()
			if len(strings.TrimSpace(string(line))) == 0 {
				continue
			}
			var msg rpcMessage
			if err := json.Unmarshal(line, &msg); err != nil {
				continue
			}
			if msg.ID != nil {
				c.deliver(*msg.ID, msg)
			}
		}
	}()
	go func() {
		_, _ = io.Copy(writerFunc(func(p []byte) (int, error) {
			if log != nil {
				_, _ = log.Write(p)
			}
			return len(p), nil
		}), stderr)
	}()
	go func() {
		err := cmd.Wait()
		c.errMu.Lock()
		c.err = err
		c.errMu.Unlock()
		if log != nil {
			_, _ = fmt.Fprintf(log, "\n[gqy] 进程退出：%v\n", err)
		}
		c.failPending(ErrExited)
		c.doneOnce.Do(func() { close(c.done) })
	}()

	select {
	case <-ctx.Done():
		_ = c.close(context.Background())
		return nil, ctx.Err()
	case <-time.After(50 * time.Millisecond):
		// 给初始化前的立即退出一个机会；不是必须等待。
	}
	select {
	case <-c.done:
		_ = c.close(context.Background())
		return nil, ErrExited
	default:
	}
	return c, nil
}

func openPluginLog(host Host, id string) (*os.File, error) {
	dir := filepath.Join(host.Home, "plugins")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return os.OpenFile(filepath.Join(dir, id+".log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
}

func (c *stdioClient) deliver(id int64, msg rpcMessage) {
	c.writeMu.Lock()
	ch := c.pending[id]
	delete(c.pending, id)
	c.writeMu.Unlock()
	if ch != nil {
		ch <- msg
	}
}

func (c *stdioClient) failPending(err error) {
	c.writeMu.Lock()
	pending := c.pending
	c.pending = map[int64]chan rpcMessage{}
	c.writeMu.Unlock()
	for _, ch := range pending {
		ch <- rpcMessage{Error: &rpcError{Code: -32000, Message: err.Error()}}
	}
}

func (c *stdioClient) call(ctx context.Context, method string, params any, out any) error {
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

	raw, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": id, "method": method, "params": params,
	})
	if err != nil {
		c.removePending(id)
		return err
	}
	if err := c.write(raw); err != nil {
		c.removePending(id)
		return err
	}
	select {
	case <-ctx.Done():
		c.removePending(id)
		return ctx.Err()
	case <-c.done:
		return ErrExited
	case msg := <-ch:
		if msg.Error != nil {
			return msg.Error
		}
		if out != nil && len(msg.Result) > 0 {
			if err := json.Unmarshal(msg.Result, out); err != nil {
				return err
			}
		}
		return nil
	}
}

func (c *stdioClient) notify(method string, params any) error {
	raw, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
	if err != nil {
		return err
	}
	return c.write(raw)
}

func (c *stdioClient) write(raw []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if c.stdin == nil {
		return ErrExited
	}
	_, err := c.stdin.Write(append(raw, '\n'))
	return err
}

func (c *stdioClient) removePending(id int64) {
	c.writeMu.Lock()
	delete(c.pending, id)
	c.writeMu.Unlock()
}

func (c *stdioClient) close(ctx context.Context) error {
	var err error
	c.closeOnce.Do(func() {
		// 先关标准输入：这是插件退出的生命线。
		if c.stdin != nil {
			_ = c.stdin.Close()
		}
		select {
		case <-c.done:
		case <-time.After(2 * time.Second):
			if c.cmd.Process != nil {
				terminateGroup(c.cmd.Process.Pid)
			}
			select {
			case <-c.done:
			case <-time.After(2 * time.Second):
				if c.cmd.Process != nil {
					killGroup(c.cmd.Process.Pid)
				}
				select {
				case <-c.done:
				case <-time.After(time.Second):
					err = errors.New("插件进程未退出")
				}
			}
		}
		if c.log != nil {
			_ = c.log.Close()
		}
	})
	return err
}

// writerFunc 把函数适配成 io.Writer。
type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }
