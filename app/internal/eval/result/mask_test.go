package result

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMaskString(t *testing.T) {
	tests := map[string]string{
		"key sk-abcdef1234567890":                   "sk-***",
		"Authorization: Bearer abc.def-123":         "Bearer ***",
		`{"api_key":"sk-secret-123","x":1}`:         `"api_key":"***"`,
		`api_key=plainvalue&x=1`:                    "api_key=***",
		`{"access_token": "tok_abcdef"}`:            `"access_token": "***"`,
		"prefix ghp_abcdefghijklmnopqrstuvwxyz1234": "gh-***",
	}
	for in, wantPart := range tests {
		got := MaskString(in)
		if !strings.Contains(got, wantPart) {
			t.Errorf("MaskString(%q)=%q，缺少 %q", in, got, wantPart)
		}
		for _, secret := range []string{"sk-secret-123", "plainvalue", "tok_abcdef", "abcdefghijklmnopqrstuvwxyz1234"} {
			if strings.Contains(got, secret) {
				t.Errorf("MaskString(%q)=%q 仍含密钥 %q", in, got, secret)
			}
		}
	}
}

func TestMaskJSON(t *testing.T) {
	in := []byte(`{
		"api_key": "sk-abcdef1234567890",
		"token": "tok",
		"tokens": {"in": 1, "out": 2},
		"messages": [{"role": "user", "content": "Bearer abcdef123"}],
		"arguments": "{\"secret\":\"value\",\"ok\":1}"
	}`)
	out, err := MaskJSON(in, false)
	if err != nil {
		t.Fatal(err)
	}
	var v map[string]any
	if err := json.Unmarshal(out, &v); err != nil {
		t.Fatal(err)
	}
	if v["api_key"] != "***" || v["token"] != "***" {
		t.Fatalf("字段未脱敏: %v", v)
	}
	tokens, ok := v["tokens"].(map[string]any)
	if !ok || tokens["in"].(float64) != 1 {
		t.Fatalf("tokens 字段不应被误伤: %v", v["tokens"])
	}
	content := v["messages"].([]any)[0].(map[string]any)["content"].(string)
	if strings.Contains(content, "abcdef") || !strings.Contains(content, "***") {
		t.Fatalf("Bearer 未脱敏: %q", content)
	}
	args := v["arguments"].(string)
	if strings.Contains(args, `"value"`) || !strings.Contains(args, `"***"`) {
		t.Fatalf("嵌套 JSON 字符串未脱敏: %q", args)
	}
}
