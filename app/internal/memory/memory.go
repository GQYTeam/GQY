// Package memory 实现长期记忆子系统：JSONL 追加存储、内存索引与召回打分。
//
// 设计依据：docs/designs/18-记忆与迁移.md（MEM1–MEM8）、
// docs/blueprint/memory.md。
//
// 记忆的真相源是 $GQY_HOME/memory/<persona>/ 下的四个 append-only JSONL 文件
// （facts / episodes / revisions / evicted）：写只追加、不原地改，进程内的索引
// 在首次访问时从文件重建（MEM1、MEM5）。读路径不写盘，召回不更新
// recall_count，保证同输入同输出。
package memory

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// 记录种类，与四个 JSONL 文件一一对应。
const (
	KindFact     = "fact"
	KindEpisode  = "episode"
	KindRevision = "revision"
	KindEvicted  = "evicted"
)

// memory_type 取值（MEM3）。
const (
	TypeFact         = "fact"
	TypePreference   = "preference"
	TypeSelf         = "self"
	TypeCorrection   = "correction"
	TypeTask         = "task"
	TypeRelationship = "relationship"
)

// truth_status 取值（MEM3）。
const (
	TruthAccepted = "accepted"
	TruthReported = "reported"
)

// status 取值（MEM5：归档不删行，追加新行覆盖语义）。
const (
	StatusActive     = "active"
	StatusArchived   = "archived"
	StatusSuperseded = "superseded"
)

// retention 取值（MEM3）。
const (
	RetentionShort = "short_term"
	RetentionLong  = "long_term"
)

// visibility 取值（MEM6：原值保留，接口输出掩码）。
const (
	VisibilityPrivileged = "privileged"
	VisibilityNormal     = "normal"
)

// 缺省值与上限。
const (
	// DefaultPersona 是缺省人格库（MEM2）。
	DefaultPersona = "default"
	// SchemaVersion 是 meta.json 与 /api/memory 的库格式版本。
	SchemaVersion = 1
	// DefaultImportance 是 importance 缺省值，取值 1–5。
	DefaultImportance = 3
	// MinImportance / MaxImportance 是 importance 的合法区间。
	MinImportance = 1
	MaxImportance = 5
	// MaxContentRunes 是视图层正文截断阈值（超过即截断并标 truncated）。
	MaxContentRunes = 4000
	// DefaultListLimit / MaxListLimit 是列表分页的默认值与上限。
	DefaultListLimit = 20
	MaxListLimit     = 200
	// DefaultRecallLimit / MaxRecallLimit 是召回条数的默认值与上限。
	DefaultRecallLimit = 5
	MaxRecallLimit     = 20
)

// 包级错误；调用方用 errors.Is 判断。
var (
	// ErrInvalid 表示参数或记录内容不合法。
	ErrInvalid = errors.New("memory: 参数不合法")
	// ErrInvalidPersona 表示人格 id 不符合命名规则。
	ErrInvalidPersona = errors.New("memory: 人格 id 非法")
	// ErrNotFound 表示记录不存在。
	ErrNotFound = errors.New("memory: 记录不存在")
)

// personaRe 是 persona 目录名的白名单（MEM2），不含路径分隔符，防穿越。
var personaRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)

// ValidPersona 判断人格 id 是否合法。
func ValidPersona(id string) bool { return personaRe.MatchString(id) }

// validatePersona 校验人格 id，空值补缺省。
func validatePersona(id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return DefaultPersona, nil
	}
	if !ValidPersona(id) {
		return "", fmt.Errorf("%w: %q", ErrInvalidPersona, id)
	}
	return id, nil
}

// ValidMemoryType 判断 memory_type 是否合法。
func ValidMemoryType(v string) bool {
	switch v {
	case TypeFact, TypePreference, TypeSelf, TypeCorrection, TypeTask, TypeRelationship:
		return true
	}
	return false
}

// ValidTruthStatus 判断 truth_status 是否合法。
func ValidTruthStatus(v string) bool { return v == TruthAccepted || v == TruthReported }

// ValidStatus 判断 status 是否合法。
func ValidStatus(v string) bool {
	return v == StatusActive || v == StatusArchived || v == StatusSuperseded
}

// ValidRetention 判断 retention 是否合法。
func ValidRetention(v string) bool { return v == RetentionShort || v == RetentionLong }

// ValidVisibility 判断 visibility 是否合法。
func ValidVisibility(v string) bool { return v == VisibilityPrivileged || v == VisibilityNormal }

// ValidKind 判断记录种类是否合法。
func ValidKind(v string) bool {
	return v == KindFact || v == KindEpisode || v == KindRevision || v == KindEvicted
}

