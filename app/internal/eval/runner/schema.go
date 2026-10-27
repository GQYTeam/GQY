package runner

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"gqy/internal/tool"
)

// ToolView 是模型实际看到的一份工具说明。
type ToolView struct {
	ExposedName string         // 模型看到的名字（alias 变体下是短名）
	RealName    string         // 真实注册名
	Description string         // 模型看到的 description
	Parameters  map[string]any // 模型看到的 parameters schema
}

var aliasRE = regexp.MustCompile(`[^A-Za-z0-9_-]`)

// RenderToolSchema 按 schema 维度渲染工具说明。
func RenderToolSchema(t tool.Tool, variant string) (ToolView, error) {
	view := ToolView{ExposedName: t.Name(), RealName: t.Name(), Description: t.Description()}
	params, err := cloneMap(t.Schema())
	if err != nil {
		return ToolView{}, fmt.Errorf("工具 %s 的 schema 无法复制: %w", t.Name(), err)
	}
	view.Parameters = params

	switch variant {
	case "min":
		view.Description = firstSentence(t.Description(), 120)
		view.Parameters = minimalSchema(params)
	case "full":
		// 原样。
	case "example":
		addExamples(view.Parameters)
	case "negative":
		view.Description = strings.TrimSpace(t.Description() + " 不要用它做与职责无关的事，也不要编造参数。")
	case "alias":
		view.ExposedName = aliasName(t.Name())
	default:
		return ToolView{}, fmt.Errorf("未知 schema 变体 %q", variant)
	}
	return view, nil
}

func minimalSchema(full map[string]any) map[string]any {
	out := map[string]any{"type": "object"}
	required := stringList(full["required"])
	props, _ := full["properties"].(map[string]any)
	minProps := map[string]any{}
	for _, name := range required {
		if p, ok := props[name].(map[string]any); ok {
			minProps[name] = cloneValue(p)
		} else {
			minProps[name] = map[string]any{"type": "string"}
		}
	}
	out["properties"] = minProps
	if len(required) > 0 {
		out["required"] = required
	}
	return out
}

func addExamples(schema map[string]any) {
	required := stringList(schema["required"])
	props, _ := schema["properties"].(map[string]any)
	for _, name := range required {
		p, ok := props[name].(map[string]any)
		if !ok {
			continue
		}
		p["examples"] = []any{sampleFor(p)}
	}
}

func sampleFor(prop map[string]any) any {
	if enum, ok := prop["enum"].([]any); ok && len(enum) > 0 {
		return enum[0]
	}
	switch prop["type"] {
	case "number", "integer":
		return 1
	case "boolean":
		return true
	case "array":
		return []any{}
	case "object":
		return map[string]any{}
	default:
		return "示例值"
	}
}

// aliasName 把 plugin__echo__echo 收敛成 echo，验证命名影响。
func aliasName(name string) string {
	parts := strings.Split(name, "__")
	out := parts[len(parts)-1]
	out = aliasRE.ReplaceAllString(out, "_")
	out = strings.Trim(out, "_-")
	if out == "" {
		return "tool"
	}
	return out
}

func cloneMap(in map[string]any) (map[string]any, error) {
	if in == nil {
		return map[string]any{}, nil
	}
	data, err := json.Marshal(in)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = map[string]any{}
	}
	return out, nil
}

func cloneValue(in any) any {
	data, err := json.Marshal(in)
	if err != nil {
		return in
	}
	var out any
	if err := json.Unmarshal(data, &out); err != nil {
		return in
	}
	return out
}

func firstSentence(s string, maxRunes int) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	cut := len(s)
	for _, sep := range []string{"。", "！", "？", ". ", "! ", "? ", "\n"} {
		if i := strings.Index(s, sep); i >= 0 && i+len(sep) <= cut {
			cut = i + len(sep)
		}
	}
	s = strings.TrimSpace(s[:cut])
	return truncateRunes(s, maxRunes)
}

func truncateRunes(s string, max int) string {
	if max <= 0 || utf8.RuneCountInString(s) <= max {
		return s
	}
	r := []rune(s)
	return string(r[:max]) + "…"
}

func stringList(v any) []string {
	switch x := v.(type) {
	case []string:
		return append([]string(nil), x...)
	case []any:
		out := make([]string, 0, len(x))
		for _, item := range x {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}
