package memory

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"gqy/internal/storage"
)

// 库内固定文件名（MEM2）。
const (
	metaFileName      = "meta.json"
	personasFileName  = "personas.json"
	factsFileName     = "facts.jsonl"
	episodesFileName  = "episodes.jsonl"
	revisionsFileName = "revisions.jsonl"
	evictedFileName   = "evicted.jsonl"
)

// jsonlFiles 是重建索引需要读取的四个文件。
var jsonlFiles = []string{factsFileName, episodesFileName, revisionsFileName, evictedFileName}

// Meta 是库级元信息（meta.json，memory.md §2.5）。
type Meta struct {
	SchemaVersion       int            `json:"schema_version"`
	DatabaseID          string         `json:"database_id,omitempty"`
	Generation          int            `json:"generation,omitempty"`
	AccessSchemaVersion int            `json:"access_schema_version,omitempty"`
	UpdatedAt           string         `json:"updated_at,omitempty"`
	MigratedFrom        map[string]any `json:"migrated_from,omitempty"`
}

// PersonaEntry 是 personas.json 里的一条人格描述（含旧版子系统开关）。
type PersonaEntry struct {
	ID         string          `json:"id"`
	Source     string          `json:"source,omitempty"`
	Subsystems map[string]bool `json:"subsystems,omitempty"`
	Plugins    map[string]any  `json:"plugins,omitempty"`
}

// personasDoc 是 personas.json 的文件形状。
type personasDoc struct {
	Personas []PersonaEntry `json:"personas"`
}

// fileState 是用于判断“文件是否被外部改过”的快照。
type fileState struct {
	exists bool
	size   int64
	mod    int64
}

// legacyRef 指向 legacy_id 对应的记录种类。
type legacyRef struct {
	kind string
	id   int64
}

// Store 是 $GQY_HOME/memory 下的记忆库集合。
//
// 一个 home 在进程内只保留一个共享实例（Open），HTTP 与 agent 工具共用，
// 追加写后立即可被召回；写入用进程内互斥量串行化（memory.md §7）。
type Store struct {
	home string

	mu   sync.Mutex
	libs map[string]*Library
}

// 共享实例表：key 为规范化后的绝对路径。
var storeRegistry = struct {
	sync.Mutex
	m map[string]*Store
}{m: make(map[string]*Store)}

// cleanHome 把数据根规范成绝对路径，用于共享实例键。
func cleanHome(home string) string {
	home = strings.TrimSpace(home)
	if home == "" {
		return ""
	}
	if abs, err := filepath.Abs(home); err == nil {
		return filepath.Clean(abs)
	}
	return filepath.Clean(home)
}

// Open 返回进程内共享的 Store：HTTP handler 与工具用同一实例。
func Open(home string) *Store {
	key := cleanHome(home)
	storeRegistry.Lock()
	defer storeRegistry.Unlock()
	if s := storeRegistry.m[key]; s != nil {
		return s
	}
	s := newStore(key)
	storeRegistry.m[key] = s
	return s
}

// NewStore 返回不共享的新实例；测试或需要独立快照时使用。
func NewStore(home string) *Store { return newStore(cleanHome(home)) }

func newStore(home string) *Store {
	return &Store{home: home, libs: make(map[string]*Library)}
}

// Home 返回数据根。
func (s *Store) Home() string { return s.home }

// MemoryDir 返回记忆库根目录。
func (s *Store) MemoryDir() string { return filepath.Join(s.home, "memory") }

// Reload 丢弃内存索引，下次访问时重新从 JSONL 载入。
func (s *Store) Reload() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.libs = make(map[string]*Library)
}

// SchemaVersion 返回库格式版本；meta.json 缺失或损坏时回落到 SchemaVersion。
func (s *Store) SchemaVersion() int {
	meta, err := s.Meta()
	if err != nil || meta.SchemaVersion <= 0 {
		return SchemaVersion
	}
	return meta.SchemaVersion
}

// Meta 读取 meta.json；缺失时返回默认元信息，损坏时不阻塞（MEM7 容错）。
func (s *Store) Meta() (Meta, error) {
	fallback := Meta{SchemaVersion: SchemaVersion, Generation: 1, AccessSchemaVersion: 2}
	if s.home == "" {
		return fallback, nil //nolint:nilerr
	}
	data, err := os.ReadFile(filepath.Join(s.MemoryDir(), metaFileName)) // #nosec G304 -- 路径来自数据根
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fallback, nil //nolint:nilerr
		}
		return fallback, nil //nolint:nilerr
	}
	var meta Meta
	if err := json.Unmarshal(data, &meta); err != nil {
		return fallback, nil //nolint:nilerr
	}
	if meta.SchemaVersion <= 0 {
		meta.SchemaVersion = SchemaVersion
	}
	return meta, nil
}

