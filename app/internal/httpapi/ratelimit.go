package httpapi

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// rateLimiter 是按客户端 IP 的分钟固定窗口计数（A4）。
type rateLimiter struct {
	mu      sync.Mutex
	buckets map[string]rateBucket
}

type rateBucket struct {
	window time.Time
	count  int
}

func newRateLimiter() *rateLimiter {
	return &rateLimiter{buckets: make(map[string]rateBucket)}
}

// allow 返回是否放行，以及超限时建议的 Retry-After 秒数。
func (l *rateLimiter) allow(key string, limit int, now time.Time) (bool, int) {
	if limit <= 0 {
		return true, 0
	}
	window := now.Truncate(time.Minute)
	retryAfter := 60 - now.Second()
	if retryAfter <= 0 {
		retryAfter = 60
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	if len(l.buckets) > 4096 {
		l.cleanup(window)
	}
	b, ok := l.buckets[key]
	if !ok || !b.window.Equal(window) {
		b = rateBucket{window: window}
	}
	b.count++
	l.buckets[key] = b
	if b.count > limit {
		return false, retryAfter
	}
	return true, 0
}

// cleanup 调用方必须持有 l.mu。
func (l *rateLimiter) cleanup(current time.Time) {
	cutoff := current.Add(-2 * time.Minute)
	for key, b := range l.buckets {
		if b.window.Before(cutoff) {
			delete(l.buckets, key)
		}
	}
}

// clientIP 只认连接对端地址，不信任 X-Forwarded-For（A4）。
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil && host != "" {
		return host
	}
	return r.RemoteAddr
}

func isLoopbackRemote(r *http.Request) bool {
	ip := net.ParseIP(clientIP(r))
	return ip != nil && ip.IsLoopback()
}

func needsAuth(path string) bool {
	switch {
	case path == "/api/auth":
		return false
	case path == "/metrics":
		return true
	case path == "/api" || strings.HasPrefix(path, "/api/"):
		return true
	case path == "/v1" || strings.HasPrefix(path, "/v1/"):
		return true
	case path == "/v1beta" || strings.HasPrefix(path, "/v1beta/"):
		return true
	}
	return false
}
