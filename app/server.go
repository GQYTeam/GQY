// 顾清影 · 本地服务（Go 标准库，无第三方依赖）
//
// 职责只有三件：
//  1. 静态托管前端（扩展名白名单 + 路径规范化）
//  2. POST /api/chat  → SSE 转发 OpenAI 兼容接口（DeepSeek / 任何兼容网关）
//  3. PUT  /api/sessions/{id} → 按 docs/AboutProjectFile.md 落盘会话
//
// 没有数据库、没有框架、没有模块依赖。密钥只在服务端环境变量里。
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

const (
	apiVersion = 1

	maxBodyChat    = 256 << 10
	maxBodySession = 8 << 20
	maxMessages    = 200
	maxChars       = 200_000
	maxArtifact    = 2 << 20
	maxSessionMsgs = 500
)

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

type config struct {
	host    string
	port    string
	apiBase string
	apiKey  string
	apiUA   string
	model   string
	wire    string // auto | chat | responses
	home    string
	mock    bool
}

func (c config) provider() string {
	switch {
	case c.apiKey != "":
		return "openai-compatible"
	case c.mock:
		return "mock"
	default:
		return "none"
	}
}

type app struct {
	root   string
	cfg    config
	client *http.Client
}

// requestID 给每个请求一个短随机 id，串进日志，便于生产环境对账。
var requestIDAlphabet = []byte("0123456789abcdefghijklmnopqrstuvwxyz")

func newRequestID() string {
	b := make([]byte, 8)
	for i := range b {
		b[i] = requestIDAlphabet[rand.Intn(len(requestIDAlphabet))]
	}
	return string(b)
}

func main() {
	// 生产环境二进制可能与源码分离：先读可执行文件旁的 .env，再读当前目录的 .env
	// （真实环境变量始终优先，loadDotEnv 只填环境里没有的变量）。
	if exe, err := os.Executable(); err == nil {
		loadDotEnv(filepath.Join(filepath.Dir(exe), ".env"))
	}
	loadDotEnv(".env")

	root := findStaticRoot()
	if root == "" {
		log.Fatalf("找不到静态前端：请在 app/ 里运行，或把二进制和 index.html 放在一起")
	}

	cfg := config{
		host:    env("HOST", "127.0.0.1"),
		port:    env("PORT", "5173"),
		apiBase: strings.TrimRight(env("GQY_API_BASE", "https://api.deepseek.com/v1"), "/"),
		apiKey:  os.Getenv("GQY_API_KEY"),
		apiUA:   os.Getenv("GQY_API_UA"),
		model:   env("GQY_MODEL", "deepseek-chat"),
		wire:    strings.ToLower(env("GQY_API_WIRE", "auto")),
		home:    env("GQY_HOME", filepath.Join(homeDir(), ".gqy")),
		mock:    isTruthy(os.Getenv("GQY_MOCK")),
	}

	a := &app{
		root: root,
		cfg:  cfg,
		client: &http.Client{
			// 不设整体超时：SSE 是长连接。只在等响应头时兜底。
			Transport: &http.Transport{
				Proxy:                 http.ProxyFromEnvironment,
				ResponseHeaderTimeout: 30 * time.Second,
				MaxIdleConnsPerHost:   4,
			},
		},
	}

	srv := &http.Server{
		Addr:              net.JoinHostPort(cfg.host, cfg.port),
		Handler:           a.routes(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		log.Println("关闭中")
		_ = srv.Shutdown(shutdown)
	}()

	log.Printf("顾清影 · http://%s", srv.Addr)
	log.Printf("  provider : %s", cfg.provider())
	if cfg.provider() == "openai-compatible" {
		log.Printf("  model    : %s", cfg.model)
		log.Printf("  wire     : %s", cfg.wire)
	}
	log.Printf("  sessions : %s", filepath.Join(cfg.home, "sessions"))
	if cfg.provider() == "none" {
		log.Printf("  提示     : 未配置 GQY_API_KEY，前端会退回本地演示回复")
	}

	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("启动失败: %v", err)
	}
}

