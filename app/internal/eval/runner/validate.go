package runner

import (
	"fmt"
	"math"
	"reflect"
	"sort"
)

// ValidateArgs 做最小的 JSON Schema 校验：必填、类型、enum、additionalProperties。
// 只校验模型看得到的 schema，保证严格模式与渲染结果一致。
func ValidateArgs(schema map[string]any, args map[string]any) []string {
	var problems []string
	props, _ := schema["properties"].(map[string]any)
	required := stringList(schema["required"])
	for _, name := range required {
		if _, ok := args[name]; !ok {
			problems = append(problems, fmt.Sprintf("缺少必填参数 %q", name))
		}
	}
	for name, raw := range args {
		spec, ok := props[name].(map[string]any)
		if !ok {
			if extra, ok := schema["additionalProperties"].(bool); ok && !extra {
				problems = append(problems, fmt.Sprintf("不允许的参数 %q", name))
			}
			continue
		}
		if want, ok := spec["type"].(string); ok && want != "" {
			if actual, ok := jsonType(raw); ok && !typeMatches(want, raw, actual) {
				problems = append(problems, fmt.Sprintf("参数 %q 应为 %s，实际是 %s", name, want, actual))
			}
		}
		if enum, ok := spec["enum"].([]any); ok && len(enum) > 0 {
			found := false
			for _, allowed := range enum {
				if deepEqualJSON(allowed, raw) {
					found = true
					break
				}
			}
			if !found {
				problems = append(problems, fmt.Sprintf("参数 %q 的值不在枚举范围内", name))
			}
		}
	}
	sort.Strings(problems)
	return problems
}

func typeMatches(want string, v any, actual string) bool {
	switch want {
	case "string", "boolean", "array", "object":
		return actual == want
	case "number":
		return actual == "number"
	case "integer":
		if actual != "number" {
			return false
		}
		switch x := v.(type) {
		case float64:
			return x == math.Trunc(x)
		case int:
			return true
		case int64:
			return true
		}
		return false
	}
	return true
}

func jsonType(v any) (string, bool) {
	switch v.(type) {
	case nil:
		return "null", true
	case string:
		return "string", true
	case bool:
		return "boolean", true
	case float64, float32, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return "number", true
	case []any:
		return "array", true
	case map[string]any:
		return "object", true
	}
	return fmt.Sprintf("%T", v), false
}

func deepEqualJSON(a, b any) bool {
	if af, ok := toFloat(a); ok {
		if bf, ok := toFloat(b); ok {
			return af == bf
		}
		return false
	}
	return reflect.DeepEqual(a, b)
}

func toFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case float32:
		return float64(x), true
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	}
	return 0, false
}