// Library 返回某个人格库；人格 id 非法时报 ErrInvalidPersona。
// 库不存在时返回空库（不报错），保证空库下接口仍可用。
func (s *Store) Library(persona string) (*Library, error) {
	id, err := validatePersona(persona)
	if err != nil {
		return nil, err
	}
	if s.home == "" {
		return nil, fmt.Errorf("memory: 数据根为空")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.libraryLocked(id), nil
}

// libraryLocked 取或建人格库；调用方需持有 s.mu。
func (s *Store) libraryLocked(persona string) *Library {
	if l := s.libs[persona]; l != nil {
		return l
	}
	l := &Library{store: s, persona: persona, dir: filepath.Join(s.MemoryDir(), persona)}
	s.libs[persona] = l
	return l
}

// personasDocLocked 读取 personas.json；缺失或损坏时返回空文档。
func (s *Store) personasDocLocked() personasDoc {
	var doc personasDoc
	data, err := os.ReadFile(filepath.Join(s.MemoryDir(), personasFileName)) // #nosec G304 -- 路径来自数据根
	if err != nil {
		return doc
	}
	_ = json.Unmarshal(data, &doc)
	return doc
}

// personaIDs 汇总人格 id：personas.json 声明 + memory/ 下带 JSONL 的目录。
func (s *Store) personaIDs() ([]string, map[string]string) {
	seen := make(map[string]bool)
	ids := make([]string, 0, 4)
	sources := make(map[string]string)

	for _, entry := range s.personasDocLocked().Personas {
		id := strings.TrimSpace(entry.ID)
		if !ValidPersona(id) || seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
		sources[id] = entry.Source
	}

	dir := s.MemoryDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return ids, sources
	}
	for _, entry := range entries {
		name := entry.Name()
		if !entry.IsDir() || !ValidPersona(name) || seen[name] {
			continue
		}
		if !hasJSONL(filepath.Join(dir, name)) {
			continue
		}
		seen[name] = true
		ids = append(ids, name)
	}
	sort.Strings(ids)
	return ids, sources
}

// hasJSONL 报告目录里是否有任一类记忆文件。
func hasJSONL(dir string) bool {
	for _, name := range jsonlFiles {
		if info, err := os.Stat(filepath.Join(dir, name)); err == nil && !info.IsDir() {
			return true
		}
	}
	return false
}

// Personas 返回全部人格库摘要（按 id 升序）；空库返回空切片而不是错误。
func (s *Store) Personas() ([]PersonaInfo, error) {
	if s.home == "" {
		return []PersonaInfo{}, nil
	}
	ids, sources := s.personaIDs()
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]PersonaInfo, 0, len(ids))
	for _, id := range ids {
		l := s.libraryLocked(id)
		if err := l.refreshLocked(); err != nil {
			// 单个库读失败不拖垮整份清单，留下告警。
			out = append(out, PersonaInfo{ID: id, Source: sources[id], LoadWarnings: []string{err.Error()}})
			continue
		}
		info := l.infoLocked()
		info.Source = sources[id]
		out = append(out, info)
	}
	return out, nil
}

// LoadWarnings 汇总已载入库的损坏行告警；未载入的库不出现。
func (s *Store) LoadWarnings() map[string][]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string][]string)
	for id, l := range s.libs {
		if len(l.warnings) > 0 {
			out[id] = append([]string(nil), l.warnings...)
		}
	}
	return out
}

// ensureMeta 在首次写入时建 meta.json；写入失败不影响记录落盘。
func (s *Store) ensureMeta() {
	path := filepath.Join(s.MemoryDir(), metaFileName)
	if _, err := os.Stat(path); err == nil {
		return
	}
	meta := Meta{
		SchemaVersion:       SchemaVersion,
		DatabaseID:          "mem-" + randomHex(16),
		Generation:          1,
		AccessSchemaVersion: 2,
		UpdatedAt:           time.Now().Format(time.RFC3339),
	}
	_ = storage.AtomicWriteJSON(path, meta, 0o600)
}

// randomHex 生成 n 字节的十六进制随机串。
func randomHex(n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf)
}

