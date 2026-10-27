package httpapi

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

type bucket struct {
	minute int64
	count  int
}

type rateLimiter struct {
	mu      sync.Mutex
	buckets map[string]*bucket
}

func (s *Server) withRateLimit(next http.Handler) http.Handler {
	if s.Cfg.RateLimit <= 0 {
		return next
	}
	lim := &rateLimiter{buckets: map[string]*bucket{}}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/") && !strings.HasPrefix(r.URL.Path, "/v1/") {
			next.ServeHTTP(w, r)
			return
		}
		key := clientIP(r)
		minute := time.Now().Unix() / 60
		lim.mu.Lock()
		// 桶只在内存里；超过阈值时先清掉上一分钟的旧桶，避免长期运行累积。
		if len(lim.buckets) > 4096 {
			for k, old := range lim.buckets {
				if old.minute != minute {
					delete(lim.buckets, k)
				}
			}
		}
		b := lim.buckets[key]
		if b == nil || b.minute != minute {
			b = &bucket{minute: minute}
			lim.buckets[key] = b
		}
		b.count++
		over := b.count > s.Cfg.RateLimit
		lim.mu.Unlock()
		if over {
			w.Header().Set("Retry-After", "60")
			if strings.HasPrefix(r.URL.Path, "/v1/") {
				writeOpenAIError(w, http.StatusTooManyRequests, "rate_limited", "请求过于频繁")
			} else {
				writeErr(w, http.StatusTooManyRequests, "rate_limited", "请求过于频繁，请稍后再试")
			}
			return
		}
		next.ServeHTTP(w, r)
	})
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return r.RemoteAddr
}
