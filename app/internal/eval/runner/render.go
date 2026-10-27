package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// RenderReturn 按 return 维度把工具原始返回值（或错误）渲染成回填给模型的消息。
func RenderReturn(variant, raw string, err error) (string, error) {
	base := raw
	if err != nil && strings.TrimSpace(base) == "" {
		base = "错误: " + err.Error()
	}

	switch variant {
	case "raw":
		return base, nil
	case "trim":
		var v any
		if json.Unmarshal([]byte(raw), &v) != nil {
			return base, nil
		}
		return marshalCompact(trimValue(v)), nil
	case "summary":
		if err != nil {
			msg := "工具返回错误：" + err.Error()
			if strings.TrimSpace(raw) != "" {
				msg += "；原始输出：" + truncateRunes(raw, 300)
			}
			return msg, nil
		}
		return "工具返回：" + truncateRunes(base, 500), nil
	case "typed":
		var v any
		if json.Unmarshal([]byte(raw), &v) != nil {
			return base, nil
		}
		return marshalCompact(typedValue(v)), nil
	case "error-rich":
		if err == nil {
			return base, nil
		}
		code, hint, retryable := "TOOL_ERROR", "检查参数或工具状态，修正后重试", false
		if errors.Is(err, context.DeadlineExceeded) {
			code, hint, retryable = "TOOL_TIMEOUT", "工具执行超时，可缩小范围后重试", true
		}
		payload := map[string]any{
			"error":     true,
			"code":      code,
			"message":   err.Error(),
			"hint":      hint,
			"retryable": retryable,
		}
		return marshalCompact(payload), nil
	case "error-min":
		if err != nil {
			return "失败", nil
		}
		return base, nil
	default:
		return "", fmt.Errorf("未知 return 变体 %q", variant)
	}
}

func marshalCompact(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(b)
}

// trimValue 去掉空值，长字符串截断，长数组只留前 3 项。
func trimValue(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := map[string]any{}
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			tv := trimValue(x[k])
			if isEmptyValue(tv) {
				continue
			}
			out[k] = tv
		}
		return out
	case []any:
		max := len(x)
		if max > 3 {
			max = 3
		}
		out := make([]any, 0, max)
		for i := 0; i < max; i++ {
			out = append(out, trimValue(x[i]))
		}
		return out
	case string:
		return truncateRunes(x, 120)
	default:
		return v
	}
}

func isEmptyValue(v any) bool {
	switch x := v.(type) {
	case nil:
		return true
	case string:
		return strings.TrimSpace(x) == ""
	case []any:
		return len(x) == 0
	case map[string]any:
		return len(x) == 0
	}
	return false
}

// typedValue 给 JSON 值递归加类型标注。
func typedValue(v any) any {
	switch x := v.(type) {
	case map[string]any:
		inner := make(map[string]any, len(x))
		for k, val := range x {
			inner[k] = typedValue(val)
		}
		return map[string]any{"type": "object", "value": inner}
	case []any:
		inner := make([]any, len(x))
		for i, val := range x {
			inner[i] = typedValue(val)
		}
		return map[string]any{"type": "array", "value": inner}
	case string:
		return map[string]any{"type": "string", "value": x}
	case bool:
		return map[string]any{"type": "boolean", "value": x}
	case float64:
		return map[string]any{"type": "number", "value": x}
	case json.Number:
		return map[string]any{"type": "number", "value": x}
	case nil:
		return map[string]any{"type": "null", "value": nil}
	default:
		return map[string]any{"type": fmt.Sprintf("%T", x), "value": x}
	}
}