// Library 是单个人格库：四个 JSONL 文件 + 内存索引。
type Library struct {
	store   *Store
	persona string
	dir     string

	loaded    bool
	states    map[string]fileState
	facts     map[int64]Record
	episodes  map[int64]Record
	evicted   map[int64]Record
	revisions []Record
	byLegacy  map[string]legacyRef
	maxID     int64
	warnings  []string
}

// Persona 返回人格 id。
func (l *Library) Persona() string { return l.persona }

// Dir 返回人格库目录。
func (l *Library) Dir() string { return l.dir }

func (l *Library) path(name string) string { return filepath.Join(l.dir, name) }

// warn 追加一条载入告警（损坏行、缺 id 等）。
func (l *Library) warn(format string, args ...any) {
	l.warnings = append(l.warnings, fmt.Sprintf(format, args...))
}

// Warnings 返回损坏行等载入告警。
func (l *Library) Warnings() []string {
	l.store.mu.Lock()
	defer l.store.mu.Unlock()
	return append([]string(nil), l.warnings...)
}

// refreshLocked 按文件快照判断是否需要重建索引；调用方需持有 store 互斥量。
func (l *Library) refreshLocked() error {
	want := make(map[string]fileState, len(jsonlFiles))
	for _, name := range jsonlFiles {
		st := fileState{}
		if info, err := os.Stat(l.path(name)); err == nil && !info.IsDir() {
			st = fileState{exists: true, size: info.Size(), mod: info.ModTime().UnixNano()}
		}
		want[name] = st
	}
	if l.loaded && sameStates(l.states, want) {
		return nil
	}
	return l.loadLocked(want)
}

// sameStates 判断两组文件快照是否完全一致。
func sameStates(a, b map[string]fileState) bool {
	if len(a) != len(b) {
		return false
	}
	for name, want := range b {
		if a[name] != want {
			return false
		}
	}
	return true
}

// loadLocked 从四个 JSONL 文件重建索引；损坏行跳过并记告警（MEM7）。
func (l *Library) loadLocked(states map[string]fileState) error {
	l.facts = make(map[int64]Record)
	l.episodes = make(map[int64]Record)
	l.evicted = make(map[int64]Record)
	l.revisions = nil
	l.byLegacy = make(map[string]legacyRef)
	l.maxID = 0
	l.warnings = nil
	l.states = states

	if err := l.loadFileLocked(factsFileName, KindFact); err != nil {
		return err
	}
	if err := l.loadFileLocked(episodesFileName, KindEpisode); err != nil {
		return err
	}
	if err := l.loadFileLocked(revisionsFileName, KindRevision); err != nil {
		return err
	}
	if err := l.loadFileLocked(evictedFileName, KindEvicted); err != nil {
		return err
	}
	sort.SliceStable(l.revisions, func(i, j int) bool { return l.revisions[i].ID < l.revisions[j].ID })
	l.loaded = true
	return nil
}

// loadFileLocked 载入单个 JSONL 文件；同一 id 以最后一行有效（MEM5）。
func (l *Library) loadFileLocked(name, kind string) error {
	data, err := os.ReadFile(l.path(name)) // #nosec G304 -- 路径来自人格库目录
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("memory: 读取 %s: %w", name, err)
	}
	lines := strings.Split(string(data), "\n")
	for i, line := range lines {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if line == "" {
			continue
		}
		var rec Record
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			l.warn("%s:%d: %v", name, i+1, err)
			continue
		}
		normalizeRecord(kind, &rec)
		if rec.ID <= 0 {
			l.maxID++
			rec.ID = l.maxID
			l.warn("%s:%d: 记录缺少 id，已分配 %d", name, i+1, rec.ID)
		}
		if rec.ID > l.maxID {
			l.maxID = rec.ID
		}
		if kind == KindRevision {
			l.revisions = append(l.revisions, rec)
		} else {
			l.putLocked(kind, rec)
		}
		if rec.LegacyID != "" {
			l.byLegacy[rec.LegacyID] = legacyRef{kind: kind, id: rec.ID}
		}
	}
	return nil
}

