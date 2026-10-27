// 顾清影 · 备份导入器（零第三方依赖）。
//
// 读取 miyu 老架构的备份目录（gqy_backup_latest），把会话、persona、skill、
// 知识库、用量、providers 注册到 $GQY_HOME（默认 ~/.gqy）。
//
// 会话数据在备份的 SQLite（conversation.db）里，这里用系统自带的 sqlite3 命令
// 以 JSON 模式导出，再用标准库解析，不引入任何 Go SQLite 驱动。
//
// 用法：
//
//	go run ./cmd/gqy-import -src /path/to/gqy_backup_latest [-dst ~/.gqy] [-dry-run]
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"gqy/internal/session"
)

func main() {
	src := flag.String("src", "", "备份目录（含 home/）")
	dst := flag.String("dst", defaultHome(), "$GQY_HOME 目标目录")
	dryRun := flag.Bool("dry-run", false, "只打印将导入的内容，不写入")
	flag.Parse()

	if *src == "" {
		log.Fatal("需要 -src 指向备份目录")
	}
	homeDir := filepath.Join(*src, "home")
	if _, err := os.Stat(homeDir); err != nil {
		log.Fatalf("备份目录里没有 home/：%v", err)
	}
	if _, err := exec.LookPath("sqlite3"); err != nil {
		log.Fatal("系统没有 sqlite3 命令（macOS 通常自带）")
	}

	imp := &importer{src: homeDir, dst: *dst, dryRun: *dryRun}
	if err := imp.run(); err != nil {
		log.Fatalf("导入失败: %v", err)
	}
	log.Println("导入完成")
}

func defaultHome() string {
	if h, err := os.UserHomeDir(); err == nil {
		return filepath.Join(h, ".gqy")
	}
	return ".gqy"
}

type importer struct {
	src    string
	dst    string
	dryRun bool
}

func (m *importer) run() error {
	if err := m.importSessions(); err != nil {
		return err
	}
	for _, d := range []string{"personas", "extensions/skills", "data/kb", "data/prompts"} {
		rel := strings.TrimPrefix(d, "extensions/")
		rel = strings.TrimPrefix(rel, "data/")
		if err := m.copyDir(d, rel); err != nil {
			return err
		}
	}
	if err := m.importUsage(); err != nil {
		return err
	}
	return m.importProviders()
}

// sqliteJSON 用系统 sqlite3 以 JSON 数组模式执行查询。
func sqliteJSON(db, query string) ([]map[string]any, error) {
	cmd := exec.Command("sqlite3", "-json", db, query)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("sqlite3: %v: %s", err, errb.String())
	}
	if out.Len() == 0 {
		return []map[string]any{}, nil
	}
	var rows []map[string]any
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		return nil, err
	}
	return rows, nil
}

func (m *importer) importSessions() error {
	dbPath := filepath.Join(m.src, "home", "mac", "conversation.db")
	if _, err := os.Stat(dbPath); err != nil {
		log.Printf("跳过会话导入：%v", err)
		return nil
	}
	sessions, err := sqliteJSON(dbPath,
		`SELECT session_id, persona, name, workspace, provider_id, model, prompt_tokens, completion_tokens, total_tokens, updated_at FROM sessions WHERE name != '' ORDER BY updated_at DESC`)
	if err != nil {
		return err
	}
	log.Printf("发现 %d 个会话", len(sessions))

	store := session.New(m.dst)
	imported := 0
	for _, s := range sessions {
		id := str(s["session_id"])
		messages, err := m.turnsFor(dbPath, id)
		if err != nil {
			log.Printf("  跳过 %s: %v", id, err)
			continue
		}
		if len(messages) == 0 {
			continue
		}
		if m.dryRun {
			imported++
			continue
		}
		payload := session.Payload{
			ID:        id,
			Title:     str(s["name"]),
			Workspace: str(s["workspace"]),
			Messages:  messages,
		}
		if _, err := store.Put(id, payload); err != nil {
			log.Printf("  写入 %s 失败: %v", id, err)
			continue
		}
		imported++
	}
	log.Printf("导入 %d 个会话", imported)
	return nil
}

func (m *importer) turnsFor(dbPath, sessionID string) ([]json.RawMessage, error) {
	rows, err := sqliteJSON(dbPath,
		`SELECT seq, user_content, assistant_content, assistant_reasoning, tool_reports, assistant_model, token_total, user_timestamp FROM turns WHERE session_id='`+sessionID+`' AND hidden=0 ORDER BY seq`)
	if err != nil {
		return nil, err
	}
	var out []json.RawMessage
	for _, r := range rows {
		ts := parseTS(str(r["user_timestamp"]))
		userContent := str(r["user_content"])
		assistantContent := str(r["assistant_content"])
		reasoning := str(r["assistant_reasoning"])
		toolReports := str(r["tool_reports"])
		tokenTotal := intNum(r["token_total"])

		if userContent != "" {
			b, _ := json.Marshal(session.Message{Role: "user", TS: ts, Text: userContent})
			out = append(out, b)
		}
		if assistantContent != "" {
			var tools []session.Tool
			if toolReports != "" && toolReports != "[]" {
				tools = parseTools(toolReports)
			}
			b, _ := json.Marshal(session.Message{
				Role:     "assistant",
				TS:       ts + 1,
				Text:     assistantContent,
				Thinking: reasoning,
				Tools:    tools,
				Usage:    &session.Usage{TotalTokens: tokenTotal},
			})
			out = append(out, b)
		}
	}
	return out, nil
}