/* ---------------- 环境变量 ---------------- */

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func isTruthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

func homeDir() string {
	if h, err := os.UserHomeDir(); err == nil {
		return h
	}
	return "."
}

// findStaticRoot 定位前端静态目录：优先当前目录，其次可执行文件所在目录。
// 生产部署常见「把 gqy-server 和 index.html/assets/ 放在同一个目录」，
// 这样二进制即使不在 app/ 下运行也能找到前端。
func findStaticRoot() string {
	if cwd, err := os.Getwd(); err == nil {
		if _, err := os.Stat(filepath.Join(cwd, "index.html")); err == nil {
			return cwd
		}
	}
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		if _, err := os.Stat(filepath.Join(dir, "index.html")); err == nil {
			return dir
		}
	}
	return ""
}

// loadDotEnv 读 .env，只填环境里不存在的变量；真实环境变量（包括显式空值）优先。
func loadDotEnv(name string) {
	data, err := os.ReadFile(name)
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		if key == "" {
			continue
		}
		// LookupEnv 区分「没设置」和「显式设为空」：后者用于强制关闭 .env 里的值
		if _, exists := os.LookupEnv(key); !exists {
			_ = os.Setenv(key, value)
		}
	}
}

/* ---------------- 路由 ---------------- */

func (a *app) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", a.handleHealth)
	mux.HandleFunc("POST /api/chat", a.handleChat)
	mux.HandleFunc("GET /api/sessions", a.handleSessionList)
	mux.HandleFunc("GET /api/sessions/{id}", a.handleSessionGet)
	mux.HandleFunc("PUT /api/sessions/{id}", a.handleSessionPut)
	mux.HandleFunc("DELETE /api/sessions/{id}", a.handleSessionDelete)
	// 方法不匹配时给出 405，而不是让它掉进静态路由的 404
	allow := func(methods string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Allow", methods)
			writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "只支持 "+methods)
		}
	}
	mux.HandleFunc("/api/health", allow("GET"))
	mux.HandleFunc("/api/chat", allow("POST"))
	mux.HandleFunc("/api/sessions", allow("GET"))
	mux.HandleFunc("/api/sessions/{id}", allow("GET, PUT, DELETE"))
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeErr(w, http.StatusNotFound, "not_found", "接口不存在")
	})
	mux.HandleFunc("/", a.handleStatic)
	return a.withRequestID(a.logRequests(mux))
}

func (a *app) withRequestID(next http.Handler) http.Handler {
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

// Flush 必须透传，否则 SSE 会被缓冲到请求结束。
func (s *statusWriter) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (a *app) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)
		if strings.HasPrefix(r.URL.Path, "/api/") {
			log.Printf("%s %s %s %d %s", r.Header.Get("X-Request-ID"), r.Method, r.URL.Path, sw.status, time.Since(start).Round(time.Millisecond))
		}
	})
}

/* ---------------- JSON ---------------- */

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

func (a *app) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":       true,
		"provider": a.cfg.provider(),
		"model":    a.cfg.model,
		"home":     a.cfg.home,
		"version":  apiVersion,
	})
}

/* ---------------- 静态文件 ---------------- */