// normalizeRecord 补齐缺省字段；存储原值不动，只在读入时给默认语义。
func normalizeRecord(kind string, rec *Record) {
	rec.Kind = kind
	if rec.Importance == 0 {
		rec.Importance = DefaultImportance
	}
	if rec.Importance < MinImportance {
		rec.Importance = MinImportance
	}
	if rec.Importance > MaxImportance {
		rec.Importance = MaxImportance
	}
	if rec.Confidence == 0 {
		rec.Confidence = 1
	}
	if rec.Strength == 0 {
		rec.Strength = 1
	}
	if rec.Visibility == "" {
		rec.Visibility = VisibilityPrivileged
	}
	if rec.Status == "" {
		rec.Status = StatusActive
	}
	if rec.CreatedAt == "" {
		rec.CreatedAt = rec.UpdatedAt
	}
	if rec.UpdatedAt == "" {
		rec.UpdatedAt = rec.CreatedAt
	}
	if kind == KindFact && rec.MemoryType == "" {
		rec.MemoryType = TypeFact
	}
	if kind == KindFact && rec.TruthStatus == "" {
		rec.TruthStatus = TruthAccepted
	}
}

// putLocked 把记录放进内存索引（同 id 覆盖，即“最后一行有效”）。
func (l *Library) putLocked(kind string, rec Record) {
	switch kind {
	case KindFact:
		l.facts[rec.ID] = rec
	case KindEpisode:
		l.episodes[rec.ID] = rec
	case KindEvicted:
		l.evicted[rec.ID] = rec
	}
}

