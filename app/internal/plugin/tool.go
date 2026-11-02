package plugin

import (
	"context"
	"strings"

	"gqy/internal/tool"
)

// ToolName 把插件动作转成外部工具名：pluginID__action，非字母数字转 _，最长 64（P11）。
func ToolName(pluginID, action string) string {
	var b strings.Builder
	for _, r := range pluginID {
		if isAlnum(r) {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	b.WriteString("__")
	for _, r := range action {
		if isAlnum(r) {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	name := b.String()
	if len(name) > 64 {
		name = name[:64]
	}
	return name
}

func isAlnum(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
}

// ExternalTool 把插件动作包装成 tool.Tool（P11）。
type ExternalTool struct {
	PluginID string
	Action   Action
	asCore   bool
	manager  *Manager
}

func (e *ExternalTool) Name() string        { return ToolName(e.PluginID, e.Action.Name) }
func (e *ExternalTool) Description() string { return e.Action.Description }
func (e *ExternalTool) Schema() map[string]any {
	if e.Action.Schema == nil {
		return map[string]any{"type": "object"}
	}
	return e.Action.Schema
}

// Owner 标记该工具属于插件来源，供工具表热更新整体重建（HR2）。
func (e *ExternalTool) Owner() string { return "plugin:" + e.PluginID }

func (e *ExternalTool) Source() tool.Source {
	if e.asCore {
		return tool.SourceCore
	}
	return tool.SourceExternal
}

// Execute 通过插件管理器调用动作；manager 为空时返回 not_found。
func (e *ExternalTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	if e.manager == nil {
		return "", tool.ErrNotFound
	}
	return e.manager.Call(ctx, e.PluginID, e.Action.Name, args)
}

var _ tool.Tool = (*ExternalTool)(nil)
