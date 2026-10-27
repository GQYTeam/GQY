// 会话统计：从落盘的会话目录聚合 token 用量与消息量。
package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

type Stats struct {
	Sessions         int   `json:"sessions"`
	Messages         int   `json:"messages"`
	Artifacts        int   `json:"artifacts"`
	PromptTokens     int64 `json:"promptTokens"`
	CompletionTokens int64 `json:"completionTokens"`
	TotalTokens      int64 `json:"totalTokens"`
	// Days 是最近 7 天的消息量，从今天往前（索引 6 为今天）。
	Days [7]int `json:"days"`
}

// Stats 聚合所有会话目录的 token 用量与最近 7 天消息量。
func (s *Store) Stats() Stats {
	var out Stats
	now := time.Now()
	base := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)

	entries, err := os.ReadDir(s.Dir())
	if err != nil {
		return out
	}
	for _, entry := range entries {
		if !entry.IsDir() || !IDRe.MatchString(entry.Name()) {
			continue
		}
		out.Sessions++
		raw, err := os.ReadFile(filepath.Join(s.Dir(), entry.Name(), "io", "context", "messages.jsonl"))
		if err != nil {
			continue
		}
		for _, line := range splitLines(raw) {
			var m Message
			if json.Unmarshal(line, &m) != nil {
				continue
			}
			out.Messages++
			if m.Artifact != nil {
				out.Artifacts++
			}
			if m.Usage != nil {
				out.PromptTokens += int64(m.Usage.PromptTokens)
				out.CompletionTokens += int64(m.Usage.CompletionTokens)
				out.TotalTokens += int64(m.Usage.TotalTokens)
			}
			diff := int(base.Sub(time.UnixMilli(m.TS)).Hours() / 24)
			if diff >= 0 && diff < 7 {
				out.Days[6-diff]++
			}
		}
	}
	return out
}

func splitLines(b []byte) [][]byte {
	var out [][]byte
	start := 0
	for i, c := range b {
		if c == '\n' {
			if i > start {
				out = append(out, b[start:i])
			}
			start = i + 1
		}
	}
	if start < len(b) {
		out = append(out, b[start:])
	}
	return out
}
