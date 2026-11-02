package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"gqy/internal/buildinfo"
	"gqy/internal/config"
)

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	snap := snapshotFrom(r)
	version := uint64(0)
	if snap != nil {
		version = snap.Version
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":   "ok",
		"version":  version,
		"build":    buildinfo.String(),
		"uptime_s": int64(time.Since(s.started).Seconds()),
	})
}

func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	snap := snapshotFrom(r)
	if snap == nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "配置快照不可用")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"object": "list",
		"data":   modelCatalog(snap),
	})
}

type authRequest struct {
	Token string `json:"token"`
}

func (s *Server) handleAuth(w http.ResponseWriter, r *http.Request) {
	snap := snapshotFrom(r)
	if snap == nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "配置快照不可用")
		return
	}
	if snap.AuthToken == "" {
		writeError(w, http.StatusBadRequest, "auth_disabled", "未启用访问令牌")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	var body authRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "请求体必须是 JSON，且包含 token 字段")
		return
	}
	if !constantTimeEqual(body.Token, snap.AuthToken) {
		writeError(w, http.StatusUnauthorized, "auth_required", "访问令牌错误")
		return
	}

	secure := r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
	http.SetCookie(w, &http.Cookie{ // #nosec G124 -- HttpOnly/SameSite 已设置，Secure 按请求条件设置
		Name:     cookieName,
		Value:    snap.AuthToken,
		Path:     "/",
		MaxAge:   7 * 24 * 60 * 60,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   secure,
	})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "auth_enabled": true})
}

type configResponse struct {
	Version    uint64                `json:"version"`
	LoadedAt   time.Time             `json:"loaded_at"`
	LastReload time.Time             `json:"last_reload,omitempty"`
	LastError  string                `json:"last_error,omitempty"`
	Providers  []config.ProviderView `json:"providers"`
	Routing    config.Routing        `json:"routing"`
	Settings   config.Settings       `json:"settings"`
}

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	snap := snapshotFrom(r)
	if snap == nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "配置快照不可用")
		return
	}
	status := s.mgr.Status()
	writeJSON(w, http.StatusOK, configResponse{
		Version:    snap.Version,
		LoadedAt:   snap.LoadedAt,
		LastReload: status.LastReload,
		LastError:  status.LastError,
		Providers:  snap.ProviderViews(),
		Routing:    snap.Routing,
		Settings:   snap.Settings,
	})
}

func (s *Server) handleReload(w http.ResponseWriter, r *http.Request) {
	snap, err := s.mgr.Reload()
	if err != nil {
		s.log.Warn("配置重载失败", "error", err)
		writeError(w, http.StatusUnprocessableEntity, "invalid_config", err.Error())
		return
	}
	s.log.Info("配置重载成功", "version", snap.Version)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":           true,
		"version":      snap.Version,
		"loaded_at":    snap.LoadedAt,
		"providers":    len(snap.Providers),
		"groups":       len(snap.Routing.Groups),
		"max_attempts": snap.Routing.MaxAttempts,
	})
}

type accountStatus struct {
	Provider           string `json:"provider"`
	ID                 string `json:"id"`
	APIKeySet          bool   `json:"api_key_set"`
	Healthy            bool   `json:"healthy"`
	Calls              int64  `json:"calls"`
	Failures           int64  `json:"failures"`
	InFlight           int    `json:"in_flight"`
	Capacity           int    `json:"capacity"`
	CooldownRemainingS int    `json:"cooldown_remaining_s"`
	TokenUsedToday     int64  `json:"token_used_today"`
	TokenLimit         int64  `json:"token_limit"`
	QuotaExhausted     bool   `json:"quota_exhausted"`
}

type statusResponse struct {
	Config      config.ManagerStatus  `json:"config"`
	AuthEnabled bool                  `json:"auth_enabled"`
	RateLimit   int                   `json:"rate_limit"`
	Providers   []config.ProviderView `json:"providers"`
	Accounts    []accountStatus       `json:"accounts"`
	Routing     statusRouting         `json:"routing"`
}

type statusRouting struct {
	Default *config.ModelRef `json:"default,omitempty"`
	Groups  []config.Group   `json:"groups"`
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	snap := snapshotFrom(r)
	if snap == nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "配置快照不可用")
		return
	}

	keySet := make(map[string]bool)
	for _, p := range snap.Providers {
		for _, a := range p.Accounts {
			keySet[p.ID+"\x00"+a.ID] = a.APIKey != ""
		}
	}

	accounts := make([]accountStatus, 0)
	if s.gw != nil {
		for _, st := range s.gw.AccountStatuses(snap) {
			accounts = append(accounts, accountStatus{
				Provider:           st.Provider,
				ID:                 st.ID,
				APIKeySet:          keySet[st.Provider+"\x00"+st.ID],
				Healthy:            st.Healthy,
				Calls:              st.Calls,
				Failures:           st.Failures,
				InFlight:           st.InFlight,
				Capacity:           st.Capacity,
				CooldownRemainingS: st.CooldownRemainingS,
				TokenUsedToday:     st.TokenUsedToday,
				TokenLimit:         st.TokenLimit,
				QuotaExhausted:     st.QuotaExhausted,
			})
		}
	} else {
		for _, p := range snap.Providers {
			for _, a := range p.Accounts {
				accounts = append(accounts, accountStatus{
					Provider:   p.ID,
					ID:         a.ID,
					APIKeySet:  a.APIKey != "",
					TokenLimit: a.DailyTokenLimit,
				})
			}
		}
	}

	status := statusResponse{
		Config:      s.mgr.Status(),
		AuthEnabled: snap.AuthToken != "",
		RateLimit:   snap.RateLimit,
		Providers:   snap.ProviderViews(),
		Accounts:    accounts,
		Routing: statusRouting{
			Default: snap.Routing.Default,
			Groups:  append([]config.Group(nil), snap.Routing.Groups...),
		},
	}
	writeJSON(w, http.StatusOK, status)
}
