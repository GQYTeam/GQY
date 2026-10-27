// 包 httpapi：可选访问令牌。GQY_AUTH_TOKEN 为空时不启用。
package httpapi

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strings"
)

const authCookie = "gqy_token"

func (s *Server) authEnabled() bool { return s.Cfg.AuthToken != "" }

func (s *Server) withAuth(next http.Handler) http.Handler {
	if !s.authEnabled() {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !protectedPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		if s.validToken(r) {
			next.ServeHTTP(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/v1/") {
			writeOpenAIError(w, http.StatusUnauthorized, "unauthorized", "需要 Authorization: Bearer <GQY_AUTH_TOKEN>")
			return
		}
		if strings.HasPrefix(r.URL.Path, "/v1beta/") {
			writeGeminiError(w, http.StatusUnauthorized, "需要访问令牌")
			return
		}
		writeErr(w, http.StatusUnauthorized, "unauthorized", "需要访问令牌")
	})
}

func protectedPath(p string) bool {
	if p == "/api/auth" {
		return false
	}
	return strings.HasPrefix(p, "/api/") || strings.HasPrefix(p, "/v1/") || strings.HasPrefix(p, "/v1beta/")
}

func (s *Server) validToken(r *http.Request) bool {
	token := s.Cfg.AuthToken
	if token == "" {
		return true
	}
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		if subtle.ConstantTimeCompare([]byte(strings.TrimPrefix(h, "Bearer ")), []byte(token)) == 1 {
			return true
		}
	}
	if h := r.Header.Get("X-GQY-Token"); subtle.ConstantTimeCompare([]byte(h), []byte(token)) == 1 {
		return true
	}
	if c, err := r.Cookie(authCookie); err == nil && subtle.ConstantTimeCompare([]byte(c.Value), []byte(token)) == 1 {
		return true
	}
	return false
}

func (s *Server) handleAuth(w http.ResponseWriter, r *http.Request) {
	if !s.authEnabled() {
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
		return
	}
	var req struct {
		Token string `json:"token"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10))
	if err := dec.Decode(&req); err != nil || subtle.ConstantTimeCompare([]byte(req.Token), []byte(s.Cfg.AuthToken)) != 1 {
		writeErr(w, http.StatusUnauthorized, "bad_token", "访问令牌不正确")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     authCookie,
		Value:    s.Cfg.AuthToken,
		Path:     "/",
		HttpOnly: true,
		// 反向代理终止 TLS 时也要带上 Secure，避免令牌走明文回源后由浏览器继续明文发送。
		Secure:   r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https"),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   7 * 24 * 3600,
	})
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
