package plugin

import (
	"context"
	"errors"
	"time"
)

// NativeAdapter 对接 gqy-jsonrpc-v1：换行分隔 JSON-RPC 2.0。
type NativeAdapter struct{}

func (NativeAdapter) ID() string { return "gqy-jsonrpc-v1" }

func (NativeAdapter) Match(m Manifest) bool {
	return m.Protocol == "" || m.Protocol == "gqy-jsonrpc-v1"
}

func (NativeAdapter) Load(ctx context.Context, m Manifest, host Host) (Instance, error) {
	c, err := startStdio(ctx, m, host)
	if err != nil {
		return nil, err
	}
	var res struct {
		OK      bool   `json:"ok"`
		Name    string `json:"name"`
		Version string `json:"version"`
	}
	initCtx, cancel := context.WithTimeout(ctx, time.Duration(m.TimeoutMS)*time.Millisecond)
	defer cancel()
	if err := c.call(initCtx, "initialize", map[string]any{
		"protocol": "gqy-jsonrpc-v1",
		"version":  m.Version,
	}, &res); err != nil {
		_ = c.close(context.Background())
		return nil, err
	}
	if !res.OK {
		_ = c.close(context.Background())
		return nil, errors.New("插件 initialize 未返回 ok")
	}
	th := m.TimeoutMS
	return &nativeInstance{manifest: m, client: c, timeout: time.Duration(th) * time.Millisecond}, nil
}

type nativeInstance struct {
	manifest Manifest
	client   *stdioClient
	timeout  time.Duration
}

func (n *nativeInstance) Call(ctx context.Context, action string, args map[string]any) (string, error) {
	var res struct {
		Content string `json:"content"`
	}
	if err := n.client.call(ctx, "call", map[string]any{
		"action": action, "arguments": args,
	}, &res); err != nil {
		return "", err
	}
	return res.Content, nil
}

func (n *nativeInstance) Close(ctx context.Context) error {
	// 先礼貌 shutdown，失败也不影响后面的关闭。
	shCtx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
	_ = n.client.call(shCtx, "shutdown", map[string]any{}, nil)
	cancel()
	return n.client.close(ctx)
}

func (n *nativeInstance) Info() Info {
	return Info{
		PID:       n.client.cmd.Process.Pid,
		StartedAt: time.Now(),
		Protocol:  "gqy-jsonrpc-v1",
		Actions:   n.manifest.Actions,
	}
}