func (a *app) handleStatic(w http.ResponseWriter, r *http.Request) {
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
	// 白名单：不托管服务端源码、包描述、隐藏文件、构建产物
	if hasHiddenSegment(rel) || strings.HasPrefix(rel, "node_modules/") || strings.HasPrefix(rel, "scripts/") ||
		strings.HasPrefix(rel, "bin/") || strings.HasPrefix(rel, "tmp/") {
		writeErr(w, http.StatusNotFound, "not_found", "文件不存在")
		return
	}
	base := path.Base(rel)
	if base == "package.json" || base == "package-lock.json" || base == "go.mod" || base == "go.sum" {
		writeErr(w, http.StatusNotFound, "not_found", "文件不存在")
		return
	}
	ext := strings.ToLower(path.Ext(rel))
	contentType, ok := mimeTypes[ext]
	if !ok {
		writeErr(w, http.StatusNotFound, "not_found", "文件不存在")
		return
	}

	file, err := os.Open(filepath.Join(a.root, filepath.FromSlash(rel)))
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

// cleanPath 把 URL 路径规范成相对 root 的安全路径；越界或编码异常返回错误。
func cleanPath(raw string) (string, error) {
	// 只解码一次；重复解码是路径穿越的常见入口。
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

/* ---------------- 聊天：SSE ---------------- */

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Messages []chatMessage `json:"messages"`
	Model    string        `json:"model"`
	Effort   string        `json:"effort"`
}

type usage struct {
	PromptTokens     int  `json:"prompt_tokens"`
	CompletionTokens int  `json:"completion_tokens"`
	TotalTokens      int  `json:"total_tokens"`
	Mock             bool `json:"mock,omitempty"`
}

type toolEvent struct {
	Name   string     `json:"name"`
	Detail string     `json:"detail"`
	Icon   string     `json:"icon"`
	Status string     `json:"status"`
	MS     int64      `json:"ms,omitempty"`
	Args   [][]string `json:"args,omitempty"`
	Output string     `json:"output,omitempty"`
}

type sseEvent struct {
	Type    string     `json:"type"`
	Delta   string     `json:"delta,omitempty"`
	Tool    *toolEvent `json:"tool,omitempty"`
	Usage   *usage     `json:"usage,omitempty"`
	Code    string     `json:"code,omitempty"`
	Message string     `json:"message,omitempty"`
}

// sseWriter 串行化写入，保证心跳和事件不会交错。
type sseWriter struct {
	mu        sync.Mutex
	w         http.ResponseWriter
	fl        http.Flusher
	lastEvent atomic.Int64
}

func (s *sseWriter) send(ev sseEvent) {
	payload, err := json.Marshal(ev)
	if err != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastEvent.Store(time.Now().UnixMilli())
	_, _ = fmt.Fprintf(s.w, "data: %s\n\n", payload)
	s.fl.Flush()
}

func (s *sseWriter) comment(text string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, _ = fmt.Fprintf(s.w, ": %s\n\n", text)
	s.fl.Flush()
}

func normalizeMessages(in []chatMessage) []chatMessage {
	if len(in) == 0 {
		return nil
	}
	if len(in) > maxMessages {
		in = in[len(in)-maxMessages:]
	}
	kept := make([]chatMessage, 0, len(in))
	for _, m := range in {
		role := "user"
		switch m.Role {
		case "assistant":
			role = "assistant"
		case "system":
			role = "system"
		}
		content := m.Content
		if runes := []rune(content); len(runes) > maxChars {
			content = string(runes[:maxChars])
		}
		if strings.TrimSpace(content) == "" {
			continue
		}
		kept = append(kept, chatMessage{Role: role, Content: content})
	}
	// 从尾部往前累计字符预算，避免把整个历史都塞给上游
	total := 0
	start := len(kept)
	for i := len(kept) - 1; i >= 0; i-- {
		total += len([]rune(kept[i].Content))
		if total > maxChars {
			break
		}
		start = i
	}
	// 至少保留最后一条
	if start >= len(kept) && len(kept) > 0 {
		start = len(kept) - 1
	}
	return kept[start:]
}

func (a *app) handleChat(w http.ResponseWriter, r *http.Request) {
	var req chatRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyChat))
	if err := dec.Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_json", "请求体不是合法 JSON")
		return
	}

	messages := normalizeMessages(req.Messages)
	if len(messages) == 0 {
		writeErr(w, http.StatusBadRequest, "messages_required", "messages 不能为空")
		return
	}
	if a.cfg.provider() == "none" {
		writeErr(w, http.StatusServiceUnavailable, "no_provider", "服务端未配置 GQY_API_KEY，前端会退回本地演示回复")
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusInternalServerError, "no_flusher", "当前连接不支持流式")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	out := &sseWriter{w: w, fl: flusher}
	out.lastEvent.Store(time.Now().UnixMilli())
	started := time.Now()

	// 心跳 + 上游无输出超时
	go func() {
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				idle := time.Since(time.UnixMilli(out.lastEvent.Load()))
				if idle > 120*time.Second {
					out.send(sseEvent{Type: "error", Code: "upstream_timeout", Message: "模型服务 120 秒没有响应"})
					cancel()
					return
				}
				out.comment("ping")
			}
		}
	}()

	var err error
	switch {
	case a.cfg.provider() == "mock":
		err = a.mockStream(ctx, out)
	case a.cfg.wire == "responses":
		err = a.responsesStream(ctx, req, messages, out)
	default:
		err = a.upstreamStream(ctx, req, messages, out)
		var ue *upstreamError
		// 上游说这个模型只支持 /v1/responses 时自动换协议
		if a.cfg.wire == "auto" && errors.As(err, &ue) && ue.code == "need_responses" {
			log.Printf("[chat] 模型只支持 Responses API，自动切换 wire")
			err = a.responsesStream(ctx, req, messages, out)
		}
	}
	if err != nil && ctx.Err() == nil {
		code := "chat_failed"
		var ue *upstreamError
		if errors.As(err, &ue) {
			code = ue.code
		}
		log.Printf("[chat] %v", err)
		out.send(sseEvent{Type: "error", Code: code, Message: err.Error()})
	}
	out.send(sseEvent{Type: "end"})
	log.Printf("[chat] %s %d msgs %.1fs", a.cfg.provider(), len(messages), time.Since(started).Seconds())
}

