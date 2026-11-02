package httpapi

import (
	"net/http"
	"strings"

	"gqy/internal/buildinfo"
	"gqy/internal/metrics"
)

func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	snap := snapshotFrom(r)
	if snap == nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "配置快照不可用")
		return
	}

	metrics.Set("gqy_build_info", "Build information", map[string]string{
		"version": buildinfo.Version,
		"commit":  buildinfo.Commit,
	}, 1)
	metrics.Set("gqy_config_version", "Current config snapshot version", nil, float64(snap.Version))

	if s.gw != nil {
		for _, account := range s.gw.AccountStatuses(snap) {
			labels := map[string]string{"provider": account.Provider, "account": account.ID}
			metrics.Set("gqy_gateway_cooldown_seconds", "Account cooldown remaining seconds", labels, float64(account.CooldownRemainingS))
			metrics.Set("gqy_gateway_inflight", "Account in-flight requests", labels, float64(account.InFlight))
			metrics.Set("gqy_gateway_token_used_today", "Account tokens used today", labels, float64(account.TokenUsedToday))
			quota := 0.0
			if account.QuotaExhausted {
				quota = 1
			}
			metrics.Set("gqy_gateway_quota_exhausted", "Account daily quota exhausted", labels, quota)
		}
	}
	if s.plugins != nil {
		for _, plugin := range s.plugins.Status() {
			ready := 0.0
			if plugin.State == "ready" {
				ready = 1
			}
			metrics.Set("gqy_plugin_ready", "Plugin ready state", map[string]string{"plugin": plugin.ID}, ready)
		}
	}

	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = metrics.Default.WriteText(w)
}

// metricPath 把带 id 的路径归一为模板，避免 Prometheus 标签高基数（M4）。
func metricPath(path string) string {
	switch {
	case strings.HasPrefix(path, "/api/sessions/"):
		return normalizePath(path, "/api/sessions/{id}")
	case strings.HasPrefix(path, "/api/plugins/"):
		rest := strings.TrimPrefix(path, "/api/plugins/")
		if rest == "install" || rest == "reload" {
			return path
		}
		return normalizePath(path, "/api/plugins/{id}")
	case strings.HasPrefix(path, "/api/memory/"):
		rest := strings.TrimPrefix(path, "/api/memory/")
		parts := strings.Split(rest, "/")
		for i := 1; i < len(parts); i++ {
			if isDigits(parts[i]) {
				parts[i] = "{id}"
			}
		}
		return "/api/memory/{persona}/" + strings.Join(parts[1:], "/")
	case strings.HasPrefix(path, "/v1beta/models/"):
		return "/v1beta/models/{model}"
	default:
		return path
	}
}

func normalizePath(path, base string) string {
	rest := strings.TrimPrefix(path, strings.Split(base, "{")[0])
	parts := strings.Split(rest, "/")
	if len(parts) <= 1 {
		return base
	}
	return base + "/" + strings.Join(parts[1:], "/")
}

func isDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
