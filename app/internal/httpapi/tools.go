package httpapi

import "net/http"

// handleTools 返回核心与外部工具的只读清单（T1、T3）。
func (s *Server) handleTools(w http.ResponseWriter, r *http.Request) {
	if s.agentLoop == nil {
		writeError(w, http.StatusServiceUnavailable, "tools_unavailable", "agent loop 未初始化")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tools": s.agentLoop.ToolInfos()})
}