const mockReply = "这是服务端的本地模拟流（`GQY_MOCK=1`），用来在没有密钥时验证链路。\n\n" +
	"接入真实模型只需要三个环境变量：\n\n" +
	"```bash\n" +
	"export GQY_API_KEY=sk-...\n" +
	"export GQY_API_BASE=https://api.deepseek.com/v1\n" +
	"export GQY_MODEL=deepseek-chat\n" +
	"```\n\n" +
	"然后重新启动 `npm start`。前端不需要改动。"

func (a *app) mockStream(ctx context.Context, out *sseWriter) error {
	sleep := func(d time.Duration) bool {
		select {
		case <-ctx.Done():
			return false
		case <-time.After(d):
			return true
		}
	}
	out.send(sseEvent{Type: "thinking", Delta: "服务端未配置密钥，走本地模拟流。\n"})
	if !sleep(120 * time.Millisecond) {
		return nil
	}
	tool := toolEvent{Name: "读取环境变量", Detail: "GQY_API_KEY", Icon: "i-terminal", Status: "running"}
	out.send(sseEvent{Type: "tool", Tool: &tool})
	if !sleep(220 * time.Millisecond) {
		return nil
	}
	tool.Status = "done"
	tool.MS = 220
	tool.Output = "未设置（mock 模式）"
	out.send(sseEvent{Type: "tool", Tool: &tool})

	runes := []rune(mockReply)
	for i := 0; i < len(runes); i += 8 {
		end := i + 8
		if end > len(runes) {
			end = len(runes)
		}
		out.send(sseEvent{Type: "text", Delta: string(runes[i:end])})
		if !sleep(18 * time.Millisecond) {
			return nil
		}
	}
	out.send(sseEvent{Type: "done", Usage: &usage{Mock: true}})
	return nil
}

type upstreamError struct {
	status int
	code   string
	detail string
}

func (e *upstreamError) Error() string {
	if e.detail != "" {
		return e.detail
	}
	switch e.status {
	case http.StatusUnauthorized:
		return "模型服务拒绝了密钥（401），检查 GQY_API_KEY"
	case http.StatusForbidden:
		return "模型服务拒绝访问（403），检查密钥权限或模型名"
	case http.StatusNotFound:
		return "模型或接口不存在（404），检查 GQY_API_BASE / GQY_MODEL"
	case http.StatusTooManyRequests:
		return "模型服务限流（429），稍后再试"
	}
	if e.status >= 500 {
		return fmt.Sprintf("模型服务错误（%d）", e.status)
	}
	return fmt.Sprintf("模型服务返回 %d", e.status)
}

