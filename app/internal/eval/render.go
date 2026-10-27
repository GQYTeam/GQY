package eval

import (
	"encoding/json"
	"fmt"
	"strings"

	"gqy/internal/tool"
)

// PromptFor 渲染系统提示词。
func PromptFor(variant string, t tool.Tool) string {
	head := "你可以使用工具。工具名：" + t.Name() + "；说明：" + t.Description()
	switch variant {
	case "none":
		return ""
	case "basic":
		return head + "\n需要时调用，不需要时直接回答。"
	case "when":
		return head + "\n什么时候调用：需要真实数据、文件、命令输出时。\n什么时候不调用：闲聊、常识、纯创作。"
	case "when-how":
		return head + "\n什么时候调用：需要真实数据、文件、命令输出时。\n什么时候不调用：闲聊、常识、纯创作。\n参数必须填完整；缺少信息时先调用工具确认，不要编造。"
	case "fewshot":
		return head + "\n正例：用户问“列出当前目录”，调用工具并传空参数。\n反例：用户说“你好”，不要调用工具，直接回答。"
	case "strict":
		return head + "\n规则：不确定就不要调用；需要真实信息必须先调用工具；调用后只根据工具结果回答。"
	default:
		return head
	}
}

// ToolSchemaFor 渲染工具声明。返回实际发给模型的工具名。
func ToolSchemaFor(t tool.Tool, variant string) (string, map[string]any) {
	name := t.Name()
	schema := clone(t.Schema())
	description := t.Description()
	switch variant {
	case "min":
		description = firstSentence(description)
		schema = minify(schema)
	case "full":
		// 原样
	case "example":
		schema["examples"] = []any{exampleFromSchema(schema)}
	case "negative":
		description += " 不要用它做与任务无关的事；参数不明确时先问用户。"
	case "alias":
		name = "use_tool"
		description = "调用 " + t.Name() + "：" + description
	}
	return name, map[string]any{
		"type": "function",
		"function": map[string]any{
			"name":        name,
			"description": description,
			"parameters":  schema,
		},
	}
}

// RenderReturn 渲染工具返回值。
func RenderReturn(raw string, err error, variant string) string {
	if err != nil {
		switch variant {
		case "error-min":
			return "失败"
		case "error-rich":
			return fmt.Sprintf("错误: %s（code=exec_failed, retryable=false）", truncate(err.Error(), 300))
		default:
			return "错误: " + truncate(err.Error(), 500)
		}
	}
	switch variant {
	case "trim":
		return trimJSON(raw)
	case "summary":
		return "工具返回：" + truncate(raw, 500)
	case "typed":
		return "[string] " + truncate(raw, 500)
	default:
		return raw
	}
}

func firstSentence(s string) string {
	if i := strings.IndexAny(s, "。.;；"); i >= 0 {
		return s[:i+1]
	}
	if len([]rune(s)) > 80 {
		return string([]rune(s)[:80])
	}
	return s
}

func clone(m map[string]any) map[string]any {
	b, _ := json.Marshal(m)
	var out map[string]any
	_ = json.Unmarshal(b, &out)
	if out == nil {
		out = map[string]any{"type": "object", "properties": map[string]any{}}
	}
	return out
}

func minify(schema map[string]any) map[string]any {
	out := map[string]any{}
	if t, ok := schema["type"]; ok {
		out["type"] = t
	}
	if req, ok := schema["required"]; ok {
		out["required"] = req
	}
	if props, ok := schema["properties"].(map[string]any); ok {
		next := map[string]any{}
		for k, v := range props {
			if pm, ok := v.(map[string]any); ok {
				next[k] = map[string]any{"type": pm["type"]}
			} else {
				next[k] = v
			}
		}
		out["properties"] = next
	}
	return out
}

func exampleFromSchema(schema map[string]any) map[string]any {
	out := map[string]any{}
	props, _ := schema["properties"].(map[string]any)
	for k, v := range props {
		pm, _ := v.(map[string]any)
		switch pm["type"] {
		case "string":
			out[k] = "示例"
		case "number", "integer":
			out[k] = 1
		case "boolean":
			out[k] = true
		case "array":
			out[k] = []any{}
		default:
			out[k] = map[string]any{}
		}
	}
	return out
}

func trimJSON(raw string) string {
	var v any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return truncate(raw, 500)
	}
	b, err := json.Marshal(v)
	if err != nil {
		return truncate(raw, 500)
	}
	return truncate(string(b), 500)
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
