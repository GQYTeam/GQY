package httpapi

import (
	"encoding/json"
	"net/http"

	"gqy/internal/persona"
)

func (s *Server) handlePersonaGet(w http.ResponseWriter, r *http.Request) {
	snap := snapshotFrom(r)
	if snap == nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "配置快照不可用")
		return
	}
	writeJSON(w, http.StatusOK, persona.Load(snap.Home))
}

func (s *Server) handlePersonaPut(w http.ResponseWriter, r *http.Request) {
	snap := snapshotFrom(r)
	if snap == nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "配置快照不可用")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var in persona.Persona
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "人格设置不是合法 JSON")
		return
	}
	if err := persona.Save(snap.Home, in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_persona", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "persona": persona.Load(snap.Home)})
}