func (a *app) upstreamStream(ctx context.Context, req chatRequest, messages []chatMessage, out *sseWriter) error {
	model := req.Model
	if model == "" {
		model = a.cfg.model
	}
	temperature := 0.4
	switch req.Effort {
	case "High":
		temperature = 0.6
	case "Low":
		temperature = 0.2
	}
	payload, err := json.Marshal(map[string]any{
		"model":          model,
		"messages":       messages,
		"stream":         true,
		"stream_options": map[string]bool{"include_usage": true},
		"temperature":    temperature,
	})
	if err != nil {
		return err
	}

	upReq, err := http.NewRequestWithContext(ctx, http.MethodPost, a.cfg.apiBase+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	upReq.Header.Set("Content-Type", "application/json")
	upReq.Header.Set("Authorization", "Bearer "+a.cfg.apiKey)
	upReq.Header.Set("Accept", "text/event-stream")
	a.setUserAgent(upReq)

	resp, err := a.client.Do(upReq)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errors.New("连不上模型服务，检查 GQY_API_BASE 与网络")
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		detail, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		if resp.StatusCode == http.StatusBadRequest && strings.Contains(string(detail), "/v1/responses") {
			return &upstreamError{status: resp.StatusCode, code: "need_responses"}
		}
		return &upstreamError{status: resp.StatusCode, code: fmt.Sprintf("upstream_%d", resp.StatusCode)}
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 64<<10), 1<<20)

	var finalUsage *usage
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" || payload == "[DONE]" {
			continue
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content          string `json:"content"`
					ReasoningContent string `json:"reasoning_content"`
				} `json:"delta"`
			} `json:"choices"`
			Usage *usage `json:"usage"`
		}
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			continue
		}
		if chunk.Usage != nil {
			finalUsage = chunk.Usage
		}
		for _, choice := range chunk.Choices {
			if choice.Delta.ReasoningContent != "" {
				out.send(sseEvent{Type: "thinking", Delta: choice.Delta.ReasoningContent})
			}
			if choice.Delta.Content != "" {
				out.send(sseEvent{Type: "text", Delta: choice.Delta.Content})
			}
		}
	}
	if err := scanner.Err(); err != nil && ctx.Err() == nil {
		return errors.New("读取上游流失败：" + err.Error())
	}
	out.send(sseEvent{Type: "done", Usage: finalUsage})
	return nil
}

/* ---------------- Responses API（OpenAI 新协议，推理模型常用） ---------------- */

func (a *app) setUserAgent(req *http.Request) {
	if a.cfg.apiUA != "" {
		req.Header.Set("User-Agent", a.cfg.apiUA)
	}
}

