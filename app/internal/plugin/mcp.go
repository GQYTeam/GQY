package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// MCPAdapter 是 MCP stdio 适配器（P4）。
type MCPAdapter struct{}

func (a *MCPAdapter) Protocol() string { return ProtocolMCP }
func (a *MCPAdapter) Match(community string) bool {
	return community == "mcp"
}

// Load 拉起进程并完成 MCP 最小握手：initialize → notifications/initialized → tools/list。
func (a *MCPAdapter) Load(ctx context.Context, m *Manifest, dir string) (adapterInstance, error) {
	proc, err := startProcess(ctx, m, dir)
	if err != nil {
		return nil, err
	}
	tr := newTransport(proc)
	go tr.start()

	initCtx, cancel := context.WithTimeout(ctx, timeout(m.TimeoutMS))
	defer cancel()
	if _, err := tr.call(initCtx, "initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "gqy", "version": "0.1.0"},
	}); err != nil {
		_ = proc.Close()
		return nil, err
	}
	_ = tr.notify("notifications/initialized", nil)

	listCtx, cancel2 := context.WithTimeout(ctx, timeout(m.TimeoutMS))
	defer cancel2()
	result, err := tr.call(listCtx, "tools/list", nil)
	if err != nil {
		_ = proc.Close()
		return nil, err
	}
	var list struct {
		Tools []struct {
			Name        string         `json:"name"`
			Description string         `json:"description"`
			InputSchema map[string]any `json:"inputSchema"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(result, &list); err != nil {
		_ = proc.Close()
		return nil, fmt.Errorf("call_failed: tools/list 响应非法")
	}
	actions := make([]Action, 0, len(list.Tools))
	for _, t := range list.Tools {
		actions = append(actions, Action{Name: t.Name, Description: t.Description, Schema: t.InputSchema})
	}
	return &mcpInstance{proc: proc, tr: tr, actions: actions}, nil
}

type mcpInstance struct {
	proc    process
	tr      *transport
	actions []Action
}

func (m *mcpInstance) PID() int          { return m.proc.PID() }
func (m *mcpInstance) Actions() []Action { return m.actions }

func (m *mcpInstance) Call(ctx context.Context, action string, args map[string]any) (string, error) {
	result, err := m.tr.call(ctx, "tools/call", map[string]any{"name": action, "arguments": args})
	if err != nil {
		return "", err
	}
	var out struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(result, &out); err != nil {
		return "", fmt.Errorf("%w: tools/call 响应非法", ErrCallFailed)
	}
	if out.IsError {
		return "", fmt.Errorf("%w: isError", ErrCallFailed)
	}
	var b strings.Builder
	for _, c := range out.Content {
		if c.Type == "text" {
			b.WriteString(c.Text)
		}
	}
	return b.String(), nil
}

func (m *mcpInstance) Close(ctx context.Context) error {
	shutdownCtx, cancel := context.WithTimeout(ctx, timeout(5000))
	defer cancel()
	_, _ = m.tr.call(shutdownCtx, "shutdown", nil)
	return m.proc.Close()
}