func parseTS(s string) int64 {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UnixMilli()
	}
	return time.Now().UnixMilli()
}

func parseTools(raw string) []session.Tool {
	var reports []string
	_ = json.Unmarshal([]byte(raw), &reports)
	var out []session.Tool
	for _, r := range reports {
		name := extractToolName(r)
		if name == "" {
			continue
		}
		out = append(out, session.Tool{Name: name, Status: "done"})
	}
	return out
}

func extractToolName(s string) string {
	if i := strings.Index(s, `name="`); i >= 0 {
		rest := s[i+len(`name="`):]
		if j := strings.Index(rest, `"`); j >= 0 {
			return rest[:j]
		}
	}
	return ""
}

func str(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func intNum(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case int64:
		return int(n)
	}
	return 0
}

func (m *importer) copyDir(srcRel, dstRel string) error {
	src := filepath.Join(m.src, srcRel)
	if _, err := os.Stat(src); err != nil {
		log.Printf("跳过 %s：%v", srcRel, err)
		return nil
	}
	dst := filepath.Join(m.dst, dstRel)
	if m.dryRun {
		log.Printf("复制 %s → %s", srcRel, dstRel)
		return nil
	}
	if err := os.MkdirAll(dst, 0o700); err != nil {
		return err
	}
	return copyTree(src, dst)
}

func copyTree(src, dst string) error {
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.Name() == ".DS_Store" {
			continue
		}
		s := filepath.Join(src, e.Name())
		d := filepath.Join(dst, e.Name())
		if e.IsDir() {
			if err := os.MkdirAll(d, 0o700); err != nil {
				return err
			}
			if err := copyTree(s, d); err != nil {
				return err
			}
		} else {
			if err := copyFile(s, d); err != nil {
				return err
			}
		}
	}
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

func (m *importer) importUsage() error {
	src := filepath.Join(m.src, "state", "usage.json")
	if _, err := os.Stat(src); err != nil {
		log.Printf("跳过 usage：%v", err)
		return nil
	}
	if m.dryRun {
		log.Printf("复制 usage.json → %s", filepath.Join(m.dst, "usage.json"))
		return nil
	}
	return copyFile(src, filepath.Join(m.dst, "usage.json"))
}

// importProviders 从 config.jsonc 提取 providers 到 ~/.gqy/providers.json。
func (m *importer) importProviders() error {
	src := filepath.Join(m.src, "config", "config.jsonc")
	if _, err := os.Stat(src); err != nil {
		log.Printf("跳过 providers：%v", err)
		return nil
	}
	raw, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	providers := parseProviders(string(raw))
	if len(providers) == 0 {
		log.Printf("未解析到 providers")
		return nil
	}
	b, _ := json.MarshalIndent(map[string]any{"providers": providers}, "", "  ")
	dst := filepath.Join(m.dst, "providers.json")
	if m.dryRun {
		log.Printf("写入 %d 个 provider → %s", len(providers), dst)
		return nil
	}
	return os.WriteFile(dst, b, 0o600)
}

func parseProviders(raw string) []map[string]any {
	var out []map[string]any
	lines := strings.Split(raw, "\n")
	var cur map[string]any
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, `"id":`) {
			if cur != nil {
				out = append(out, cur)
			}
			cur = map[string]any{"id": strValue(trimmed)}
		} else if cur != nil {
			switch {
			case strings.HasPrefix(trimmed, `"display_name":`):
				cur["display_name"] = strValue(trimmed)
			case strings.HasPrefix(trimmed, `"base_url":`):
				cur["base_url"] = strValue(trimmed)
			case strings.HasPrefix(trimmed, `"protocol":`):
				cur["protocol"] = strValue(trimmed)
			case strings.HasPrefix(trimmed, `"default_model":`):
				cur["default_model"] = strValue(trimmed)
			}
		}
	}
	if cur != nil {
		out = append(out, cur)
	}
	var filtered []map[string]any
	for _, p := range out {
		id, _ := p["id"].(string)
		if id == "" || strings.HasPrefix(id, "account-") || isNumeric(id) {
			continue
		}
		filtered = append(filtered, p)
	}
	return filtered
}

func strValue(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimSuffix(s, ",")
	if i := strings.Index(s, ":"); i >= 0 {
		return strings.Trim(strings.TrimSpace(s[i+1:]), `"`)
	}
	return ""
}

func isNumeric(s string) bool {
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return len(s) > 0
}
