package session

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestRedactText(t *testing.T) {
	cases := map[string]string{
		"key=sk-abcdef123456":                    "key=sk-***",
		"Authorization: Bearer abcdefghijklmnop": "Authorization: Bearer ***",
		"curl -H 'api_key=supersecretvalue'":     "curl -H 'api_key=***'",
		"GQY_AUTH_TOKEN=abcdef1234567890":        "GQY_AUTH_TOKEN=***",
		"password=hunter2xx":                     "password=***",
		"普通输出 hello world":                       "普通输出 hello world",
	}
	for in, want := range cases {
		if got := RedactText(in); got != want {
			t.Fatalf("RedactText(%q) = %q, 期望 %q", in, got, want)
		}
	}
}

func TestAppendToolLogRedactsSecrets(t *testing.T) {
	sess, err := NewStore(t.TempDir()).Create("/tmp/ws")
	if err != nil {
		t.Fatal(err)
	}
	rec := ToolLogRecord{
		ToolName:      "shell",
		Status:        "ok",
		DurationMS:    12,
		InputSummary:  `{"command":"curl -H 'api_key=sk-verysecret123' https://x"}`,
		OutputSummary: "Bearer abcdefghijklmnop leaked",
	}
	if err := sess.AppendToolLog(rec); err != nil {
		t.Fatal(err)
	}
	path := sess.ToolLogPath(time.Now())
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("工具日志应写入 %s: %v", path, err)
	}
	text := string(data)
	if strings.Contains(text, "sk-verysecret123") || strings.Contains(text, "abcdefghijklmnop") {
		t.Fatalf("日志不应包含明文密钥: %s", text)
	}
	for _, want := range []string{`"tool_name":"shell"`, `"status":"ok"`, `"duration_ms":12`, `"artifact_paths":[]`} {
		if !strings.Contains(text, want) {
			t.Fatalf("日志缺少字段 %s: %s", want, text)
		}
	}
}

// TestAppendToolLogArtifacts 验证产物路径写入工具日志（022）。
func TestAppendToolLogArtifacts(t *testing.T) {
	sess, err := NewStore(t.TempDir()).Create("/tmp/ws")
	if err != nil {
		t.Fatal(err)
	}
	if err := sess.AppendToolLog(ToolLogRecord{
		ToolName:      "edit",
		Status:        "ok",
		InputSummary:  `{"path":"/tmp/ws/a.txt"}`,
		OutputSummary: "ok",
		ArtifactPaths: []string{"/tmp/ws/a.txt"},
	}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(sess.ToolLogPath(time.Now()))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"artifact_paths":["/tmp/ws/a.txt"]`) {
		t.Fatalf("工具日志应包含产物路径: %s", string(data))
	}
}
