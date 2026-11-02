package httpapi

import (
	"encoding/json"
	"io"
	"net/http"

	"gqy/internal/goal"
)

func (s *Server) requireGoals(w http.ResponseWriter) *goal.Manager {
	if s.goals == nil {
		writeError(w, http.StatusServiceUnavailable, "goal_unavailable", "goal 运行时未初始化")
		return nil
	}
	return s.goals
}

// handleGoalCreate 处理 POST /api/goal。
func (s *Server) handleGoalCreate(w http.ResponseWriter, r *http.Request) {
	gm := s.requireGoals(w)
	if gm == nil {
		return
	}
	var body struct {
		Title     string `json:"title"`
		MaxRounds int    `json:"max_rounds"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&body); err != nil || body.Title == "" {
		writeError(w, http.StatusBadRequest, "goal_invalid", "需要 title")
		return
	}
	g := gm.Create(body.Title, body.MaxRounds)
	writeJSON(w, http.StatusOK, g)
}

// handleGoalList 处理 GET /api/goal。
func (s *Server) handleGoalList(w http.ResponseWriter, r *http.Request) {
	gm := s.requireGoals(w)
	if gm == nil {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"goals": gm.Status()})
}

// handleGoalPause 处理 POST /api/goal/{id}/pause。
func (s *Server) handleGoalPause(w http.ResponseWriter, r *http.Request) {
	gm := s.requireGoals(w)
	if gm == nil {
		return
	}
	var body struct {
		Reason string `json:"reason"`
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&body)
	if err := gm.Pause(r.PathValue("id"), body.Reason); err != nil {
		writeError(w, http.StatusBadRequest, "goal_pause_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleGoalResume 处理 POST /api/goal/{id}/resume。
func (s *Server) handleGoalResume(w http.ResponseWriter, r *http.Request) {
	gm := s.requireGoals(w)
	if gm == nil {
		return
	}
	var body struct {
		Reason string `json:"reason"`
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&body)
	if err := gm.Resume(r.PathValue("id"), body.Reason); err != nil {
		writeError(w, http.StatusBadRequest, "goal_resume_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleGoalQueue 处理 POST /api/goal/{id}/queue。
func (s *Server) handleGoalQueue(w http.ResponseWriter, r *http.Request) {
	gm := s.requireGoals(w)
	if gm == nil {
		return
	}
	var body struct {
		Message string `json:"message"`
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&body)
	if err := gm.Queue(r.PathValue("id"), body.Message); err != nil {
		writeError(w, http.StatusBadRequest, "goal_queue_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleGoalAdopt 处理 POST /api/goal/{id}/adopt。
func (s *Server) handleGoalAdopt(w http.ResponseWriter, r *http.Request) {
	gm := s.requireGoals(w)
	if gm == nil {
		return
	}
	var body struct {
		BranchID string `json:"branch_id"`
		Summary  string `json:"summary"`
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&body)
	if err := gm.Adopt(r.PathValue("id"), body.BranchID, body.Summary); err != nil {
		writeError(w, http.StatusBadRequest, "goal_adopt_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
