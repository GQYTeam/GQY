package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// NativeAdapter 是 gqy-jsonrpc-v1 适配器（P4）。
type NativeAdapter struct{}

func (a *NativeAdapter) Protocol() string { return ProtocolNative }
func (a *NativeAdapter) Match(community string) bool {
	return community == "gqy" || community == ""
}

// Load 拉起进程并完成 initialize 握手。
func (a *NativeAdapter) Load(ctx context.Context, m *Manifest, dir string) (adapterInstance, error) {
	proc, err := startProcess(ctx, m, dir)
	if err != nil {
		return nil, err
	}
	tr := newTransport(proc)
	go tr.start()

	initCtx, cancel := context.WithTimeout(ctx, timeout(m.TimeoutMS))
	defer cancel()
	result, err := tr.call(initCtx, "initialize", map[string]any{"protocol": ProtocolNative, "settings": m.Values})
	if err != nil {
		_ = proc.Close()
		return nil, err
	}
	var init struct {
		OK bool `json:"ok"`
	}
	if err := json.Unmarshal(result, &init); err != nil || !init.OK {
		_ = proc.Close()
		return nil, fmt.Errorf("call_failed: initialize 失败")
	}
	return &nativeInstance{proc: proc, tr: tr, manifest: m}, nil
}

type nativeInstance struct {
	proc     process
	tr       *transport
	manifest *Manifest
}

func (n *nativeInstance) PID() int { return n.proc.PID() }

func (n *nativeInstance) Actions() []Action { return n.manifest.Actions }

func (n *nativeInstance) Call(ctx context.Context, action string, args map[string]any) (string, error) {
	result, err := n.tr.call(ctx, "call", map[string]any{"action": action, "arguments": args})
	if err != nil {
		var ce *callError
		if ok := asCallError(err, &ce); ok {
			return "", fmt.Errorf("%w: %s", ErrCallFailed, ce.message)
		}
		return "", err
	}
	var out struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal(result, &out); err != nil {
		return "", fmt.Errorf("%w: result.content 非法", ErrCallFailed)
	}
	return out.Content, nil
}

func (n *nativeInstance) Close(ctx context.Context) error {
	shutdownCtx, cancel := context.WithTimeout(ctx, timeout(5000))
	defer cancel()
	_, _ = n.tr.call(shutdownCtx, "shutdown", nil)
	return n.proc.Close()
}

func asCallError(err error, target **callError) bool {
	var ce *callError
	if errors.As(err, &ce) {
		*target = ce
		return true
	}
	return false
}
