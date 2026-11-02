package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"gqy/internal/plugin"
)

func (s *Server) requirePlugins(w http.ResponseWriter) *plugin.Manager {
	if s.plugins == nil {
		writeError(w, http.StatusServiceUnavailable, "plugins_unavailable", "插件运行时未初始化")
		return nil
	}
	return s.plugins
}

// handlePlugins 处理 GET /api/plugins。
func (s *Server) handlePlugins(w http.ResponseWriter, r *http.Request) {
	pm := s.requirePlugins(w)
	if pm == nil {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"plugins": pm.Status()})
}

// handlePluginsReload 处理 POST /api/plugins/reload。
func (s *Server) handlePluginsReload(w http.ResponseWriter, r *http.Request) {
	pm := s.requirePlugins(w)
	if pm == nil {
		return
	}
	if err := pm.Scan(); err != nil {
		writeError(w, http.StatusInternalServerError, "plugin_reload_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "plugins": pm.Status()})
}

// handlePluginLoad 处理 POST /api/plugins/{id}/load。
func (s *Server) handlePluginLoad(w http.ResponseWriter, r *http.Request) {
	pm := s.requirePlugins(w)
	if pm == nil {
		return
	}
	if err := pm.Load(r.Context(), r.PathValue("id")); err != nil {
		writeError(w, http.StatusBadRequest, "plugin_load_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "plugins": pm.Status()})
}

// handlePluginUnload 处理 POST /api/plugins/{id}/unload。
func (s *Server) handlePluginUnload(w http.ResponseWriter, r *http.Request) {
	pm := s.requirePlugins(w)
	if pm == nil {
		return
	}
	if err := pm.Unload(r.Context(), r.PathValue("id")); err != nil {
		writeError(w, http.StatusBadRequest, "plugin_unload_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "plugins": pm.Status()})
}

// handlePluginCall 处理 POST /api/plugins/{id}/call（调试调用，P12）。
func (s *Server) handlePluginCall(w http.ResponseWriter, r *http.Request) {
	pm := s.requirePlugins(w)
	if pm == nil {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "plugin_call_failed", "请求体读取失败")
		return
	}
	var req struct {
		Action    string         `json:"action"`
		Arguments map[string]any `json:"arguments"`
	}
	if err := json.Unmarshal(body, &req); err != nil || req.Action == "" {
		writeError(w, http.StatusBadRequest, "plugin_call_failed", "需要 action 字段")
		return
	}
	start := time.Now()
	content, err := pm.Call(r.Context(), r.PathValue("id"), req.Action, req.Arguments)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": pluginErrCode(err), "message": err.Error(), "duration_ms": time.Since(start).Milliseconds()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "content": content, "duration_ms": time.Since(start).Milliseconds()})
}

func pluginErrCode(err error) string {
	if errors.Is(err, plugin.ErrNotFound) {
		return "not_found"
	}
	if errors.Is(err, plugin.ErrNotReady) {
		return "not_ready"
	}
	if errors.Is(err, plugin.ErrTimeout) {
		return "timeout"
	}
	if errors.Is(err, plugin.ErrCanceled) {
		return "canceled"
	}
	return "call_failed"
}
