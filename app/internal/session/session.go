// Package session 实现会话、消息事件流、草稿与附件。
//
// 设计依据：docs/designs/05-会话与产物.md（S1–S7）、
// docs/designs/09-存储.md（ST1–ST7）。
package session

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"time"

	"gqy/internal/storage"
)

// ID 格式：小写字母数字与连字符，防路径穿越。
var idRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

// Manifest 是会话清单（05 S5 与 09 ST3）。
type Manifest struct {
	SessionID     string        `json:"session_id"`
	Title         string        `json:"title,omitempty"`
	Workspace     string        `json:"workspace"`
	CreatedAt     string        `json:"created_at"`
	Artifacts     []Artifact    `json:"artifacts"`
	CleanupPolicy CleanupPolicy `json:"cleanup_policy"`
}

// Artifact 是一条产物索引（S3）。
type Artifact struct {
	ID        string `json:"id"`
	Path      string `json:"path"`
	Kind      string `json:"kind"` // final | intermediate
	CreatedAt string `json:"created_at"`
}

// CleanupPolicy 是会话清理策略（S6）。
type CleanupPolicy struct {
	SessionTmpTTLDays int `json:"session_tmp_ttl_days"`
	LogsTTLDays       int `json:"logs_ttl_days"`
}

// DefaultCleanupPolicy 返回默认清理策略（tmp 7 天，日志 30 天）。
func DefaultCleanupPolicy() CleanupPolicy {
	return CleanupPolicy{SessionTmpTTLDays: 7, LogsTTLDays: 30}
}

// ValidID 判断会话 id 是否合法（S7：防 ../ 穿越）。
func ValidID(id string) bool { return idRe.MatchString(id) }

func newID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}

// Store 是会话仓库，所有会话都在 $GQY_HOME/sessions 下。
type Store struct {
	home string
}

// NewStore 构造会话仓库。
func NewStore(home string) *Store { return &Store{home: home} }

func (s *Store) sessionsDir() string { return filepath.Join(s.home, "sessions") }

// Session 是一个打开的会话。
type Session struct {
	store    *Store
	dir      string
	Manifest Manifest
}

// Create 创建会话并落 manifest（原子写）。
func (s *Store) Create(workspace string) (*Session, error) {
	id := newID()
	sess := &Session{
		store: s,
		dir:   filepath.Join(s.sessionsDir(), id),
		Manifest: Manifest{
			SessionID:     id,
			Workspace:     workspace,
			CreatedAt:     time.Now().UTC().Format(time.RFC3339),
			Artifacts:     []Artifact{},
			CleanupPolicy: DefaultCleanupPolicy(),
		},
	}
	if err := sess.ensureDirs(); err != nil {
		return nil, err
	}
	if err := sess.SaveManifest(); err != nil {
		return nil, err
	}
	return sess, nil
}

// Open 打开既有会话并读 manifest。
func (s *Store) Open(id string) (*Session, error) {
	if !ValidID(id) {
		return nil, fmt.Errorf("session: 会话 id 非法: %q", id)
	}
	dir := filepath.Join(s.sessionsDir(), id)
	manifestPath := filepath.Join(dir, "manifest.json")
	data, err := os.ReadFile(manifestPath) // #nosec G304 -- 路径来自会话目录
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("session: 会话不存在: %s", id)
		}
		return nil, fmt.Errorf("session: 读 manifest: %w", err)
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("session: manifest 解析失败: %w", err)
	}
	if m.SessionID == "" {
		m.SessionID = id
	}
	if m.CleanupPolicy.SessionTmpTTLDays == 0 && m.CleanupPolicy.LogsTTLDays == 0 {
		m.CleanupPolicy = DefaultCleanupPolicy()
	}
	return &Session{store: s, dir: dir, Manifest: m}, nil
}

// List 返回会话目录下的全部 manifest，按创建时间倒序。
func (s *Store) List() ([]Manifest, error) {
	entries, err := os.ReadDir(s.sessionsDir())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []Manifest{}, nil
		}
		return nil, fmt.Errorf("session: 列目录: %w", err)
	}
	out := make([]Manifest, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() || !ValidID(entry.Name()) {
			continue
		}
		sess, err := s.Open(entry.Name())
		if err != nil {
			continue
		}
		out = append(out, sess.Manifest)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt > out[j].CreatedAt })
	return out, nil
}

// Delete 删除单个会话；删除前核对 id 与路径在 sessions 目录内（ST7）。
func (s *Store) Delete(id string) error {
	if !ValidID(id) {
		return fmt.Errorf("session: 会话 id 非法: %q", id)
	}
	dir := filepath.Join(s.sessionsDir(), id)
	if err := storage.EnsureInside(s.sessionsDir(), dir); err != nil {
		return err
	}
	if _, err := os.Stat(dir); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("session: 会话不存在: %s", id)
		}
		return err
	}
	return os.RemoveAll(dir)
}

// Dir 返回会话目录。
func (s *Session) Dir() string { return s.dir }

// ID 返回会话 id。
func (s *Session) ID() string { return s.Manifest.SessionID }

// EventsPath 返回消息事件流路径（真相源，05 S5）。
func (s *Session) EventsPath() string { return filepath.Join(s.dir, "io", "context", "messages.jsonl") }

// UploadsDir 返回附件目录。
func (s *Session) UploadsDir() string { return filepath.Join(s.dir, "tmp", "uploads") }

// GeneratedDir 返回中间产物目录。
func (s *Session) GeneratedDir() string { return filepath.Join(s.dir, "tmp", "generated") }

// DraftPath 返回草稿路径。
func (s *Session) DraftPath() string { return filepath.Join(s.dir, "draft.txt") }

// SaveManifest 原子写 manifest（ST4）。
func (s *Session) SaveManifest() error {
	if s.Manifest.SessionID == "" {
		return errors.New("session: manifest 缺少 session_id")
	}
	if s.Manifest.Artifacts == nil {
		s.Manifest.Artifacts = []Artifact{}
	}
	return storage.AtomicWriteJSON(filepath.Join(s.dir, "manifest.json"), s.Manifest, 0o600)
}

// AddArtifact 追加产物到 manifest；只记最终产物，中间产物跳过（S3）。
func (s *Session) AddArtifact(kind, path string) error {
	if kind == "intermediate" {
		return nil
	}
	if kind == "" {
		kind = "final"
	}
	s.Manifest.Artifacts = append(s.Manifest.Artifacts, Artifact{
		ID:        newID(),
		Path:      path,
		Kind:      kind,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	})
	return s.SaveManifest()
}

func (s *Session) ensureDirs() error {
	dirs := []string{
		s.dir,
		filepath.Join(s.dir, "tmp", "uploads"),
		filepath.Join(s.dir, "tmp", "generated"),
		filepath.Join(s.dir, "tmp", "scratch"),
		filepath.Join(s.dir, "logs", "tools"),
		filepath.Join(s.dir, "io", "input"),
		filepath.Join(s.dir, "io", "output"),
		filepath.Join(s.dir, "io", "context"),
		filepath.Join(s.dir, "io", "webui"),
		filepath.Join(s.dir, "scripts"),
	}
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("session: 创建目录 %s: %w", dir, err)
		}
	}
	return nil
}