func (a *app) responsesStream(ctx context.Context, req chatRequest, messages []chatMessage, out *sseWriter) error {
	model := req.Model
	if model == "" {
		model = a.cfg.model
	}
	input := make([]map[string]string, 0, len(messages))
	instructions := ""
	for _, m := range messages {
		if m.Role == "system" {
			instructions = strings.TrimSpace(instructions + "\n" + m.Content)
			continue
		}
		input = append(input, map[string]string{"role": m.Role, "content": m.Content})
	}

	effort := "medium"
	switch req.Effort {
	case "High":
		effort = "high"
	case "Low":
		effort = "low"
	}
	body := map[string]any{
		"model":  model,
		"input":  input,
		"stream": true,
		"store":  false,
		"reasoning": map[string]any{
			"effort":  effort,
			"summary": "auto",
		},
	}
	if instructions != "" {
		body["instructions"] = instructions
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}

	upReq, err := http.NewRequestWithContext(ctx, http.MethodPost, a.cfg.apiBase+"/responses", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	upReq.Header.Set("Content-Type", "application/json")
	upReq.Header.Set("Authorization", "Bearer "+a.cfg.apiKey)
	upReq.Header.Set("Accept", "text/event-stream")
	a.setUserAgent(upReq)

	resp, err := a.client.Do(upReq)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errors.New("连不上模型服务，检查 GQY_API_BASE 与网络")
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		detail, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		var apiErr struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal(detail, &apiErr)
		if apiErr.Error.Message != "" {
			return &upstreamError{status: resp.StatusCode, code: fmt.Sprintf("upstream_%d", resp.StatusCode), detail: apiErr.Error.Message}
		}
		return &upstreamError{status: resp.StatusCode, code: fmt.Sprintf("upstream_%d", resp.StatusCode)}
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 64<<10), 4<<20)

	var finalUsage *usage
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" || payload == "[DONE]" {
			continue
		}
		var event struct {
			Type  string `json:"type"`
			Delta string `json:"delta"`
			Error *struct {
				Message string `json:"message"`
				Code    string `json:"code"`
			} `json:"error"`
			Response *struct {
				Usage *struct {
					InputTokens  int `json:"input_tokens"`
					OutputTokens int `json:"output_tokens"`
					TotalTokens  int `json:"total_tokens"`
				} `json:"usage"`
			} `json:"response"`
		}
		if err := json.Unmarshal([]byte(payload), &event); err != nil {
			continue
		}
		switch event.Type {
		case "response.output_text.delta":
			if event.Delta != "" {
				out.send(sseEvent{Type: "text", Delta: event.Delta})
			}
		case "response.reasoning_summary_text.delta", "response.reasoning_text.delta":
			if event.Delta != "" {
				out.send(sseEvent{Type: "thinking", Delta: event.Delta})
			}
		case "response.completed":
			if event.Response != nil && event.Response.Usage != nil {
				finalUsage = &usage{
					PromptTokens:     event.Response.Usage.InputTokens,
					CompletionTokens: event.Response.Usage.OutputTokens,
					TotalTokens:      event.Response.Usage.TotalTokens,
				}
			}
		case "response.failed", "error":
			message := "上游返回错误"
			if event.Error != nil && event.Error.Message != "" {
				message = event.Error.Message
			}
			return &upstreamError{status: http.StatusBadGateway, code: "upstream_error", detail: message}
		}
	}
	if err := scanner.Err(); err != nil && ctx.Err() == nil {
		return errors.New("读取上游流失败：" + err.Error())
	}
	out.send(sseEvent{Type: "done", Usage: finalUsage})
	return nil
}

/* ---------------- 会话落盘 ---------------- */

type sessionPayload struct {
	ID        string            `json:"id"`
	Title     string            `json:"title"`
	Workspace string            `json:"workspace"`
	UpdatedAt int64             `json:"updatedAt"`
	Messages  []json.RawMessage `json:"messages"`
}

type storedMessage struct {
	Role     string          `json:"role"`
	TS       int64           `json:"ts"`
	Text     string          `json:"text"`
	Thinking string          `json:"thinking"`
	Tools    []storedTool    `json:"tools"`
	Artifact *storedArtifact `json:"artifact"`
}

type storedTool struct {
	Name   string     `json:"name"`
	Status string     `json:"status"`
	MS     int64      `json:"ms"`
	Args   [][]string `json:"args"`
	Output string     `json:"output"`
}

type storedArtifact struct {
	Title   string `json:"title"`
	Content string `json:"content"`
}

var (
	sessionIDRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	unsafeName  = regexp.MustCompile(`[^A-Za-z0-9_.-]+`)
	secretKey   = regexp.MustCompile(`(?i)(key|token|secret|authorization|password)`)
)

func (a *app) sessionsDir() string {
	return filepath.Join(a.cfg.home, "sessions")
}

