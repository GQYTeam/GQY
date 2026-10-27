// 包 httpapi：HTTP 路由与处理器，聚合 config/catalog/provider/session。
package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gqy/internal/catalog"
	"gqy/internal/config"
	"gqy/internal/provider"
	"gqy/internal/session"
)

const apiVersion = 1

var mimeTypes = map[string]string{
	".html":  "text/html; charset=utf-8",
	".js":    "text/javascript; charset=utf-8",
	".css":   "text/css; charset=utf-8",
	".json":  "application/json; charset=utf-8",
	".svg":   "image/svg+xml",
	".png":   "image/png",
	".jpg":   "image/jpeg",
	".jpeg":  "image/jpeg",
	".webp":  "image/webp",
	".ico":   "image/x-icon",
	".woff2": "font/woff2",
	".md":    "text/markdown; charset=utf-8",
	".txt":   "text/plain; charset=utf-8",
}

type Server struct {
	Root     string
	Cfg      config.Config
	Client   *http.Client
	Catalog  *catalog.Catalog
	Session  *session.Store
	Upstream *provider.Upstream
}

func New(root string, cfg config.Config, client *http.Client) *Server {
	return &Server{
		Root:    root,
		Cfg:     cfg,
		Client:  client,
		Catalog: catalog.New(client),
		Session: session.New(cfg.Home),
		Upstream: &provider.Upstream{
			APIBase: cfg.APIBase,
			APIKey:  cfg.APIKey,
			APIUA:   cfg.APIUA,
			Model:   cfg.Model,
			Wire:    cfg.Wire,
			Mock:    cfg.Mock,
			Client:  client,
		},
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", s.handleHealth)
	mux.HandleFunc("GET /api/models", s.handleModels)
	mux.HandleFunc("GET /api/usage", s.handleUsage)
	mux.HandleFunc("POST /api/chat", s.handleChat)
	mux.HandleFunc("GET /api/sessions", s.handleSessionList)
	mux.HandleFunc("GET /api/sessions/{id}", s.handleSessionGet)
	mux.HandleFunc("PUT /api/sessions/{id}", s.handleSessionPut)
	mux.HandleFunc("DELETE /api/sessions/{id}", s.handleSessionDelete)

	allow := func(methods string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Allow", methods)
			writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "只支持 "+methods)
		}
	}
	mux.HandleFunc("/api/health", allow("GET"))
	mux.HandleFunc("/api/models", allow("GET"))
	mux.HandleFunc("/api/usage", allow("GET"))
	mux.HandleFunc("/api/chat", allow("POST"))
	mux.HandleFunc("/api/sessions", allow("GET"))
	mux.HandleFunc("/api/sessions/{id}", allow("GET, PUT, DELETE"))
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeErr(w, http.StatusNotFound, "not_found", "接口不存在")
	})
	mux.HandleFunc("/", s.handleStatic)
	return s.withRequestID(s.logRequests(mux))
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":       true,
		"provider": s.Cfg.Provider(),
		"model":    s.Cfg.Model,
		"home":     s.Cfg.Home,
		"version":  apiVersion,
	})
}

func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	entries, err := s.Catalog.Snapshot()
	if err != nil {
		log.Printf("[models] 拉取 models.dev 失败：%v", err)
		writeErr(w, http.StatusBadGateway, "catalog_unavailable", "暂时无法获取模型列表，请稍后再试")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"models":    entries,
		"count":     len(entries),
		"source":    "https://models.dev/api.json",
		"fetchedAt": s.Catalog.FetchedAt().UTC().Format(time.RFC3339),
	})
}

func (s *Server) handleUsage(w http.ResponseWriter, r *http.Request) {
	stats := s.Session.Stats()
	writeJSON(w, http.StatusOK, map[string]any{
		"usage":     stats,
		"generated": time.Now().UTC().Format(time.RFC3339),
	})
}

func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	s.Upstream.Handle(w, r, provider.Request{})
}

/* ---------------- 会话 ---------------- */