// RecallableKind 判断记录种类是否参与召回（revisions 只做审计，不参与）。
func RecallableKind(v string) bool {
	return v == KindFact || v == KindEpisode || v == KindEvicted
}

// StringList 是 []string 的兼容类型：tags 是字符串数组，旧库偶尔把它存成
// JSON 文本，这里同时接受 JSON 数组与 JSON 文本数组两种写法（memory.md §2.1）。
type StringList []string

// UnmarshalJSON 兼容 `["a","b"]`、`"[\"a\",\"b\"]"` 与逗号分隔文本。
func (s *StringList) UnmarshalJSON(data []byte) error {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" || trimmed == "null" {
		*s = nil
		return nil
	}
	if strings.HasPrefix(trimmed, `"`) {
		var text string
		if err := json.Unmarshal(data, &text); err != nil {
			return err
		}
		text = strings.TrimSpace(text)
		if text == "" || text == "null" {
			*s = nil
			return nil
		}
		var out []string
		if err := json.Unmarshal([]byte(text), &out); err != nil {
			out = splitLooseList(text)
		}
		*s = StringList(out)
		return nil
	}
	var out []string
	if err := json.Unmarshal(data, &out); err != nil {
		return err
	}
	*s = StringList(out)
	return nil
}

// splitLooseList 把非 JSON 的旧值按逗号切分并去空白。
func splitLooseList(text string) []string {
	parts := strings.Split(text, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if v := strings.TrimSpace(part); v != "" {
			out = append(out, v)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// SubjectList 是 subjects 的兼容类型：旧库是**对象数组** `[{"name":…}]`，
// 迁移后保持对象数组且未知字段原样保留；这里也接受字符串数组并补成对象。
type SubjectList []map[string]any

// UnmarshalJSON 兼容对象数组、字符串数组与 JSON 文本写法。
func (s *SubjectList) UnmarshalJSON(data []byte) error {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" || trimmed == "null" {
		*s = nil
		return nil
	}
	if strings.HasPrefix(trimmed, `"`) {
		var text string
		if err := json.Unmarshal(data, &text); err != nil {
			return err
		}
		text = strings.TrimSpace(text)
		if text == "" || text == "null" {
			*s = nil
			return nil
		}
		if !strings.HasPrefix(text, "[") {
			// 单个名字的旧写法。
			*s = SubjectList{{"name": text}}
			return nil
		}
		data = []byte(text)
	}
	var raw []json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	out := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		item = bytes.TrimSpace(item)
		if len(item) == 0 || string(item) == "null" {
			continue
		}
		if item[0] == '{' {
			var obj map[string]any
			if err := json.Unmarshal(item, &obj); err != nil {
				continue
			}
			out = append(out, obj)
			continue
		}
		var name string
		if err := json.Unmarshal(item, &name); err != nil {
			continue
		}
		if name = strings.TrimSpace(name); name != "" {
			out = append(out, map[string]any{"name": name})
		}
	}
	*s = out
	return nil
}

// Int64List 是 source_episode_ids（整数数组）的兼容类型；
// 同时接受数字、数字字符串与 JSON 文本写法。
type Int64List []int64

// UnmarshalJSON 兼容整数数组、数字字符串数组与 JSON 文本写法。
func (s *Int64List) UnmarshalJSON(data []byte) error {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" || trimmed == "null" {
		*s = nil
		return nil
	}
	if strings.HasPrefix(trimmed, `"`) {
		var text string
		if err := json.Unmarshal(data, &text); err != nil {
			return err
		}
		text = strings.TrimSpace(text)
		if text == "" || text == "null" {
			*s = nil
			return nil
		}
		data = []byte(text)
	}
	var raw []json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	out := make([]int64, 0, len(raw))
	for _, item := range raw {
		item = bytes.TrimSpace(item)
		if len(item) == 0 || string(item) == "null" {
			continue
		}
		if item[0] == '"' {
			var text string
			if err := json.Unmarshal(item, &text); err != nil {
				continue
			}
			if v, err := strconv.ParseInt(strings.TrimSpace(text), 10, 64); err == nil {
				out = append(out, v)
			}
			continue
		}
		var v int64
		if err := json.Unmarshal(item, &v); err != nil {
			continue
		}
		out = append(out, v)
	}
	*s = out
	return nil
}

// Embedding 是记录自带的向量。旧库来自 bge-small-zh-v1.5-int8，新库不新算
// （MEM4：只在记录与查询都带向量时做余弦）。
type Embedding struct {
	Model    string `json:"model,omitempty"`
	Dim      int    `json:"dim,omitempty"`
	Encoding string `json:"encoding,omitempty"` // int8 | float32
	Data     string `json:"data,omitempty"`     // base64
}

