package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"gqy/internal/memory"
)

// 记忆接口（/api/memory）：九个端点，全部走现有鉴权 / 限流 / 租户中间件，
// 不额外挂中间件（memory.md §3）。数据根从请求的配置快照取，store 在进程内
// 共享，写后立即可读。

// memoryStore 返回请求对应的记忆库集合；配置快照缺失时写 500。
func (s *Server) memoryStore(w http.ResponseWriter, r *http.Request) *memory.Store {
	home := homeOf(r)
	if home == "" {
		writeError(w, http.StatusInternalServerError, "internal_error", "配置快照不可用")
		return nil
	}
	return memory.Open(home)
}

// memoryLibrary 取路径里的人格库；人格 id 非法 400，其它错误 500。
func (s *Server) memoryLibrary(w http.ResponseWriter, r *http.Request) *memory.Library {
	store := s.memoryStore(w, r)
	if store == nil {
		return nil
	}
	lib, err := store.Library(r.PathValue("persona"))
	if err != nil {
		if errors.Is(err, memory.ErrInvalidPersona) {
			writeError(w, http.StatusBadRequest, "invalid_persona", err.Error())
			return nil
		}
		writeError(w, http.StatusInternalServerError, "memory_unavailable", err.Error())
		return nil
	}
	return lib
}

// handleMemoryList 处理 GET /api/memory：人格清单与统计。
func (s *Server) handleMemoryList(w http.ResponseWriter, r *http.Request) {
	store := s.memoryStore(w, r)
	if store == nil {
		return
	}
	personas, err := store.Personas()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "memory_list_failed", err.Error())
		return
	}
	for _, info := range personas {
		if len(info.LoadWarnings) > 0 {
			// 只记条数与人格名，不记正文（G6）。
			s.log.Warn("记忆库载入有损坏行", "persona", info.ID, "warnings", len(info.LoadWarnings))
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"schema_version": store.SchemaVersion(),
		"personas":       personas,
	})
}

// handleMemoryStats 处理 GET /api/memory/{persona}/stats。
func (s *Server) handleMemoryStats(w http.ResponseWriter, r *http.Request) {
	lib := s.memoryLibrary(w, r)
	if lib == nil {
		return
	}
	stats, err := lib.Stats()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "memory_stats_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

// handleMemoryFacts 处理 GET /api/memory/{persona}/facts。
func (s *Server) handleMemoryFacts(w http.ResponseWriter, r *http.Request) {
	lib := s.memoryLibrary(w, r)
	if lib == nil {
		return
	}
	filter, err := memoryFilterFrom(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	records, total, err := lib.ListFacts(filter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "memory_list_failed", err.Error())
		return
	}
	items := make([]memory.FactView, 0, len(records))
	for _, rec := range records {
		items = append(items, memory.FactViewOf(rec))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items": items, "total": total, "offset": filter.Offset, "limit": filter.Limit,
	})
}

// handleMemoryEpisodes 处理 GET /api/memory/{persona}/episodes。
func (s *Server) handleMemoryEpisodes(w http.ResponseWriter, r *http.Request) {
	lib := s.memoryLibrary(w, r)
	if lib == nil {
		return
	}
	filter, err := memoryFilterFrom(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	records, total, err := lib.ListEpisodes(filter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "memory_list_failed", err.Error())
		return
	}
	items := make([]memory.EpisodeView, 0, len(records))
	for _, rec := range records {
		items = append(items, memory.EpisodeViewOf(rec))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items": items, "total": total, "offset": filter.Offset, "limit": filter.Limit,
	})
}

// handleMemoryEvicted 处理 GET /api/memory/{persona}/evicted。
func (s *Server) handleMemoryEvicted(w http.ResponseWriter, r *http.Request) {
	lib := s.memoryLibrary(w, r)
	if lib == nil {
		return
	}
	filter, err := memoryFilterFrom(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	records, total, err := lib.ListEvicted(filter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "memory_list_failed", err.Error())
		return
	}
	items := make([]memory.EvictedView, 0, len(records))
	for _, rec := range records {
		items = append(items, memory.EvictedViewOf(rec))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items": items, "total": total, "offset": filter.Offset, "limit": filter.Limit,
	})
}

