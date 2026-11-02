package httpapi

// 全局默认模型（HR5）：PUT /api/model/active 一次选择，之后所有「未指定 model」的请求
// （内部 /api/chat、外部 OpenAI/Anthropic/Gemini 客户端）都走它；可选持久化到 routing.json。

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"gqy/internal/config"
)

type activeModelResponse struct {
	// Model 是当前生效的运行时覆盖；空表示使用 routing.json 的默认。
	Model string `json:"model"`
	// Source 为 runtime（有覆盖）或 config（用配置默认）。
	Source string `json:"source"`
	// Default 是配置里的默认模型引用（可能为空）。
	Default string `json:"default,omitempty"`
}

func defaultRefString(snap *config.Snapshot) string {
	if snap == nil || snap.Routing.Default == nil {
		return ""
	}
	return snap.Routing.Default.Provider + "/" + snap.Routing.Default.Model
}

// handleActiveModelGet 处理 GET /api/model/active。
func (s *Server) handleActiveModelGet(w http.ResponseWriter, r *http.Request) {
	snap := snapshotFrom(r)
	resp := activeModelResponse{Source: "config", Default: defaultRefString(snap)}
	if s.gw != nil {
		if m := s.gw.ActiveModel(); m != "" {
			resp.Model = m
			resp.Source = "runtime"
		} else {
			resp.Model = resp.Default
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleActiveModelPut 处理 PUT /api/model/active：{model, persist?}。
// 空 model 表示清除覆盖；persist=true 时同时写回 routing.json 的 default。
func (s *Server) handleActiveModelPut(w http.ResponseWriter, r *http.Request) {
	if s.gw == nil {
		writeError(w, http.StatusServiceUnavailable, "gateway_unavailable", "网关未初始化")
		return
	}
	snap := snapshotFrom(r)
	var body struct {
		Model   string `json:"model"`
		Persist bool   `json:"persist"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 16<<10)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "请求体不是合法 JSON")
		return
	}
	model := strings.TrimSpace(body.Model)
	if model != "" {
		if err := s.gw.ResolveModel(snap, model); err != nil {
			writeError(w, http.StatusBadRequest, "model_not_found", err.Error())
			return
		}
	}

	s.gw.SetActiveModel(model)
	persisted := false
	if body.Persist && model != "" && snap != nil {
		if err := persistDefaultModel(snap.Home, model); err != nil {
			s.log.Warn("全局默认模型持久化失败", "error", err)
		} else if _, err := s.mgr.Reload(); err != nil {
			s.log.Warn("持久化后重载配置失败", "error", err)
		} else {
			persisted = true
		}
	}
	s.Publish("model", map[string]any{"model": model, "persisted": persisted})
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "model": model, "persisted": persisted, "source": "runtime",
	})
}

// persistDefaultModel 把 provider/model 写回 routing.json 的 default（原子写）。
// 只接受 provider/model 形式；group/xxx 不持久化（组是策略，不是单一模型）。
func persistDefaultModel(home, model string) error {
	provider, name, ok := strings.Cut(model, "/")
	if !ok || provider == "" || name == "" || strings.HasPrefix(model, "group/") {
		return nil
	}
	path := filepath.Join(home, "routing.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	doc := map[string]any{}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return err
	}
	doc["default"] = map[string]any{"provider": provider, "model": name}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
