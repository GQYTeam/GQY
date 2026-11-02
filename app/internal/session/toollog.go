package session

// 工具日志与脱敏（S5 / G6）：logs/tools/YYYY-MM-DD.jsonl，一行一次调用，敏感参数脱敏。

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// ToolLogRecord 是一条工具日志（字段与 blueprint/store.md 一致）。
type ToolLogRecord struct {
	Timestamp     string   `json:"timestamp"`
	ToolName      string   `json:"tool_name"`
	Status        string   `json:"status"`
	DurationMS    int64    `json:"duration_ms"`
	InputSummary  string   `json:"input_summary"`
	OutputSummary string   `json:"output_summary"`
	ArtifactPaths []string `json:"artifact_paths"`
}

var (
	reSkKey    = regexp.MustCompile(`sk-[A-Za-z0-9_-]{6,}`)
	reBearer   = regexp.MustCompile(`(?i)(bearer\s+)[A-Za-z0-9._~+/=-]{8,}`)
	reAPIKey   = regexp.MustCompile(`(?i)(api[_-]?key\s*[=:]\s*)[^\s"',}]{4,}`)
	reToken    = regexp.MustCompile(`(?i)((?:access[_-]?)?token\s*[=:]\s*)[^\s"',}]{8,}`)
	rePassword = regexp.MustCompile(`(?i)(password\s*[=:]\s*)[^\s"',}]{4,}`)
)

// RedactText 把常见密钥形态替换成掩码；用于工具日志与摘要（G6）。
func RedactText(s string) string {
	if s == "" {
		return s
	}
	s = reSkKey.ReplaceAllString(s, "sk-***")
	s = reBearer.ReplaceAllString(s, "${1}***")
	s = reAPIKey.ReplaceAllString(s, "${1}***")
	s = reToken.ReplaceAllString(s, "${1}***")
	s = rePassword.ReplaceAllString(s, "${1}***")
	return s
}

// truncateSummary 截断摘要，避免日志过大。
func truncateSummary(s string) string {
	const max = 512
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}

// ToolLogPath 返回某天的工具日志路径（日期按本地时区）。
func (s *Session) ToolLogPath(now time.Time) string {
	return filepath.Join(s.dir, "logs", "tools", now.Format("2006-01-02")+".jsonl")
}

// AppendToolLog 追加一条工具日志；输入/输出摘要自动脱敏与截断（S5）。
func (s *Session) AppendToolLog(rec ToolLogRecord) error {
	now := time.Now()
	if rec.Timestamp == "" {
		rec.Timestamp = now.Format(time.RFC3339)
	}
	rec.InputSummary = truncateSummary(RedactText(rec.InputSummary))
	rec.OutputSummary = truncateSummary(RedactText(rec.OutputSummary))
	if rec.ArtifactPaths == nil {
		rec.ArtifactPaths = []string{}
	}
	data, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	path := s.ToolLogPath(now)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	if _, err := f.Write(append(data, '\n')); err != nil {
		return err
	}
	return nil
}

// SummaryJSON 把工具参数安全地转成一行摘要（脱敏后）。
func SummaryJSON(v any) string {
	if v == nil {
		return ""
	}
	data, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(RedactText(string(data)))
}