// Record 是一条记忆记录；四类文件共用该结构，专属字段按文件填写
// （字段表见 docs/blueprint/memory.md §2）。
type Record struct {
	ID       int64  `json:"id"`
	LegacyID string `json:"legacy_id,omitempty"`
	Kind     string `json:"kind"`

	Content string `json:"content,omitempty"`

	MemoryType  string `json:"memory_type,omitempty"`
	TruthStatus string `json:"truth_status,omitempty"`
	Status      string `json:"status,omitempty"`
	Retention   string `json:"retention,omitempty"`

	Importance int     `json:"importance,omitempty"`
	Confidence float64 `json:"confidence,omitempty"`
	Strength   float64 `json:"strength,omitempty"`

	RecallCount    int    `json:"recall_count,omitempty"`
	LastRecalledAt string `json:"last_recalled_at,omitempty"`
	LastDecayAt    string `json:"last_decay_at,omitempty"`

	CreatedAt string `json:"created_at,omitempty"`
	UpdatedAt string `json:"updated_at,omitempty"`

	Visibility       string `json:"visibility,omitempty"`
	OwnerPrincipal   string `json:"owner_principal,omitempty"`
	OwnerDisplayName string `json:"owner_display_name,omitempty"`

	Subjects         SubjectList `json:"subjects,omitempty"`
	Tags             StringList  `json:"tags,omitempty"`
	SourceEpisodeIDs Int64List   `json:"source_episode_ids,omitempty"`

	OriginSessionID string `json:"origin_session_id,omitempty"`
	Source          string `json:"source,omitempty"`

	Embedding *Embedding `json:"embedding,omitempty"`

	// episodes 专属（MEM3）。
	UserMessage             string `json:"user_message,omitempty"`
	AssistantMessage        string `json:"assistant_message,omitempty"`
	ExpiresAt               string `json:"expires_at,omitempty"`
	ConsolidatedAt          string `json:"consolidated_at,omitempty"`
	PromotedAt              string `json:"promoted_at,omitempty"`
	PromotionPending        bool   `json:"promotion_pending,omitempty"`
	SourceKey               string `json:"source_key,omitempty"`
	OriginKind              string `json:"origin_kind,omitempty"`
	OriginPlatform          string `json:"origin_platform,omitempty"`
	OriginAccountID         string `json:"origin_account_id,omitempty"`
	OriginConversationKind  string `json:"origin_conversation_kind,omitempty"`
	OriginConversationID    string `json:"origin_conversation_id,omitempty"`
	OriginSenderID          string `json:"origin_sender_id,omitempty"`
	OriginSenderDisplayName string `json:"origin_sender_display_name,omitempty"`
	OriginMessageID         string `json:"origin_message_id,omitempty"`

	// revisions 专属（MEM3）。
	MemoryID   int64  `json:"memory_id,omitempty"`
	OldContent string `json:"old_content,omitempty"`
	NewContent string `json:"new_content,omitempty"`

	// evicted 专属（MEM3）。
	SourceID  string `json:"source_id,omitempty"`
	Timestamp string `json:"timestamp,omitempty"`
	Role      string `json:"role,omitempty"`
}

// Counts 是单个人格库的记录条数（按 id 去重，最后一行生效）。
type Counts struct {
	Facts     int `json:"facts"`
	Episodes  int `json:"episodes"`
	Revisions int `json:"revisions"`
	Evicted   int `json:"evicted"`
}

// PersonaInfo 是 GET /api/memory 里的一条人格库摘要。
type PersonaInfo struct {
	ID           string   `json:"id"`
	Source       string   `json:"source,omitempty"`
	Counts       Counts   `json:"counts"`
	UpdatedAt    string   `json:"updated_at,omitempty"`
	LoadWarnings []string `json:"load_warnings,omitempty"`
}

// Stats 是单库统计（GET /api/memory/{persona}/stats）。
type Stats struct {
	Persona      string         `json:"persona"`
	Counts       Counts         `json:"counts"`
	ByType       map[string]int `json:"by_type"`
	ByTruth      map[string]int `json:"by_truth"`
	LastUpdated  string         `json:"last_updated,omitempty"`
	LoadWarnings []string       `json:"load_warnings,omitempty"`
}

// Filter 是列表查询条件（memory.md §3 的查询参数）。
// 缺省只返回 status=active 且 visibility=privileged 的记录（MEM4/MEM6）。
type Filter struct {
	Query           string
	Limit           int
	Offset          int
	MemoryType      string
	TruthStatus     string
	Status          string
	Retention       string
	IncludeArchived bool
	// IncludeNormal 为真时也返回 visibility=normal 的记录；接口默认不开。
	IncludeNormal bool
}

