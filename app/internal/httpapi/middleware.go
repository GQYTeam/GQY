package httpapi

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"net/http"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"gqy/internal/config"
	"gqy/internal/gateway"
	"gqy/internal/metrics"
)

type contextKey int

const (
	ctxRequestID contextKey = iota
	ctxSnapshot
)

const cookieName = "gqy_token"

func (s *Server) withSnapshot(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		snap := s.mgr.Current()
		ctx := context.WithValue(r.Context(), ctxSnapshot, snap)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func snapshotFrom(r *http.Request) *config.Snapshot {
	snap, _ := r.Context().Value(ctxSnapshot).(*config.Snapshot)
	return snap
}

// ctxTenant 保存请求所属租户（MT2）。
type ctxTenantKey struct{}

// tenantFrom 取请求租户；空表示 default（MT2）。
func tenantFrom(r *http.Request) string {
	v, _ := r.Context().Value(ctxTenantKey{}).(string)
	return v
}

// tenant 解析 X-GQY-Tenant / X-GQY-Task，非法字符直接 400（MT2/MT3）。
func (s *Server) tenant(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimSpace(r.Header.Get("X-GQY-Tenant"))
		if name == "" {
			name = strings.TrimSpace(r.Header.Get("X-GQY-Task"))
		}
		if name != "" && !validTenantName(name) {
			writeError(w, http.StatusBadRequest, "invalid_tenant", "租户名非法：只能包含字母、数字、下划线、点、短横线，长度 1–64")
			return
		}
		if name != "" {
			r = r.WithContext(context.WithValue(r.Context(), ctxTenantKey{}, name))
		}
		next.ServeHTTP(w, r)
	})
}

// validTenantName 与 config 侧规则一致（MT2）。
func validTenantName(name string) bool {
	if name == "" || len(name) > 64 {
		return false
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '.', r == '-':
		default:
			return false
		}
	}
	return true
}

// metaFor 组装网关调用元数据（含租户）。
func metaFor(r *http.Request) gateway.Meta {
	return gateway.Meta{
		RequestID: requestIDFrom(r),
		SessionID: sessionIDFrom(r),
		Tenant:    tenantFrom(r),
	}
}

func requestIDFrom(r *http.Request) string {
	id, _ := r.Context().Value(ctxRequestID).(string)
	return id
}

func (s *Server) requestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-ID")
		if !validRequestID(id) {
			id = newRequestID()
		}
		w.Header().Set("X-Request-ID", id)
		ctx := context.WithValue(r.Context(), ctxRequestID, id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func validRequestID(id string) bool {
	if id == "" || len(id) > 128 {
		return false
	}
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '-', r == '_', r == '.':
		default:
			return false
		}
	}
	return true
}

func newRequestID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("req-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}

func (s *Server) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				s.log.Error("请求处理 panic",
					"request_id", requestIDFrom(r),
					"panic", v,
					"stack", string(debug.Stack()),
				)
				writeError(w, http.StatusInternalServerError, "internal_error", "服务内部错误")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func (s *Server) accessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &responseRecorder{ResponseWriter: w}
		next.ServeHTTP(rec, r)
		status := rec.status
		if status == 0 {
			status = http.StatusOK
		}
		path := metricPath(r.URL.Path)
		metrics.Inc("gqy_http_requests_total", "HTTP requests total", map[string]string{
			"method": r.Method, "path": path, "status": strconv.Itoa(status),
		})
		metrics.Observe("gqy_http_request_duration_seconds", "HTTP request duration in seconds", map[string]string{
			"method": r.Method, "path": path,
		}, time.Since(start).Seconds())
		s.log.Info("http",
			"request_id", requestIDFrom(r),
			"method", r.Method,
			"path", r.URL.Path,
			"status", status,
			"bytes", rec.bytes,
			"duration_ms", time.Since(start).Milliseconds(),
			"remote", clientIP(r),
		)
	})
}

type responseRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (w *responseRecorder) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *responseRecorder) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	n, err := w.ResponseWriter.Write(p)
	w.bytes += n
	return n, err
}

func (w *responseRecorder) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *responseRecorder) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		h.Set("Content-Security-Policy",
			"default-src 'self'; img-src 'self' data: blob:; style-src 'self' 'unsafe-inline'; script-src 'self'; connect-src 'self'; font-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) rateLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isLimitedPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		snap := snapshotFrom(r)
		limit := 0
		if snap != nil {
			limit = snap.RateLimit
		}
		allowed, retryAfter := s.limiter.allow(clientIP(r), limit, s.now())
		if !allowed {
			w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
			writeError(w, http.StatusTooManyRequests, "rate_limited", "请求过于频繁，请稍后再试")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func isLimitedPath(path string) bool {
	return path == "/api" || strings.HasPrefix(path, "/api/") ||
		path == "/v1" || strings.HasPrefix(path, "/v1/") ||
		path == "/v1beta" || strings.HasPrefix(path, "/v1beta/")
}

func (s *Server) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !needsAuth(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		snap := snapshotFrom(r)
		if snap == nil {
			writeError(w, http.StatusInternalServerError, "internal_error", "配置快照不可用")
			return
		}
		if snap.AuthToken == "" {
			if isLoopbackRemote(r) {
				next.ServeHTTP(w, r)
				return
			}
			writeError(w, http.StatusForbidden, "auth_required", "未设置访问令牌时只允许本机访问")
			return
		}
		if tokenMatches(snap.AuthToken, r) {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Set("WWW-Authenticate", `Bearer realm="gqy"`)
		writeError(w, http.StatusUnauthorized, "auth_required", "缺少或错误的访问令牌")
	})
}

func tokenMatches(want string, r *http.Request) bool {
	if auth := r.Header.Get("Authorization"); auth != "" {
		parts := strings.Fields(auth)
		if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") && constantTimeEqual(parts[1], want) {
			return true
		}
	}
	if c, err := r.Cookie(cookieName); err == nil && constantTimeEqual(c.Value, want) {
		return true
	}
	return false
}

// constantTimeEqual 先哈希再比较，长度差异不会提前返回。
func constantTimeEqual(a, b string) bool {
	ha := sha256.Sum256([]byte(a))
	hb := sha256.Sum256([]byte(b))
	return subtle.ConstantTimeCompare(ha[:], hb[:]) == 1
}
