package httpapi

import (
	"context"
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

// handlePluginSettingsGet 处理 GET /api/plugins/{id}/settings（PL3）。
func (s *Server) handlePluginSettingsGet(w http.ResponseWriter, r *http.Request) {
	pm := s.requirePlugins(w)
	if pm == nil {
		return
	}
	id := r.PathValue("id")
	schema, readOnly, source, err := pm.SettingSchema(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "plugin_not_found", err.Error())
		return
	}
	values, err := pm.Settings(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "plugin_settings_failed", err.Error())
		return
	}
	effective, err := pm.EffectiveSettings(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "plugin_settings_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id": id, "schema": schema, "values": values, "effective": effective,
		"source": source, "read_only": readOnly,
	})
}

// handlePluginSettingsPut 处理 PUT /api/plugins/{id}/settings（PL3）。
func (s *Server) handlePluginSettingsPut(w http.ResponseWriter, r *http.Request) {
	pm := s.requirePlugins(w)
	if pm == nil {
		return
	}
	var body struct {
		Values map[string]any `json:"values"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "请求体不是合法 JSON")
		return
	}
	id := r.PathValue("id")
	if err := pm.SaveSettings(id, body.Values); err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, plugin.ErrNotFound) {
			status = http.StatusNotFound
		}
		writeError(w, status, "plugin_settings_failed", err.Error())
		return
	}
	effective, _ := pm.EffectiveSettings(id)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "effective": effective})
}

// handlePluginDelete 处理 DELETE /api/plugins/{id}（PL4）：卸载并删除目录与设置。
func (s *Server) handlePluginDelete(w http.ResponseWriter, r *http.Request) {
	pm := s.requirePlugins(w)
	if pm == nil {
		return
	}
	if err := pm.Delete(r.Context(), r.PathValue("id")); err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, plugin.ErrNotFound) {
			status = http.StatusNotFound
		}
		writeError(w, status, "plugin_delete_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "plugins": pm.Status()})
}

// handlePluginInstall 处理 POST /api/plugins/install（PL1）：从 https Git 仓库安装插件。
func (s *Server) handlePluginInstall(w http.ResponseWriter, r *http.Request) {
	pm := s.requirePlugins(w)
	if pm == nil {
		return
	}
	var body struct {
		Source string `json:"source"`
		ID     string `json:"id"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "请求体不是合法 JSON")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 180*time.Second)
	defer cancel()
	if err := pm.Install(ctx, body.Source, body.ID); err != nil {
		writeError(w, http.StatusBadRequest, "plugin_install_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "plugins": pm.Status()})
}