// normalize 补齐默认值与上限。
func (f Filter) normalize() Filter {
	if f.Limit <= 0 {
		f.Limit = DefaultListLimit
	}
	if f.Limit > MaxListLimit {
		f.Limit = MaxListLimit
	}
	if f.Offset < 0 {
		f.Offset = 0
	}
	return f
}

// RememberInput 是写入一条 fact 的入参；零值字段按默认值补齐。
type RememberInput struct {
	Content          string
	MemoryType       string
	TruthStatus      string
	Importance       int
	Confidence       float64
	Tags             []string
	Subjects         SubjectList
	SourceEpisodeIDs []int64
	Visibility       string
	OwnerPrincipal   string
	OwnerDisplayName string
	OriginSessionID  string
	Source           string
	// LegacyID 是迁移来源键；重复写入幂等（MG3）。
	LegacyID string
	// CreatedAt 为空时用当前时间；测试与迁移可固定记录时间。
	CreatedAt string
}

// normalize 校验并补齐默认值。
func (in RememberInput) normalize() (RememberInput, error) {
	in.Content = strings.TrimSpace(in.Content)
	if in.Content == "" {
		return in, fmt.Errorf("%w: content 不能为空", ErrInvalid)
	}
	if runes := []rune(in.Content); len(runes) > MaxContentRunes {
		return in, fmt.Errorf("%w: content 超过 %d 字符", ErrInvalid, MaxContentRunes)
	}
	if in.MemoryType == "" {
		in.MemoryType = TypeFact
	}
	if !ValidMemoryType(in.MemoryType) {
		return in, fmt.Errorf("%w: memory_type %q", ErrInvalid, in.MemoryType)
	}
	if in.TruthStatus == "" {
		in.TruthStatus = TruthAccepted
	}
	if !ValidTruthStatus(in.TruthStatus) {
		return in, fmt.Errorf("%w: truth_status %q", ErrInvalid, in.TruthStatus)
	}
	if in.Importance == 0 {
		in.Importance = DefaultImportance
	}
	if in.Importance < MinImportance || in.Importance > MaxImportance {
		return in, fmt.Errorf("%w: importance 取值 %d–%d", ErrInvalid, MinImportance, MaxImportance)
	}
	if in.Confidence == 0 {
		in.Confidence = 1
	}
	if in.Confidence < 0 || in.Confidence > 1 {
		return in, fmt.Errorf("%w: confidence 取值 0–1", ErrInvalid)
	}
	if in.Visibility == "" {
		in.Visibility = VisibilityPrivileged
	}
	if !ValidVisibility(in.Visibility) {
		return in, fmt.Errorf("%w: visibility %q", ErrInvalid, in.Visibility)
	}
	if in.Source == "" {
		in.Source = "api"
	}
	in.LegacyID = strings.TrimSpace(in.LegacyID)
	in.CreatedAt = strings.TrimSpace(in.CreatedAt)
	return in, nil
}

// Scored 是一条带分数的召回结果（MEM4）。
type Scored struct {
	Record Record
	Score  float64
}

// RecallRequest 是召回入参（MEM4）。
type RecallRequest struct {
	Query           string
	Limit           int
	Kinds           []string
	IncludeArchived bool
	// IncludeNormal 为真时也召回 visibility=normal 的记录。
	IncludeNormal bool
	// QueryEmbedding 可选；与记录自带向量一起算余弦。
	QueryEmbedding *Embedding
	// Now 为零值时用 time.Now()；测试可固定它。
	Now time.Time
}

// normalize 补齐默认值并把 kinds 收敛成可召回集合。
func (r RecallRequest) normalize() RecallRequest {
	if r.Limit <= 0 {
		r.Limit = DefaultRecallLimit
	}
	if r.Limit > MaxRecallLimit {
		r.Limit = MaxRecallLimit
	}
	if len(r.Kinds) == 0 {
		r.Kinds = []string{KindFact, KindEpisode, KindEvicted}
	}
	return r
}

// wantKind 判断某类记录是否参与本次召回。
func (r RecallRequest) wantKind(kind string) bool {
	for _, k := range r.Kinds {
		if k == kind {
			return true
		}
	}
	return false
}

// include 判断一条记录是否通过过滤条件。
func (r RecallRequest) include(rec Record) bool {
	if rec.Visibility == VisibilityNormal && !r.IncludeNormal {
		return false
	}
	if !r.IncludeArchived && rec.Status != StatusActive {
		return false
	}
	return true
}
