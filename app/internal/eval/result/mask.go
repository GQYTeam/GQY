package result

import (
	"bytes"
	"encoding/json"
	"regexp"
	"strings"
)

// 密钥字段名（归一化后）一律替换成 "***"。
var secretKeys = map[string]bool{
	"api_key":       true,
	"apikey":        true,
	"authorization": true,
	"token":         true,
	"access_token":  true,
	"refresh_token": true,
	"secret":        true,
	"client_secret": true,
	"api_secret":    true,
	"password":      true,
	"passwd":        true,
	"private_key":   true,
	"cookie":        true,
	"set_cookie":    true,
}

var (
	// JSON 片段里的 "api_key": "sk-xxx"。
	jsonSecretRE = regexp.MustCompile(`(?i)("(?:api[_-]?key|access[_-]?token|refresh[_-]?token|client[_-]?secret|api[_-]?secret|secret|token|password|authorization)"\s*:\s*")[^"]*(")`)
	// KEY=value 形式。
	kvSecretRE = regexp.MustCompile(`(?i)((?:api[_-]?key|access[_-]?token|refresh[_-]?token|client[_-]?secret|api[_-]?secret|secret|token|password|authorization)=)[^\s,&;]+`)
	// Authorization: Bearer xxx。
	bearerRE = regexp.MustCompile(`(?i)(Bearer\s+)[A-Za-z0-9._~+/\-=]+`)
	// 常见密钥前缀。
	skRE = regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{8,}\b`)
	ghRE = regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{20,}\b`)
)

func normalizeKey(k string) string {
	k = strings.ToLower(strings.TrimSpace(k))
	k = strings.ReplaceAll(k, "-", "_")
	return k
}

// MaskString 对自由文本做脱敏，覆盖字段片段、Bearer 和常见密钥前缀。
func MaskString(s string) string {
	if s == "" {
		return s
	}
	s = jsonSecretRE.ReplaceAllString(s, `${1}***${2}`)
	s = kvSecretRE.ReplaceAllString(s, `${1}***`)
	s = bearerRE.ReplaceAllString(s, `${1}***`)
	s = skRE.ReplaceAllString(s, `sk-***`)
	s = ghRE.ReplaceAllString(s, `gh-***`)
	return s
}

// MaskValue 递归脱敏任意 JSON 值。
func MaskValue(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, val := range x {
			if secretKeys[normalizeKey(k)] {
				out[k] = "***"
				continue
			}
			out[k] = MaskValue(val)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, val := range x {
			out[i] = MaskValue(val)
		}
		return out
	case string:
		return MaskString(x)
	default:
		return v
	}
}

// MaskJSON 先解析再递归脱敏；indent 为 true 时输出两空格缩进。
func MaskJSON(data []byte, indent bool) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	v = MaskValue(v)
	if indent {
		return json.MarshalIndent(v, "", "  ")
	}
	return json.Marshal(v)
}