func (s *Server) handleSessionPut(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !session.IDRe.MatchString(id) {
		writeErr(w, http.StatusBadRequest, "bad_session_id", "会话 id 不合法")
		return
	}
	var payload session.Payload
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<20))
	if err := dec.Decode(&payload); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_json", "请求体不是合法 JSON")
		return
	}
	if payload.ID != "" && payload.ID != id {
		writeErr(w, http.StatusBadRequest, "id_mismatch", "路径与会话 id 不一致")
		return
	}
	dir, err := s.Session.Put(id, payload)
	if err != nil {
		log.Printf("[session] %v", err)
		writeErr(w, http.StatusInternalServerError, "write_failed", "写入会话失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "dir": dir})
}

func (s *Server) handleSessionGet(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !session.IDRe.MatchString(id) {
		writeErr(w, http.StatusBadRequest, "bad_session_id", "会话 id 不合法")
		return
	}
	raw, err := s.Session.Get(id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "not_found", "会话不存在")
		return
	}
	lines := make([]string, 0, 64)
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.TrimSpace(line) != "" {
			lines = append(lines, line)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id":       id,
		"messages": json.RawMessage("[" + strings.Join(lines, ",") + "]"),
	})
}

func (s *Server) handleSessionDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !session.IDRe.MatchString(id) {
		writeErr(w, http.StatusBadRequest, "bad_session_id", "会话 id 不合法")
		return
	}
	if err := s.Session.Delete(id); err != nil {
		writeErr(w, http.StatusInternalServerError, "delete_failed", "删除失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleSessionList(w http.ResponseWriter, r *http.Request) {
	list, err := s.Session.List()
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"sessions": []any{}, "home": s.Cfg.Home})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"sessions": list, "home": s.Cfg.Home})
}

/* ---------------- 静态文件 ---------------- */

func (s *Server) handleStatic(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "只支持 GET / HEAD")
		return
	}
	rel, err := cleanPath(r.URL.Path)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_path", "路径非法")
		return
	}
	if rel == "" {
		rel = "index.html"
	}
	if hasHiddenSegment(rel) || strings.HasPrefix(rel, "node_modules/") || strings.HasPrefix(rel, "scripts/") ||
		strings.HasPrefix(rel, "bin/") || strings.HasPrefix(rel, "tmp/") ||
		strings.HasPrefix(rel, "cmd/") || strings.HasPrefix(rel, "internal/") {
		writeErr(w, http.StatusNotFound, "not_found", "文件不存在")
		return
	}
	base := path.Base(rel)
	if base == "package.json" || base == "package-lock.json" || base == "go.mod" || base == "go.sum" ||
		strings.HasSuffix(base, ".go") {
		writeErr(w, http.StatusNotFound, "not_found", "文件不存在")
		return
	}
	ext := strings.ToLower(path.Ext(rel))
	contentType, ok := mimeTypes[ext]
	if !ok {
		writeErr(w, http.StatusNotFound, "not_found", "文件不存在")
		return
	}
	file, err := os.Open(filepath.Join(s.Root, filepath.FromSlash(rel)))
	if err != nil {
		writeErr(w, http.StatusNotFound, "not_found", "文件不存在")
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || info.IsDir() {
		writeErr(w, http.StatusNotFound, "not_found", "文件不存在")
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Etag", fmt.Sprintf(`W/"%x-%x"`, info.Size(), info.ModTime().UnixNano()))
	http.ServeContent(w, r, base, info.ModTime(), file)
}

func cleanPath(raw string) (string, error) {
	decoded, err := url.PathUnescape(raw)
	if err != nil {
		return "", err
	}
	cleaned := path.Clean("/" + strings.TrimPrefix(decoded, "/"))
	rel := strings.TrimPrefix(cleaned, "/")
	if rel == "." || rel == "/" {
		return "", nil
	}
	if strings.HasPrefix(rel, "../") || strings.Contains(rel, "/../") {
		return "", errors.New("traversal")
	}
	return rel, nil
}

func hasHiddenSegment(rel string) bool {
	for _, seg := range strings.Split(rel, "/") {
		if strings.HasPrefix(seg, ".") {
			return true
		}
	}
	return false
}

/* ---------------- 中间件与 JSON ---------------- */

var requestIDAlphabet = []byte("0123456789abcdefghijklmnopqrstuvwxyz")

func newRequestID() string {
	b := make([]byte, 8)
	for i := range b {
		b[i] = requestIDAlphabet[rand.Intn(len(requestIDAlphabet))]
	}
	return string(b)
}

func (s *Server) withRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Request-ID", newRequestID())
		next.ServeHTTP(w, r)
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (s *statusWriter) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusWriter) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (s *Server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)
		if strings.HasPrefix(r.URL.Path, "/api/") {
			log.Printf("%s %s %s %d %s", r.Header.Get("X-Request-ID"), r.Method, r.URL.Path, sw.status, time.Since(start).Round(time.Millisecond))
		}
	})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	payload, err := json.Marshal(body)
	if err != nil {
		http.Error(w, `{"error":"encode_failed"}`, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_, _ = w.Write(payload)
}

func writeErr(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]string{"error": code, "message": message})
}
