package plugin

import (
	"context"

	"gqy/internal/tool"
)

// pluginTool 把插件动作暴露成外部工具。
type pluginTool struct {
	manager    *Manager
	pluginID   string
	pluginName string
	action     Action
	name       string
}

func (t *pluginTool) Name() string { return t.name }

func (t *pluginTool) Description() string {
	desc := t.action.Description
	if desc == "" {
		desc = t.action.Name
	}
	return "[" + t.pluginName + "] " + desc
}

func (t *pluginTool) Schema() map[string]any {
	if t.action.Schema == nil {
		return map[string]any{"type": "object", "properties": map[string]any{}}
	}
	return t.action.Schema
}

func (t *pluginTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	return t.manager.Call(ctx, t.pluginID, t.action.Name, args)
}

func (t *pluginTool) Kind() tool.Kind { return tool.KindExternal }
