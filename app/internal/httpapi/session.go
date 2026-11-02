package httpapi

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"gqy/internal/session"
)

func (s *Server) sessionStore(home string) *session.Store {
	return session.NewStore(home)
}

func homeOf(r *http.Request) string {
	if snap := snapshotFrom(r); snap != nil {
		return snap.Home
	}
	return ""
}

// handleSessionCreate 处理 POST /api/sessions。
func (s *Server) handleSessionCreate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Workspace string `json:"workspace"`
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&body)
	sess, err := s.sessionStore(homeOf(r)).Create(body.Workspace)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "session_create_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, sess.Manifest)
}

// handleSessionList 处理 GET /api/sessions。
func (s *Server) handleSessionList(w http.ResponseWriter, r *http.Request) {
	list, err := s.sessionStore(homeOf(r)).List()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "session_list_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"sessions": list})
}

// handleSessionGet 处理 GET /api/sessions/{id}。
func (s *Server) handleSessionGet(w http.ResponseWriter, r *http.Request) {
	store := s.sessionStore(homeOf(r))
	sess, err := store.Open(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "session_not_found", err.Error())
		return
	}
	events, _ := session.ReadEvents(sess.EventsPath())
	writeJSON(w, http.StatusOK, map[string]any{
		"manifest": sess.Manifest,
		"messages": session.Rebuild(events),
	})
}

// handleSessionDelete 处理 DELETE /api/sessions/{id}。
func (s *Server) handleSessionDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.sessionStore(homeOf(r)).Delete(r.PathValue("id")); err != nil {
		writeError(w, http.StatusNotFound, "session_delete_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleSessionDraftGet 处理 GET /api/sessions/{id}/draft。
func (s *Server) handleSessionDraftGet(w http.ResponseWriter, r *http.Request) {
	sess, err := s.sessionStore(homeOf(r)).Open(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "session_not_found", err.Error())
		return
	}
	content, err := session.LoadDraft(sess.DraftPath())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "draft_read_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"draft": content})
}

// handleSessionDraftPut 处理 PUT /api/sessions/{id}/draft。
func (s *Server) handleSessionDraftPut(w http.ResponseWriter, r *http.Request) {
	sess, err := s.sessionStore(homeOf(r)).Open(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "session_not_found", err.Error())
		return
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeError(w, http.StatusBadRequest, "draft_read_failed", "草稿读取失败")
		return
	}
	var body struct {
		Draft *string `json:"draft"`
	}
	var content string
	if err := json.Unmarshal(data, &body); err == nil && body.Draft != nil {
		content = *body.Draft
	} else {
		content = strings.TrimSpace(string(data))
		content = strings.Trim(content, `"`)
	}
	if err := session.SaveDraft(sess.DraftPath(), content); err != nil {
		writeError(w, http.StatusInternalServerError, "draft_save_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleSessionUpload 处理 POST /api/sessions/{id}/uploads（multipart file）。
func (s *Server) handleSessionUpload(w http.ResponseWriter, r *http.Request) {
	sess, err := s.sessionStore(homeOf(r)).Open(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "session_not_found", err.Error())
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<20)
	if err := r.ParseMultipartForm(64 << 20); err != nil { // #nosec G120 -- 已用 MaxBytesReader 限制 64 MiB
		writeError(w, http.StatusBadRequest, "upload_failed", "multipart 解析失败")
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "upload_failed", "缺少 file 字段")
		return
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, 64<<20))
	if err != nil {
		writeError(w, http.StatusBadRequest, "upload_failed", "读取附件失败")
		return
	}
	path, err := session.SaveUpload(sess.UploadsDir(), header.Filename, data)
	if err != nil {
		writeError(w, http.StatusConflict, "upload_conflict", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "path": path})
}

// handleSessionArtifacts 处理 POST /api/sessions/{id}/artifacts。
func (s *Server) handleSessionArtifacts(w http.ResponseWriter, r *http.Request) {
	sess, err := s.sessionStore(homeOf(r)).Open(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "session_not_found", err.Error())
		return
	}
	var body struct {
		Kind string `json:"kind"`
		Path string `json:"path"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "artifact_invalid", "请求体不是合法 JSON")
		return
	}
	if body.Path == "" {
		writeError(w, http.StatusBadRequest, "artifact_invalid", "path 不能为空")
		return
	}
	if err := sess.AddArtifact(body.Kind, body.Path); err != nil {
		writeError(w, http.StatusInternalServerError, "artifact_save_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "manifest": sess.Manifest})
}
