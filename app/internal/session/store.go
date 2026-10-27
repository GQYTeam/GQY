// 包 session：会话落盘与统计。按 docs/AboutProjectFile.md 的目录规范写文件。
package session

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	maxSessionMsgs = 500
	maxArtifact    = 2 << 20
)

var (
	IDRe       = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	unsafeName = regexp.MustCompile(`[^A-Za-z0-9_.-]+`)
	secretKey  = regexp.MustCompile(`(?i)(key|token|secret|authorization|password)`)
)

type Payload struct {
	ID        string            `json:"id"`
	Title     string            `json:"title"`
	Workspace string            `json:"workspace"`
	UpdatedAt int64             `json:"updatedAt"`
	Messages  []json.RawMessage `json:"messages"`
}

type Message struct {
	Role     string    `json:"role"`
	TS       int64     `json:"ts"`
	Text     string    `json:"text"`
	Thinking string    `json:"thinking"`
	Tools    []Tool    `json:"tools"`
	Artifact *Artifact `json:"artifact"`
	Usage    *Usage    `json:"usage"`
}

type Tool struct {
	Name   string     `json:"name"`
	Status string     `json:"status"`
	MS     int64      `json:"ms"`
	Args   [][]string `json:"args"`
	Output string     `json:"output"`
}

type Artifact struct {
	Title   string         `json:"title"`
	Content string         `json:"content"`
	Files   []ArtifactFile `json:"files,omitempty"`
}

type ArtifactFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

type Usage struct {
	PromptTokens     int  `json:"prompt_tokens"`
	CompletionTokens int  `json:"completion_tokens"`
	TotalTokens      int  `json:"total_tokens"`
	Mock             bool `json:"mock,omitempty"`
}

// Store 以 $GQY_HOME/sessions 为根写会话。
type Store struct {
	Home string
}

func New(home string) *Store {
	return &Store{Home: home}
}

func (s *Store) Dir() string {
	return filepath.Join(s.Home, "sessions")
}

func (s *Store) sessionDir(id string) string {
	return filepath.Join(s.Dir(), id)
}

func (s *Store) Put(id string, payload Payload) (string, error) {
	dir := s.sessionDir(id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return dir, err
	}

	messages := payload.Messages
	if len(messages) > maxSessionMsgs {
		messages = messages[len(messages)-maxSessionMsgs:]
	}

	createdAt := time.Now().UTC().Format(time.RFC3339)
	if raw, err := os.ReadFile(filepath.Join(dir, "manifest.json")); err == nil {
		var old struct {
			CreatedAt string `json:"created_at"`
		}
		if json.Unmarshal(raw, &old) == nil && old.CreatedAt != "" {
			createdAt = old.CreatedAt
		}
	}

	decoded := make([]Message, 0, len(messages))
	artifactTitles := make([]string, 0, 8)
	for _, raw := range messages {
		var m Message
		if json.Unmarshal(raw, &m) == nil {
			decoded = append(decoded, m)
			if m.Artifact != nil && m.Artifact.Title != "" {
				artifactTitles = append(artifactTitles, m.Artifact.Title)
			}
		}
	}

	manifest := map[string]any{
		"session_id": id,
		"workspace":  payload.Workspace,
		"title":      payload.Title,
		"created_at": createdAt,
		"updated_at": time.Now().UTC().Format(time.RFC3339),
		"messages":   len(messages),
		"artifacts":  artifactTitles,
		"cleanup_policy": map[string]int{
			"session_tmp_ttl_days": 7,
			"logs_ttl_days":        30,
		},
	}
	manifestJSON, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return dir, err
	}
	if err := writeFileAtomic(filepath.Join(dir, "manifest.json"), append(manifestJSON, '\n')); err != nil {
		return dir, err
	}

	var context strings.Builder
	fmt.Fprintf(&context, "# %s\n\n- session: `%s`\n- 更新时间: %s\n\n", fallback(payload.Title, "会话"), id, manifest["updated_at"])
	for i, m := range decoded {
		role := "顾清影"
		if m.Role == "user" {
			role = "用户"
		}
		fmt.Fprintf(&context, "## %d. %s\n\n%s\n\n", i+1, role, truncateRunes(m.Text, 8000))
		if m.Artifact != nil {
			fmt.Fprintf(&context, "产物: `%s`\n\n", m.Artifact.Title)
		}
	}
	if err := writeFileAtomic(filepath.Join(dir, "io", "context", "context.md"), []byte(context.String())); err != nil {
		return dir, err
	}

	var jsonl strings.Builder
	for _, raw := range messages {
		if len(bytes.TrimSpace(raw)) == 0 {
			continue
		}
		jsonl.Write(bytes.TrimSpace(raw))
		jsonl.WriteByte('\n')
	}
	if err := writeFileAtomic(filepath.Join(dir, "io", "context", "messages.jsonl"), []byte(jsonl.String())); err != nil {
		return dir, err
	}

	// 产物
	used := map[string]bool{}
	artifactNames := make([]string, 0, 8)
	for _, m := range decoded {
		if m.Artifact == nil {
			continue
		}
		title := safeName(m.Artifact.Title, fmt.Sprintf("artifact-%d", len(artifactNames)))
		if m.Artifact.Files != nil {
			for _, f := range m.Artifact.Files {
				if len(f.Content) > maxArtifact {
					continue
				}
				rel, err := safeArtifactPath(f.Path)
				if err != nil {
					continue
				}
				target := filepath.Join(dir, "io", "output", "artifacts", title, rel)
				if err := writeFileAtomic(target, []byte(f.Content)); err != nil {
					return dir, err
				}
				artifactNames = append(artifactNames, filepath.ToSlash(filepath.Join(title, rel)))
			}
			continue
		}
		if m.Artifact.Content == "" || len(m.Artifact.Content) > maxArtifact {
			continue
		}
		name := title
		if used[name] {
			name = fmt.Sprintf("%d-%s", len(artifactNames), name)
		}
		used[name] = true
		if err := writeFileAtomic(filepath.Join(dir, "io", "output", "artifacts", name), []byte(m.Artifact.Content)); err != nil {
			return dir, err
		}
		artifactNames = append(artifactNames, name)
	}
	indexJSON, _ := json.MarshalIndent(map[string]any{"artifacts": artifactNames}, "", "  ")
	if err := writeFileAtomic(filepath.Join(dir, "io", "output", "index.json"), append(indexJSON, '\n')); err != nil {
		return dir, err
	}

	// 工具日志：按消息日期分文件，每次同步重写（幂等）
	byDay := map[string][]string{}
	for _, m := range decoded {
		day := time.UnixMilli(m.TS).UTC().Format("2006-01-02")
		if m.TS == 0 {
			day = time.Now().UTC().Format("2006-01-02")
		}
		for _, tool := range m.Tools {
			status := "error"
			if tool.Status == "done" {
				status = "done"
			}
			inputs := make([]string, 0, len(tool.Args))
			for _, pair := range tool.Args {
				if len(pair) < 2 || secretKey.MatchString(pair[0]) {
					continue
				}
				inputs = append(inputs, pair[0]+"="+truncateRunes(pair[1], 120))
			}
			line, err := json.Marshal(map[string]any{
				"timestamp":      time.UnixMilli(m.TS).UTC().Format(time.RFC3339),
				"tool_name":      fallback(tool.Name, "tool"),
				"status":         status,
				"duration_ms":    tool.MS,
				"input_summary":  strings.Join(inputs, " "),
				"output_summary": truncateRunes(tool.Output, 300),
			})
			if err == nil {
				byDay[day] = append(byDay[day], string(line))
			}
		}
	}
	days := make([]string, 0, len(byDay))
	for day := range byDay {
		days = append(days, day)
	}
	sort.Strings(days)
	for _, day := range days {
		body := strings.Join(byDay[day], "\n") + "\n"
		if err := writeFileAtomic(filepath.Join(dir, "logs", "tools", day+".jsonl"), []byte(body)); err != nil {
			return dir, err
		}
	}

	return dir, nil
}