func (a *app) handleSessionPut(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !sessionIDRe.MatchString(id) {
		writeErr(w, http.StatusBadRequest, "bad_session_id", "会话 id 不合法")
		return
	}
	var payload sessionPayload
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodySession))
	if err := dec.Decode(&payload); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_json", "请求体不是合法 JSON")
		return
	}
	if payload.ID != "" && payload.ID != id {
		writeErr(w, http.StatusBadRequest, "id_mismatch", "路径与会话 id 不一致")
		return
	}
	dir, err := a.persistSession(id, payload)
	if err != nil {
		log.Printf("[session] %v", err)
		writeErr(w, http.StatusInternalServerError, "write_failed", "写入会话失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "dir": dir})
}

func (a *app) handleSessionGet(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !sessionIDRe.MatchString(id) {
		writeErr(w, http.StatusBadRequest, "bad_session_id", "会话 id 不合法")
		return
	}
	raw, err := os.ReadFile(filepath.Join(a.sessionsDir(), id, "io", "context", "messages.jsonl"))
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

func (a *app) handleSessionDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !sessionIDRe.MatchString(id) {
		writeErr(w, http.StatusBadRequest, "bad_session_id", "会话 id 不合法")
		return
	}
	if err := os.RemoveAll(filepath.Join(a.sessionsDir(), id)); err != nil {
		writeErr(w, http.StatusInternalServerError, "delete_failed", "删除失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (a *app) handleSessionList(w http.ResponseWriter, r *http.Request) {
	entries, err := os.ReadDir(a.sessionsDir())
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"sessions": []any{}, "home": a.cfg.home})
		return
	}
	type manifest struct {
		SessionID string `json:"session_id"`
		Title     string `json:"title"`
		Workspace string `json:"workspace"`
		CreatedAt string `json:"created_at"`
		UpdatedAt string `json:"updated_at"`
		Messages  int    `json:"messages"`
	}
	list := make([]manifest, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() || !sessionIDRe.MatchString(entry.Name()) {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(a.sessionsDir(), entry.Name(), "manifest.json"))
		if err != nil {
			continue
		}
		var m manifest
		if json.Unmarshal(raw, &m) == nil {
			list = append(list, m)
		}
	}
	sort.Slice(list, func(i, j int) bool { return list[i].UpdatedAt > list[j].UpdatedAt })
	if len(list) > 200 {
		list = list[:200]
	}
	writeJSON(w, http.StatusOK, map[string]any{"sessions": list, "home": a.cfg.home})
}

