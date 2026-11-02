package agent

import "gqy/internal/provider"

// ToolDecls 把注册表里的核心工具转成模型工具声明（T2：核心自动注入）。
func (l *Loop) ToolDecls() []provider.Tool {
	out := make([]provider.Tool, 0)
	for _, t := range l.Tools.Core() {
		out = append(out, provider.Tool{
			Name:        t.Name(),
			Description: t.Description(),
			Parameters:  t.Schema(),
		})
	}
	return out
}

// ToolInfo 是读接口使用的工具描述（T1、T3）。
type ToolInfo struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Schema      map[string]any `json:"schema"`
	Source      string         `json:"source"`
}

// ToolInfos 返回核心与外部工具的只读清单。
func (l *Loop) ToolInfos() []ToolInfo {
	out := make([]ToolInfo, 0)
	for _, t := range l.Tools.Core() {
		out = append(out, ToolInfo{Name: t.Name(), Description: t.Description(), Schema: t.Schema(), Source: "core"})
	}
	for _, t := range l.Tools.External() {
		out = append(out, ToolInfo{Name: t.Name(), Description: t.Description(), Schema: t.Schema(), Source: "external"})
	}
	return out
}

// artifactsFor 从一次成功的工具调用推断产物路径（022）。
// 目前只有 edit 会写文件；后续新增写文件工具时在这里登记。
func artifactsFor(name string, args map[string]any) []string {
	if name != "edit" {
		return nil
	}
	if p, ok := args["path"].(string); ok && p != "" {
		return []string{p}
	}
	return nil
}