func (s *Store) Get(id string) ([]byte, error) {
	return os.ReadFile(filepath.Join(s.sessionDir(id), "io", "context", "messages.jsonl"))
}

func (s *Store) Delete(id string) error {
	return os.RemoveAll(s.sessionDir(id))
}

func (s *Store) List() ([]Manifest, error) {
	entries, err := os.ReadDir(s.Dir())
	if err != nil {
		return []Manifest{}, nil
	}
	list := make([]Manifest, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() || !IDRe.MatchString(entry.Name()) {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(s.Dir(), entry.Name(), "manifest.json"))
		if err != nil {
			continue
		}
		var m Manifest
		if json.Unmarshal(raw, &m) == nil {
			list = append(list, m)
		}
	}
	sort.Slice(list, func(i, j int) bool { return list[i].UpdatedAt > list[j].UpdatedAt })
	if len(list) > 200 {
		list = list[:200]
	}
	return list, nil
}

type Manifest struct {
	SessionID string `json:"session_id"`
	Title     string `json:"title"`
	Workspace string `json:"workspace"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
	Messages  int    `json:"messages"`
}

func writeFileAtomic(file string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		return err
	}
	tmp := fmt.Sprintf("%s.%d.%d.tmp", file, os.Getpid(), rand.Int63())
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, file); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func safeArtifactPath(raw string) (string, error) {
	clean := filepath.Clean("/" + strings.TrimPrefix(filepath.ToSlash(raw), "/"))
	rel := strings.TrimPrefix(clean, "/")
	if rel == "" || rel == "." || strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("非法产物路径")
	}
	parts := strings.Split(rel, "/")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = safeName(p, "file")
		if p == "" {
			continue
		}
		out = append(out, p)
	}
	if len(out) == 0 {
		return "", fmt.Errorf("非法产物路径")
	}
	return filepath.Join(out...), nil
}

func safeName(name, fallbackName string) string {
	clean := strings.TrimLeft(unsafeName.ReplaceAllString(name, "_"), "._")
	if clean == "" {
		clean = fallbackName
	}
	if runes := []rune(clean); len(runes) > 80 {
		clean = string(runes[:80])
	}
	return clean
}

func truncateRunes(s string, limit int) string {
	runes := []rune(s)
	if len(runes) <= limit {
		return s
	}
	return string(runes[:limit])
}

func fallback(value, def string) string {
	if strings.TrimSpace(value) == "" {
		return def
	}
	return value
}
