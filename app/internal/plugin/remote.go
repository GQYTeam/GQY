package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
)

// RemoteAdapter 是 unix / tcp 远程适配器：只连接不拉起、绝不杀远程进程（P4、SEC6）。
type RemoteAdapter struct{}

func (a *RemoteAdapter) Protocol() string { return ProtocolUnix }

func (a *RemoteAdapter) Match(community string) bool { return community == "remote" }

func (a *RemoteAdapter) Load(ctx context.Context, m *Manifest, dir string) (adapterInstance, error) {
	network := m.Protocol
	if network == "" {
		network = ProtocolUnix
	}
	var conn net.Conn
	var err error
	switch network {
	case ProtocolUnix:
		d := net.Dialer{}
		conn, err = d.DialContext(ctx, "unix", m.Address)
	case ProtocolTCP:
		d := net.Dialer{}
		conn, err = d.DialContext(ctx, "tcp", m.Address)
	default:
		return nil, fmt.Errorf("call_failed: 未知远程协议 %s", network)
	}
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrExited, err)
	}

	tr := newTransport(conn)
	go tr.start()

	initCtx, cancel := context.WithTimeout(ctx, timeout(m.TimeoutMS))
	defer cancel()
	if _, err := tr.call(initCtx, "initialize", map[string]any{"protocol": m.Protocol}); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return &remoteInstance{conn: conn, tr: tr, actions: m.Actions}, nil
}

type remoteInstance struct {
	conn    net.Conn
	tr      *transport
	actions []Action
}

func (r *remoteInstance) PID() int          { return 0 }
func (r *remoteInstance) Actions() []Action { return r.actions }

func (r *remoteInstance) Call(ctx context.Context, action string, args map[string]any) (string, error) {
	result, err := r.tr.call(ctx, "call", map[string]any{"action": action, "arguments": args})
	if err != nil {
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

func (r *remoteInstance) Close(ctx context.Context) error {
	shutdownCtx, cancel := context.WithTimeout(ctx, timeout(5000))
	defer cancel()
	_, _ = r.tr.call(shutdownCtx, "shutdown", nil)
	return r.conn.Close()
}
