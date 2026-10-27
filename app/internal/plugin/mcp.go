package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

// MCPAdapter 对接 MCP stdio（2024-11-05 最小集）。
type MCPAdapter struct{}

func (MCPAdapter) ID() string { return "mcp" }

func (MCPAdapter) Match(m Manifest) bool { return m.Protocol == "mcp" }

func (MCPAdapter) Load(ctx context.Context, m Manifest, host Host) (Instance, error) {
	c, err := startStdio(ctx, m, host)
	if err != nil {
		return nil, err
	}
	timeout := time.Duration(m.TimeoutMS) * time.Millisecond
	initCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var initRes struct {
		ProtocolVersion string         `json:"protocolVersion"`
		Capabilities    map[string]any `json:"capabilities"`
		ServerInfo      map[string]any `json:"serverInfo"`
	}
	if err := c.call(initCtx, "initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "gqy", "version": "1.0.0"},
	}, &initRes); err != nil {
		_ = c.close(context.Background())
		return nil, err
	}
	_ = c.notify("notifications/initialized", map[string]any{})

	var listRes struct {
		Tools []struct {
			Name        string         `json:"name"`
			Description string         `json:"description"`
			InputSchema map[string]any `json:"inputSchema"`
		} `json:"tools"`
	}
	if err := c.call(initCtx, "tools/list", map[string]any{}, &listRes); err != nil {
		_ = c.close(context.Background())
		return nil, err
	}
	actions := make([]Action, 0, len(listRes.Tools))
	for _, t := range listRes.Tools {
		if t.Name == "" {
			continue
		}
		actions = append(actions, Action{Name: t.Name, Description: t.Description, Schema: t.InputSchema})
	}
	return &mcpInstance{manifest: m, client: c, actions: actions}, nil
}

type mcpInstance struct {
	manifest Manifest
	client   *stdioClient
	actions  []Action
}

func (m *mcpInstance) Call(ctx context.Context, action string, args map[string]any) (string, error) {
	var res struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Structured map[string]any `json:"structuredContent"`
		IsError    bool           `json:"isError"`
	}
	if err := m.client.call(ctx, "tools/call", map[string]any{
		"name": action, "arguments": args,
	}, &res); err != nil {
		return "", err
	}
	var text string
	for _, c := range res.Content {
		if c.Text != "" {
			text += c.Text
		}
	}
	if text == "" && res.Structured != nil {
		b, _ := json.Marshal(res.Structured)
		text = string(b)
	}
	if res.IsError {
		if text == "" {
			text = "MCP 工具返回错误"
		}
		return "", errors.New(text)
	}
	return text, nil
}

func (m *mcpInstance) Close(ctx context.Context) error {
	shCtx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
	_ = m.client.call(shCtx, "shutdown", map[string]any{}, nil)
	cancel()
	return m.client.close(ctx)
}

func (m *mcpInstance) Info() Info {
	return Info{
		PID:       m.client.cmd.Process.Pid,
		StartedAt: time.Now(),
		Protocol:  "mcp",
		Actions:   m.actions,
	}
}