func (a *app) persistSession(id string, payload sessionPayload) (string, error) {
	dir := filepath.Join(a.sessionsDir(), id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return dir, err
	}

	messages := payload.Messages
	if len(messages) > maxSessionMsgs {
		messages = messages[len(messages)-maxSessionMsgs:]
	}

	createdAt := time.Now().UTC().Format(time.RFC3339)
	if raw, err := os.ReadFile(filepath.Join(dir, "manifest.json")); err == nil {
		var old struct {
			CreatedAt string `json:"created_at"`
		}
		if json.Unmarshal(raw, &old) == nil && old.CreatedAt != "" {
			createdAt = old.CreatedAt
		}
	}

	decoded := make([]storedMessage, 0, len(messages))
	artifactTitles := make([]string, 0, 8)
	for _, raw := range messages {
		var m storedMessage
		if json.Unmarshal(raw, &m) == nil {
			decoded = append(decoded, m)
			if m.Artifact != nil && m.Artifact.Title != "" {
				artifactTitles = append(artifactTitles, m.Artifact.Title)
			}
		}
	}

	manifest := map[string]any{
		"session_id": id,
		"workspace":  payload.Workspace,
		"title":      payload.Title,
		"created_at": createdAt,
		"updated_at": time.Now().UTC().Format(time.RFC3339),
		"messages":   len(messages),
		"artifacts":  artifactTitles,
		"cleanup_policy": map[string]int{
			"session_tmp_ttl_days": 7,
			"logs_ttl_days":        30,
		},
	}
	manifestJSON, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return dir, err
	}
	if err := writeFileAtomic(filepath.Join(dir, "manifest.json"), append(manifestJSON, '\n')); err != nil {
		return dir, err
	}

	var context strings.Builder
	fmt.Fprintf(&context, "# %s\n\n- session: `%s`\n- 更新时间: %s\n\n", fallback(payload.Title, "会话"), id, manifest["updated_at"])
	for i, m := range decoded {
		role := "顾清影"
		if m.Role == "user" {
			role = "用户"
		}
		fmt.Fprintf(&context, "## %d. %s\n\n%s\n\n", i+1, role, truncateRunes(m.Text, 8000))
		if m.Artifact != nil {
			fmt.Fprintf(&context, "产物: `%s`\n\n", m.Artifact.Title)
		}
	}
	if err := writeFileAtomic(filepath.Join(dir, "io", "context", "context.md"), []byte(context.String())); err != nil {
		return dir, err
	}

	var jsonl strings.Builder
	for _, raw := range messages {
		if len(bytes.TrimSpace(raw)) == 0 {
			continue
		}
		jsonl.Write(bytes.TrimSpace(raw))
		jsonl.WriteByte('\n')
	}
	if err := writeFileAtomic(filepath.Join(dir, "io", "context", "messages.jsonl"), []byte(jsonl.String())); err != nil {
		return dir, err
	}

	// 产物
	used := map[string]bool{}
	artifactNames := make([]string, 0, 8)
	for _, m := range decoded {
		if m.Artifact == nil || m.Artifact.Content == "" || len(m.Artifact.Content) > maxArtifact {
			continue
		}
		name := safeName(m.Artifact.Title, fmt.Sprintf("artifact-%d", len(artifactNames)))
		if used[name] {
			name = fmt.Sprintf("%d-%s", len(artifactNames), name)
		}
		used[name] = true
		if err := writeFileAtomic(filepath.Join(dir, "io", "output", "artifacts", name), []byte(m.Artifact.Content)); err != nil {
			return dir, err
		}
		artifactNames = append(artifactNames, name)
	}
	indexJSON, _ := json.MarshalIndent(map[string]any{"artifacts": artifactNames}, "", "  ")
	if err := writeFileAtomic(filepath.Join(dir, "io", "output", "index.json"), append(indexJSON, '\n')); err != nil {
		return dir, err
	}

	// 工具日志：按消息日期分文件，每次同步重写（幂等）
	byDay := map[string][]string{}
	for _, m := range decoded {
		day := time.UnixMilli(m.TS).UTC().Format("2006-01-02")
		if m.TS == 0 {
			day = time.Now().UTC().Format("2006-01-02")
		}
		for _, tool := range m.Tools {
			status := "error"
			if tool.Status == "done" {
				status = "done"
			}
			inputs := make([]string, 0, len(tool.Args))
			for _, pair := range tool.Args {
				if len(pair) < 2 || secretKey.MatchString(pair[0]) {
					continue
				}
				inputs = append(inputs, pair[0]+"="+truncateRunes(pair[1], 120))
			}
			line, err := json.Marshal(map[string]any{
				"timestamp":      time.UnixMilli(m.TS).UTC().Format(time.RFC3339),
				"tool_name":      fallback(tool.Name, "tool"),
				"status":         status,
				"duration_ms":    tool.MS,
				"input_summary":  strings.Join(inputs, " "),
				"output_summary": truncateRunes(tool.Output, 300),
			})
			if err == nil {
				byDay[day] = append(byDay[day], string(line))
			}
		}
	}
	days := make([]string, 0, len(byDay))
	for day := range byDay {
		days = append(days, day)
	}
	sort.Strings(days)
	for _, day := range days {
		body := strings.Join(byDay[day], "\n") + "\n"
		if err := writeFileAtomic(filepath.Join(dir, "logs", "tools", day+".jsonl"), []byte(body)); err != nil {
			return dir, err
		}
	}

	return dir, nil
}

func writeFileAtomic(file string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		return err
	}
	tmp := fmt.Sprintf("%s.%d.%d.tmp", file, os.Getpid(), rand.Int63())
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, file); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func safeName(name, fallbackName string) string {
	clean := strings.TrimLeft(unsafeName.ReplaceAllString(name, "_"), "._")
	if clean == "" {
		clean = fallbackName
	}
	if runes := []rune(clean); len(runes) > 80 {
		clean = string(runes[:80])
	}
	return clean
}

func truncateRunes(s string, limit int) string {
	runes := []rune(s)
	if len(runes) <= limit {
		return s
	}
	return string(runes[:limit])
}

func fallback(value, def string) string {
	if strings.TrimSpace(value) == "" {
		return def
	}
	return value
}