// handleMemoryRevisions 处理 GET /api/memory/{persona}/revisions?memory_id=。
func (s *Server) handleMemoryRevisions(w http.ResponseWriter, r *http.Request) {
	lib := s.memoryLibrary(w, r)
	if lib == nil {
		return
	}
	var memoryID int64
	if raw := strings.TrimSpace(r.URL.Query().Get("memory_id")); raw != "" {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || id <= 0 {
			writeError(w, http.StatusBadRequest, "invalid_request", "memory_id 必须是正整数")
			return
		}
		memoryID = id
	}
	records, err := lib.Revisions(memoryID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "memory_list_failed", err.Error())
		return
	}
	items := make([]memory.RevisionView, 0, len(records))
	for _, rec := range records {
		items = append(items, memory.RevisionViewOf(rec))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// memoryRecallItem 是召回结果的一行（content 已截断）。
type memoryRecallItem struct {
	ID         int64   `json:"id"`
	Kind       string  `json:"kind"`
	Content    string  `json:"content"`
	Truncated  bool    `json:"truncated,omitempty"`
	Score      float64 `json:"score"`
	MemoryType string  `json:"memory_type,omitempty"`
	Importance int     `json:"importance"`
	UpdatedAt  string  `json:"updated_at,omitempty"`
}

// handleMemoryRecall 处理 POST /api/memory/{persona}/recall。
// 同参数两次调用结果完全一致；读路径不写盘（MEM4）。
func (s *Server) handleMemoryRecall(w http.ResponseWriter, r *http.Request) {
	lib := s.memoryLibrary(w, r)
	if lib == nil {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var body struct {
		Query           string            `json:"query"`
		Limit           int               `json:"limit"`
		Kinds           []string          `json:"kinds"`
		IncludeArchived bool              `json:"include_archived"`
		QueryEmbedding  *memory.Embedding `json:"query_embedding"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid_request", "请求体不是合法 JSON")
		return
	}
	kinds, err := memoryValidateKinds(body.Kinds)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_kinds", err.Error())
		return
	}

	limit := body.Limit
	if limit <= 0 {
		limit = memory.DefaultRecallLimit
	}
	if limit > memory.MaxRecallLimit {
		limit = memory.MaxRecallLimit
	}

	scored, err := lib.Recall(memory.RecallRequest{
		Query:           body.Query,
		Limit:           limit,
		Kinds:           kinds,
		IncludeArchived: body.IncludeArchived,
		QueryEmbedding:  body.QueryEmbedding,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "memory_recall_failed", err.Error())
		return
	}
	items := make([]memoryRecallItem, 0, len(scored))
	for _, item := range scored {
		content, truncated := memory.TruncateContent(item.Record.Content)
		items = append(items, memoryRecallItem{
			ID:         item.Record.ID,
			Kind:       item.Record.Kind,
			Content:    content,
			Truncated:  truncated,
			Score:      item.Score,
			MemoryType: item.Record.MemoryType,
			Importance: item.Record.Importance,
			UpdatedAt:  item.Record.UpdatedAt,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"query": body.Query, "items": items})
}

// handleMemoryFactCreate 处理 POST /api/memory/{persona}/facts。
func (s *Server) handleMemoryFactCreate(w http.ResponseWriter, r *http.Request) {
	lib := s.memoryLibrary(w, r)
	if lib == nil {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var body struct {
		Content          string             `json:"content"`
		MemoryType       string             `json:"memory_type"`
		TruthStatus      string             `json:"truth_status"`
		Importance       int                `json:"importance"`
		Confidence       float64            `json:"confidence"`
		Tags             []string           `json:"tags"`
		Subjects         memory.SubjectList `json:"subjects"`
		SourceEpisodeIDs []int64            `json:"source_episode_ids"`
		Visibility       string             `json:"visibility"`
		OwnerPrincipal   string             `json:"owner_principal"`
		OwnerDisplayName string             `json:"owner_display_name"`
		OriginSessionID  string             `json:"origin_session_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "请求体不是合法 JSON")
		return
	}

	rec, err := lib.Remember(memory.RememberInput{
		Content:          body.Content,
		MemoryType:       body.MemoryType,
		TruthStatus:      body.TruthStatus,
		Importance:       body.Importance,
		Confidence:       body.Confidence,
		Tags:             body.Tags,
		Subjects:         body.Subjects,
		SourceEpisodeIDs: body.SourceEpisodeIDs,
		Visibility:       body.Visibility,
		OwnerPrincipal:   body.OwnerPrincipal,
		OwnerDisplayName: body.OwnerDisplayName,
		OriginSessionID:  body.OriginSessionID,
		Source:           "api",
	})
	if err != nil {
		if errors.Is(err, memory.ErrInvalid) {
			writeError(w, http.StatusBadRequest, "invalid_record", err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "memory_write_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "fact": memory.FactViewOf(rec)})
}

// handleMemoryFactPatch 处理 PATCH /api/memory/{persona}/facts/{id}：
// 归档 / 恢复只追加 revision 与新行，不删行（MEM5）。
func (s *Server) handleMemoryFactPatch(w http.ResponseWriter, r *http.Request) {
	lib := s.memoryLibrary(w, r)
	if lib == nil {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid_request", "id 必须是正整数")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var body struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "请求体不是合法 JSON")
		return
	}
	status := strings.TrimSpace(body.Status)
	if status == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "需要 status 字段（active / archived）")
		return
	}

	rec, err := lib.SetFactStatus(id, status)
	if err != nil {
		switch {
		case errors.Is(err, memory.ErrNotFound):
			writeError(w, http.StatusNotFound, "memory_not_found", err.Error())
		case errors.Is(err, memory.ErrInvalid):
			writeError(w, http.StatusBadRequest, "invalid_status", err.Error())
		default:
			writeError(w, http.StatusInternalServerError, "memory_write_failed", err.Error())
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "fact": memory.FactViewOf(rec)})
}

// memoryFilterFrom 解析列表查询参数；非法值返回错误由 handler 转 400。
func memoryFilterFrom(r *http.Request) (memory.Filter, error) {
	query := r.URL.Query()
	filter := memory.Filter{
		Query:       strings.TrimSpace(query.Get("q")),
		MemoryType:  strings.TrimSpace(query.Get("type")),
		TruthStatus: strings.TrimSpace(query.Get("truth")),
		Status:      strings.TrimSpace(query.Get("status")),
		Retention:   strings.TrimSpace(query.Get("retention")),
	}
	if raw := strings.TrimSpace(query.Get("include_archived")); raw != "" {
		value, err := strconv.ParseBool(raw)
		if err != nil {
			return filter, fmt.Errorf("include_archived 必须是布尔值")
		}
		filter.IncludeArchived = value
	}
	limit, err := memoryQueryInt(query.Get("limit"), memory.DefaultListLimit)
	if err != nil {
		return filter, fmt.Errorf("limit 必须是整数")
	}
	if limit <= 0 {
		limit = memory.DefaultListLimit
	}
	if limit > memory.MaxListLimit {
		limit = memory.MaxListLimit
	}
	offset, err := memoryQueryInt(query.Get("offset"), 0)
	if err != nil {
		return filter, fmt.Errorf("offset 必须是整数")
	}
	if offset < 0 {
		offset = 0
	}
	filter.Limit = limit
	filter.Offset = offset
	return filter, nil
}

// memoryQueryInt 解析整型查询参数，空值返回缺省值。
func memoryQueryInt(raw string, fallback int) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, err
	}
	return value, nil
}

// memoryValidateKinds 校验 kinds 参数，只认三类可召回记录。
func memoryValidateKinds(kinds []string) ([]string, error) {
	out := make([]string, 0, len(kinds))
	for _, kind := range kinds {
		kind = strings.TrimSpace(kind)
		if kind == "" {
			continue
		}
		if !memory.RecallableKind(kind) {
			return nil, fmt.Errorf("kinds 只支持 fact / episode / evicted")
		}
		out = append(out, kind)
	}
	return out, nil
}