// appendLocked 追加一行 JSONL（0600、O_APPEND）并同步更新内存索引。
func (l *Library) appendLocked(kind string, rec Record) error {
	name := fileFor(kind)
	if name == "" {
		return fmt.Errorf("memory: 未知记录种类 %q", kind)
	}
	data, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("memory: 序列化记录: %w", err)
	}
	if err := os.MkdirAll(l.dir, 0o700); err != nil {
		return fmt.Errorf("memory: 创建人格目录: %w", err)
	}
	l.store.ensureMeta()

	path := l.path(name)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600) // #nosec G304 -- 路径来自人格库目录
	if err != nil {
		return fmt.Errorf("memory: 打开 %s: %w", name, err)
	}
	if _, err := f.Write(append(data, '\n')); err != nil {
		_ = f.Close()
		return fmt.Errorf("memory: 追加 %s: %w", name, err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return fmt.Errorf("memory: 落盘 %s: %w", name, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("memory: 关闭 %s: %w", name, err)
	}

	if info, err := os.Stat(path); err == nil {
		if l.states == nil {
			l.states = make(map[string]fileState, len(jsonlFiles))
		}
		l.states[name] = fileState{exists: true, size: info.Size(), mod: info.ModTime().UnixNano()}
	}
	if kind == KindRevision {
		l.revisions = append(l.revisions, rec)
	} else {
		l.putLocked(kind, rec)
	}
	if rec.ID > l.maxID {
		l.maxID = rec.ID
	}
	if rec.LegacyID != "" {
		l.byLegacy[rec.LegacyID] = legacyRef{kind: kind, id: rec.ID}
	}
	return nil
}

// fileFor 返回记录种类对应的文件名。
func fileFor(kind string) string {
	switch kind {
	case KindFact:
		return factsFileName
	case KindEpisode:
		return episodesFileName
	case KindRevision:
		return revisionsFileName
	case KindEvicted:
		return evictedFileName
	}
	return ""
}

// nextIDLocked 分配库内自增 id（四类文件共用 id 空间）。
func (l *Library) nextIDLocked() int64 {
	l.maxID++
	return l.maxID
}

// lookupLocked 按 legacy 引用取记录。
func (l *Library) lookupLocked(ref legacyRef) (Record, bool) {
	switch ref.kind {
	case KindFact:
		rec, ok := l.facts[ref.id]
		return rec, ok
	case KindEpisode:
		rec, ok := l.episodes[ref.id]
		return rec, ok
	case KindEvicted:
		rec, ok := l.evicted[ref.id]
		return rec, ok
	}
	return Record{}, false
}

// Remember 追加一条 fact；legacy_id 重复时幂等返回已有记录（MG3）。
// 写路径不产生 revision，只有归档/恢复才写 revisions（MEM5）。
func (l *Library) Remember(in RememberInput) (Record, error) {
	in, err := in.normalize()
	if err != nil {
		return Record{}, err
	}
	l.store.mu.Lock()
	defer l.store.mu.Unlock()
	if err := l.refreshLocked(); err != nil {
		return Record{}, err
	}
	if in.LegacyID != "" {
		if ref, ok := l.byLegacy[in.LegacyID]; ok {
			if rec, ok := l.lookupLocked(ref); ok {
				return rec, nil
			}
		}
	}

	now := time.Now().Format(time.RFC3339)
	created := in.CreatedAt
	if created == "" {
		created = now
	}
	rec := Record{
		ID:               l.nextIDLocked(),
		LegacyID:         in.LegacyID,
		Kind:             KindFact,
		Content:          in.Content,
		MemoryType:       in.MemoryType,
		TruthStatus:      in.TruthStatus,
		Status:           StatusActive,
		Importance:       in.Importance,
		Confidence:       in.Confidence,
		Strength:         1,
		CreatedAt:        created,
		UpdatedAt:        created,
		Visibility:       in.Visibility,
		OwnerPrincipal:   in.OwnerPrincipal,
		OwnerDisplayName: in.OwnerDisplayName,
		Subjects:         in.Subjects,
		Tags:             StringList(in.Tags),
		SourceEpisodeIDs: Int64List(in.SourceEpisodeIDs),
		OriginSessionID:  in.OriginSessionID,
		Source:           in.Source,
	}
	if err := l.appendLocked(KindFact, rec); err != nil {
		return Record{}, err
	}
	return rec, nil
}

// SetFactStatus 归档 / 恢复一条 fact：追加 revisions 一行 + 同 id 的新行，
// 读时以该 id 最后一行有效（MEM5）。
func (l *Library) SetFactStatus(id int64, status string) (Record, error) {
	if !ValidStatus(status) {
		return Record{}, fmt.Errorf("%w: status %q", ErrInvalid, status)
	}
	if id <= 0 {
		return Record{}, fmt.Errorf("%w: id %d", ErrInvalid, id)
	}
	l.store.mu.Lock()
	defer l.store.mu.Unlock()
	if err := l.refreshLocked(); err != nil {
		return Record{}, err
	}
	cur, ok := l.facts[id]
	if !ok {
		return Record{}, fmt.Errorf("%w: fact %d", ErrNotFound, id)
	}
	if cur.Status == status {
		return cur, nil
	}

	now := time.Now().Format(time.RFC3339)
	rev := Record{
		ID:               l.nextIDLocked(),
		Kind:             KindRevision,
		MemoryID:         id,
		OldContent:       cur.Content,
		NewContent:       cur.Content,
		SourceEpisodeIDs: cur.SourceEpisodeIDs,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	if err := l.appendLocked(KindRevision, rev); err != nil {
		return Record{}, err
	}

	next := cur
	next.Status = status
	next.UpdatedAt = now
	if err := l.appendLocked(KindFact, next); err != nil {
		return Record{}, err
	}
	return next, nil
}

// ListFacts 返回事实列表与过滤后的总数。
func (l *Library) ListFacts(f Filter) ([]Record, int, error) {
	l.store.mu.Lock()
	defer l.store.mu.Unlock()
	if err := l.refreshLocked(); err != nil {
		return []Record{}, 0, err
	}
	items, total := l.selectRecordsLocked(l.facts, f)
	return items, total, nil
}

// ListEpisodes 返回片段列表与过滤后的总数。
func (l *Library) ListEpisodes(f Filter) ([]Record, int, error) {
	l.store.mu.Lock()
	defer l.store.mu.Unlock()
	if err := l.refreshLocked(); err != nil {
		return []Record{}, 0, err
	}
	items, total := l.selectRecordsLocked(l.episodes, f)
	return items, total, nil
}

// ListEvicted 返回被挤出上下文的轮次列表与过滤后的总数。
func (l *Library) ListEvicted(f Filter) ([]Record, int, error) {
	l.store.mu.Lock()
	defer l.store.mu.Unlock()
	if err := l.refreshLocked(); err != nil {
		return []Record{}, 0, err
	}
	items, total := l.selectRecordsLocked(l.evicted, f)
	return items, total, nil
}

// Revisions 返回修订链；memoryID > 0 时只返回该记录的相关修订。
func (l *Library) Revisions(memoryID int64) ([]Record, error) {
	l.store.mu.Lock()
	defer l.store.mu.Unlock()
	if err := l.refreshLocked(); err != nil {
		return []Record{}, err
	}
	out := make([]Record, 0, len(l.revisions))
	for _, rec := range l.revisions {
		if memoryID > 0 && rec.MemoryID != memoryID {
			continue
		}
		out = append(out, rec)
	}
	return out, nil
}

// selectRecordsLocked 过滤、排序（updated_at 降序 → id 降序）并分页。
func (l *Library) selectRecordsLocked(m map[int64]Record, f Filter) ([]Record, int) {
	f = f.normalize()
	items := make([]Record, 0, len(m))
	for _, rec := range m {
		if f.match(rec) {
			items = append(items, rec)
		}
	}
	sort.SliceStable(items, func(i, j int) bool { return newerFirst(items[i], items[j]) })
	total := len(items)
	start := f.Offset
	if start > total {
		start = total
	}
	end := start + f.Limit
	if end > total {
		end = total
	}
	return items[start:end], total
}

// match 判断记录是否满足过滤条件。
func (f Filter) match(rec Record) bool {
	if rec.Visibility == VisibilityNormal && !f.IncludeNormal {
		return false
	}
	if f.MemoryType != "" && rec.MemoryType != f.MemoryType {
		return false
	}
	if f.TruthStatus != "" && rec.TruthStatus != f.TruthStatus {
		return false
	}
	if f.Retention != "" && rec.Retention != f.Retention {
		return false
	}
	if f.Status != "" {
		if rec.Status != f.Status {
			return false
		}
	} else if !f.IncludeArchived && rec.Status != StatusActive {
		return false
	}
	if f.Query != "" && !matchQuery(rec, f.Query) {
		return false
	}
	return true
}

// newerFirst 是列表排序：updated_at 降序 → id 降序（确定性）。
func newerFirst(a, b Record) bool {
	at, aok := parseTime(a.UpdatedAt)
	bt, bok := parseTime(b.UpdatedAt)
	if aok && bok && !at.Equal(bt) {
		return at.After(bt)
	}
	if aok != bok {
		return aok
	}
	return a.ID > b.ID
}

// parseTime 解析 RFC3339 时间；解析失败返回 false。
func parseTime(v string) (time.Time, bool) {
	if v == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

// Stats 返回单库统计。
func (l *Library) Stats() (Stats, error) {
	l.store.mu.Lock()
	defer l.store.mu.Unlock()
	if err := l.refreshLocked(); err != nil {
		return Stats{}, err
	}
	return l.statsLocked(), nil
}

// Counts 返回单库条数。
func (l *Library) Counts() (Counts, error) {
	l.store.mu.Lock()
	defer l.store.mu.Unlock()
	if err := l.refreshLocked(); err != nil {
		return Counts{}, err
	}
	return l.countsLocked(), nil
}

func (l *Library) countsLocked() Counts {
	return Counts{
		Facts:     len(l.facts),
		Episodes:  len(l.episodes),
		Revisions: len(l.revisions),
		Evicted:   len(l.evicted),
	}
}

func (l *Library) statsLocked() Stats {
	byType := make(map[string]int)
	byTruth := make(map[string]int)
	for _, rec := range l.facts {
		byType[rec.MemoryType]++
		byTruth[rec.TruthStatus]++
	}
	return Stats{
		Persona:      l.persona,
		Counts:       l.countsLocked(),
		ByType:       byType,
		ByTruth:      byTruth,
		LastUpdated:  l.lastUpdatedLocked(),
		LoadWarnings: append([]string(nil), l.warnings...),
	}
}

// infoLocked 组装人格库摘要。
func (l *Library) infoLocked() PersonaInfo {
	return PersonaInfo{
		ID:           l.persona,
		Counts:       l.countsLocked(),
		UpdatedAt:    l.lastUpdatedLocked(),
		LoadWarnings: append([]string(nil), l.warnings...),
	}
}

// lastUpdatedLocked 取记录里最大的 updated_at；没有记录时回落到文件 mtime。
func (l *Library) lastUpdatedLocked() string {
	var latest time.Time
	consider := func(v string) {
		if t, ok := parseTime(v); ok && t.After(latest) {
			latest = t
		}
	}
	for _, rec := range l.facts {
		consider(rec.UpdatedAt)
	}
	for _, rec := range l.episodes {
		consider(rec.UpdatedAt)
	}
	for _, rec := range l.evicted {
		consider(rec.UpdatedAt)
	}
	for _, rec := range l.revisions {
		consider(rec.UpdatedAt)
	}
	if latest.IsZero() {
		for _, st := range l.states {
			if !st.exists {
				continue
			}
			if t := time.Unix(0, st.mod); t.After(latest) {
				latest = t
			}
		}
	}
	if latest.IsZero() {
		return ""
	}
	return latest.Format(time.RFC3339)
}
