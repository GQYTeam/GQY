// 包 httpapi：HTTP 路由与处理器，聚合 config/catalog/provider/session。
package httpapi

import (
	"context"
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
	"sort"
	"strconv"
	"strings"
	"time"

	"gqy/internal/agent"
	"gqy/internal/catalog"
	"gqy/internal/config"
	"gqy/internal/gateway"
	"gqy/internal/github"
	"gqy/internal/plugin"
	"gqy/internal/provider"
	"gqy/internal/session"
	"gqy/internal/tool"
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
	Gateway  *gateway.Router
	Agent    *agent.Loop
	GitHub   *github.Client
	Tools    *tool.Registry
	Plugins  *plugin.Manager
}

func New(root string, cfg config.Config, client *http.Client) *Server {
	upstream := &provider.Upstream{
		APIBase: cfg.APIBase,
		APIKey:  cfg.APIKey,
		APIUA:   cfg.APIUA,
		Model:   cfg.Model,
		Wire:    cfg.Wire,
		Mock:    cfg.Mock,
		Client:  client,
	}
	// 工具权限沙箱：文件工具只允许访问 workspace / $GQY_HOME/sessions / GQY_TOOL_ROOTS；
	// GitHub token 即使在允许根内也显式拒绝。
	policy := tool.Policy{
		Workspace:      cfg.Workspace,
		Roots:          cfg.ToolRoots,
		Denied:         []string{filepath.Join(cfg.Home, "github_token.json")},
		ShellTimeout:   cfg.ShellTimeout,
		MaxOutput:      cfg.ToolMaxOutput,
		ShellAllowlist: cfg.ShellAllowlist,
	}
	registry := tool.NewRegistry()
	_ = registry.Register(tool.Read{Policy: policy})
	_ = registry.Register(tool.Edit{Policy: policy})
	_ = registry.Register(tool.Shell{Policy: policy})
	_ = registry.Register(tool.ListDir{Policy: policy})
	_ = registry.Register(tool.WebSearch{})
	_ = registry.Register(tool.AskUser{})
	// 外部工具：provider_usage（查余额）
	_ = registry.Register(tool.ProviderUsage{
		APIBase:    cfg.APIBase,
		APIKey:     cfg.APIKey,
		Client:     client,
		LowBalance: cfg.LowBalance,
	})

	router := gateway.New(cfg, client)
	plugins := plugin.NewManager(cfg.Home, registry)
	_ = plugins.Discover()

	return &Server{
		Root:     root,
		Cfg:      cfg,
		Client:   client,
		Catalog:  catalog.New(client),
		Session:  session.New(cfg.Home),
		Upstream: upstream,
		Gateway:  router,
		Agent:    &agent.Loop{Upstream: router, Registry: registry, IncludeExternal: cfg.EnableExternal, ToolTimeout: cfg.ToolTimeout},
		Tools:    registry,
		Plugins:  plugins,
		GitHub: &github.Client{
			ClientID:     cfg.GitHubClientID,
			ClientSecret: cfg.GitHubClientSecret,
			Home:         cfg.Home,
			HTTP:         client,
		},
	}
}

// Close 关闭插件进程与网关日志。
func (s *Server) Close() {
	if s.Plugins != nil {
		s.Plugins.Close()
	}
	if s.Gateway != nil {
		_ = s.Gateway.Close()
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", s.handleHealth)
	mux.HandleFunc("GET /api/models", s.handleModels)
	mux.HandleFunc("GET /api/usage", s.handleUsage)
	mux.HandleFunc("POST /api/chat", s.handleChat)
	mux.HandleFunc("GET /api/gateway/status", s.handleGatewayStatus)
	mux.HandleFunc("GET /api/plugins", s.handlePluginList)
	mux.HandleFunc("POST /api/plugins/reload", s.handlePluginReload)
	mux.HandleFunc("POST /api/plugins/{id}/load", s.handlePluginLoad)
	mux.HandleFunc("POST /api/plugins/{id}/unload", s.handlePluginUnload)
	mux.HandleFunc("POST /api/plugins/{id}/call", s.handlePluginCall)
	mux.HandleFunc("GET /api/tools", s.handleToolList)
	mux.HandleFunc("POST /api/tools/external", s.handleToolExternal)
	mux.HandleFunc("GET /v1/models", s.handleV1Models)
	mux.HandleFunc("POST /v1/chat/completions", s.handleV1Chat)
	mux.HandleFunc("GET /api/providers", s.handleProviders)
	mux.HandleFunc("POST /api/providers", s.handleProviderUpsert)
	mux.HandleFunc("DELETE /api/providers/{id}", s.handleProviderDelete)
	mux.HandleFunc("POST /api/routing/groups", s.handleGroupUpsert)
	mux.HandleFunc("DELETE /api/routing/groups/{id}", s.handleGroupDelete)
	mux.HandleFunc("PUT /api/routing/default", s.handleDefaultSet)
	mux.HandleFunc("GET /api/github/auth", s.handleGitHubAuth)
	mux.HandleFunc("GET /api/github/callback", s.handleGitHubCallback)
	mux.HandleFunc("GET /api/github/me", s.handleGitHubMe)
	mux.HandleFunc("DELETE /api/github", s.handleGitHubDisconnect)
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
	mux.HandleFunc("/api/gateway/status", allow("GET"))
	mux.HandleFunc("/api/plugins", allow("GET"))
	mux.HandleFunc("/api/plugins/reload", allow("POST"))
	mux.HandleFunc("/api/plugins/{id}/load", allow("POST"))
	mux.HandleFunc("/api/plugins/{id}/unload", allow("POST"))
	mux.HandleFunc("/api/plugins/{id}/call", allow("POST"))
	mux.HandleFunc("/api/tools", allow("GET"))
	mux.HandleFunc("/api/tools/external", allow("POST"))
	mux.HandleFunc("/v1/models", allow("GET"))
	mux.HandleFunc("/v1/chat/completions", allow("POST"))
	mux.HandleFunc("/api/providers", allow("GET, POST"))
	mux.HandleFunc("/api/providers/{id}", allow("DELETE"))
	mux.HandleFunc("/api/routing/groups", allow("POST"))
	mux.HandleFunc("/api/routing/groups/{id}", allow("DELETE"))
	mux.HandleFunc("/api/routing/default", allow("PUT"))
	mux.HandleFunc("/api/github/auth", allow("GET"))
	mux.HandleFunc("/api/github/callback", allow("GET"))
	mux.HandleFunc("/api/github/me", allow("GET"))
	mux.HandleFunc("/api/github", allow("DELETE"))
	mux.HandleFunc("/api/sessions", allow("GET"))
	mux.HandleFunc("/api/sessions/{id}", allow("GET, PUT, DELETE"))
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeErr(w, http.StatusNotFound, "not_found", "接口不存在")
	})
	mux.HandleFunc("/", s.handleStatic)
	return s.withRequestID(s.logRequests(mux))
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	kind := s.Cfg.Provider()
	if kind == "none" && s.Gateway.Available() {
		kind = "openai-compatible"
	}
	model := s.Cfg.Model
	if s.Gateway.DefaultModel() != "" {
		model = s.Gateway.DefaultModel()
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":        true,
		"provider":  kind,
		"model":     model,
		"home":      s.Cfg.Home,
		"version":   apiVersion,
		"providers": len(s.Gateway.Status().Providers),
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
	// mock 模式：复用旧 mock 流（Upstream.Handle 自己解析 body，不走 agent loop）
	if s.Cfg.Mock {
		s.Upstream.Handle(w, r, provider.Request{})
		return
	}

	// 解析请求体
	var req provider.Request
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10))
	if err := dec.Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_json", "请求体不是合法 JSON")
		return
	}
	messages := normalizeMessages(req.Messages)
	if len(messages) == 0 {
		writeErr(w, http.StatusBadRequest, "messages_required", "messages 不能为空")
		return
	}
	if !s.Gateway.Available() {
		writeErr(w, http.StatusServiceUnavailable, "no_provider", "服务端未配置可用的 provider 密钥")
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

	ctx, cancel := context.WithCancel(gateway.WithSession(r.Context(), req.Session))
	defer cancel()
	out := provider.NewWriter(w, flusher)

	// 工具回调 → SSE tool 事件
	s.Agent.OnTool = func(tr agent.ToolResult) {
		status := "done"
		if tr.Error != nil {
			status = "error"
		}
		out.Send(provider.Event{
			Type: "tool",
			Tool: &provider.ToolEvent{
				Name:   tr.Name,
				Detail: tr.Arguments,
				Status: status,
				MS:     tr.Duration.Milliseconds(),
				Output: tr.Output,
			},
		})
	}

	final, usage, ask, err := s.Agent.Run(ctx, messages, req.Model, req.Effort)
	if err != nil && ctx.Err() == nil {
		out.Send(provider.Event{Type: "error", Code: "agent_failed", Message: err.Error()})
		out.Send(provider.Event{Type: "end"})
		return
	}

	// ask_user：发 question 事件让前端渲染提问卡片，等待用户回答（下一轮请求）
	if ask != nil {
		out.Send(provider.Event{Type: "question", Question: &provider.Question{
			Text:    ask.Question,
			Options: ask.Options,
		}})
		out.Send(provider.Event{Type: "end"})
		return
	}

	// 最终答案：分块吐 text
	if final.Content != "" {
		runes := []rune(final.Content)
		for i := 0; i < len(runes); i += 8 {
			end := i + 8
			if end > len(runes) {
				end = len(runes)
			}
			out.Send(provider.Event{Type: "text", Delta: string(runes[i:end])})
			if ctx.Err() != nil {
				break
			}
		}
	}
	out.Send(provider.Event{Type: "done", Usage: usage})
	out.Send(provider.Event{Type: "end"})
}

// normalizeMessages 截断并规范化消息（与 provider 内部逻辑一致）。
func normalizeMessages(in []provider.Message) []provider.Message {
	const maxMessages = 200
	const maxChars = 200_000
	if len(in) == 0 {
		return nil
	}
	if len(in) > maxMessages {
		in = in[len(in)-maxMessages:]
	}
	kept := make([]provider.Message, 0, len(in))
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
		kept = append(kept, provider.Message{Role: role, Content: content})
	}
	return kept
}

/* ---------------- 供应商列表（providers.json） ---------------- */

func (s *Server) handleProviders(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"providers": s.Gateway.ProviderViews()})
}

func (s *Server) handleProviderUpsert(w http.ResponseWriter, r *http.Request) {
	var p gateway.Provider
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	if err := dec.Decode(&p); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_json", "请求体不是合法 JSON")
		return
	}
	if err := s.Gateway.UpsertProvider(p); err != nil {
		writeErr(w, http.StatusBadRequest, "provider_invalid", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "providers": s.Gateway.ProviderViews()})
}

func (s *Server) handleProviderDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.Gateway.DeleteProvider(r.PathValue("id")); err != nil {
		writeErr(w, http.StatusBadRequest, "provider_delete_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "providers": s.Gateway.ProviderViews()})
}

func (s *Server) handleGroupUpsert(w http.ResponseWriter, r *http.Request) {
	var g gateway.Group
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	if err := dec.Decode(&g); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_json", "请求体不是合法 JSON")
		return
	}
	if err := s.Gateway.UpsertGroup(g); err != nil {
		writeErr(w, http.StatusBadRequest, "group_invalid", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "status": s.Gateway.Status()})
}

func (s *Server) handleGroupDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.Gateway.DeleteGroup(r.PathValue("id")); err != nil {
		writeErr(w, http.StatusBadRequest, "group_delete_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "status": s.Gateway.Status()})
}

func (s *Server) handleDefaultSet(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Provider string `json:"provider"`
		Model    string `json:"model"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10))
	if err := dec.Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_json", "请求体不是合法 JSON")
		return
	}
	if err := s.Gateway.SetDefault(req.Provider, req.Model); err != nil {
		writeErr(w, http.StatusBadRequest, "default_invalid", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "status": s.Gateway.Status()})
}

/* ---------------- 插件与工具 ---------------- */

func (s *Server) handlePluginList(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"plugins": s.Plugins.List(),
		"dir":     s.Plugins.Dir(),
	})
}

func (s *Server) handlePluginReload(w http.ResponseWriter, r *http.Request) {
	errs := s.Plugins.Reload()
	msgs := make([]string, 0, len(errs))
	for _, err := range errs {
		msgs = append(msgs, err.Error())
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "errors": msgs, "plugins": s.Plugins.List()})
}

func (s *Server) handlePluginLoad(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.Plugins.Load(r.Context(), id); err != nil {
		writePluginErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "plugins": s.Plugins.List()})
}

func (s *Server) handlePluginUnload(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.Plugins.Unload(r.Context(), id); err != nil {
		writePluginErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "plugins": s.Plugins.List()})
}

func (s *Server) handlePluginCall(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req struct {
		Action    string         `json:"action"`
		Arguments map[string]any `json:"arguments"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	if err := dec.Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_json", "请求体不是合法 JSON")
		return
	}
	if req.Action == "" {
		writeErr(w, http.StatusBadRequest, "action_required", "action 不能为空")
		return
	}
	start := time.Now()
	out, err := s.Plugins.Call(r.Context(), id, req.Action, req.Arguments)
	if err != nil {
		writePluginErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "content": out, "durationMs": time.Since(start).Milliseconds(),
	})
}

func writePluginErr(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	code := "plugin_error"
	switch {
	case errors.Is(err, plugin.ErrNotFound):
		status, code = http.StatusNotFound, "plugin_not_found"
	case errors.Is(err, plugin.ErrNotReady):
		status, code = http.StatusConflict, "plugin_not_ready"
	case errors.Is(err, plugin.ErrExited):
		status, code = http.StatusConflict, "plugin_exited"
	case errors.Is(err, context.DeadlineExceeded):
		status, code = http.StatusGatewayTimeout, "plugin_timeout"
	}
	writeErr(w, status, code, err.Error())
}

type toolInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Kind        string `json:"kind"`
}

func (s *Server) handleToolList(w http.ResponseWriter, r *http.Request) {
	list := s.Tools.List()
	out := make([]toolInfo, 0, len(list))
	for _, t := range list {
		kind := "external"
		if t.Kind() == tool.KindCore {
			kind = "core"
		}
		out = append(out, toolInfo{Name: t.Name(), Description: t.Description(), Kind: kind})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	writeJSON(w, http.StatusOK, map[string]any{
		"tools":    out,
		"external": s.Agent.ExternalEnabled(),
	})
}

func (s *Server) handleToolExternal(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Enabled bool `json:"enabled"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10))
	if err := dec.Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_json", "请求体不是合法 JSON")
		return
	}
	s.Agent.SetExternal(req.Enabled)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "external": req.Enabled})
}

/* ---------------- 本地网关 ---------------- */

func (s *Server) handleGatewayStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.Gateway.Status())
}

func (s *Server) handleV1Models(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"object": "list",
		"data":   s.Gateway.Models(),
	})
}

type v1ChatRequest struct {
	Model    string             `json:"model"`
	Messages []provider.Message `json:"messages"`
	Tools    []any              `json:"tools,omitempty"`
	Stream   bool               `json:"stream"`
	Effort   string             `json:"reasoning_effort,omitempty"`
}

func (s *Server) handleV1Chat(w http.ResponseWriter, r *http.Request) {
	var req v1ChatRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20))
	if err := dec.Decode(&req); err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_json", "请求体不是合法 JSON")
		return
	}
	if len(req.Messages) == 0 {
		writeOpenAIError(w, http.StatusBadRequest, "messages_required", "messages 不能为空")
		return
	}
	if !s.Gateway.Available() {
		writeOpenAIError(w, http.StatusServiceUnavailable, "no_provider", "没有可用的 provider")
		return
	}

	session := r.Header.Get("X-GQY-Session")
	ctx := gateway.WithSession(r.Context(), session)
	msg, usage, err := s.Gateway.Complete(ctx, provider.Request{
		Messages: req.Messages,
		Model:    req.Model,
		Effort:   req.Effort,
		Tools:    req.Tools,
	})
	if err != nil {
		status := http.StatusBadGateway
		code := "gateway_error"
		var ue *provider.UpstreamError
		if errors.As(err, &ue) {
			status = ue.Status
			if status < 400 || status > 599 {
				status = http.StatusBadGateway
			}
			code = ue.Code
		}
		writeOpenAIError(w, status, code, err.Error())
		return
	}

	model := req.Model
	if model == "" {
		model = s.Gateway.DefaultModel()
	}
	id := "chatcmpl-" + newRequestID()
	created := time.Now().Unix()

	if !req.Stream {
		msg.Role = "assistant"
		writeJSON(w, http.StatusOK, map[string]any{
			"id":      id,
			"object":  "chat.completion",
			"created": created,
			"model":   model,
			"choices": []any{map[string]any{
				"index":         0,
				"message":       msg,
				"finish_reason": finishReason(msg),
			}},
			"usage": usage,
		})
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeOpenAIError(w, http.StatusInternalServerError, "no_flusher", "当前连接不支持流式")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	send := func(v any) {
		b, _ := json.Marshal(v)
		_, _ = fmt.Fprintf(w, "data: %s\n\n", b)
		flusher.Flush()
	}
	chunk := func(delta map[string]any, finish any) map[string]any {
		return map[string]any{
			"id": id, "object": "chat.completion.chunk", "created": created, "model": model,
			"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}},
		}
	}

	runes := []rune(msg.Content)
	for i := 0; i < len(runes); i += 24 {
		end := i + 24
		if end > len(runes) {
			end = len(runes)
		}
		send(chunk(map[string]any{"content": string(runes[i:end])}, nil))
	}
	if len(msg.ToolCalls) > 0 {
		send(chunk(map[string]any{"tool_calls": msg.ToolCalls}, nil))
	}
	send(chunk(map[string]any{}, finishReason(msg)))
	if usage != nil {
		send(map[string]any{
			"id": id, "object": "chat.completion.chunk", "created": created, "model": model,
			"choices": []any{}, "usage": usage,
		})
	}
	_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	flusher.Flush()
}

func finishReason(msg provider.Message) string {
	if len(msg.ToolCalls) > 0 {
		return "tool_calls"
	}
	return "stop"
}

func writeOpenAIError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]any{"message": message, "type": "gqy_error", "code": code},
	})
}

/* ---------------- GitHub 只读账户 ---------------- */

// redirectURI 计算 OAuth 回调地址。
func (s *Server) redirectURI(r *http.Request) string {
	if s.Cfg.GitHubOAuthRedirect != "" {
		return s.Cfg.GitHubOAuthRedirect
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host + "/api/github/callback"
}

func (s *Server) handleGitHubAuth(w http.ResponseWriter, r *http.Request) {
	if s.GitHub == nil || s.GitHub.ClientID == "" {
		writeErr(w, http.StatusServiceUnavailable, "github_not_configured", "未配置 GITHUB_CLIENT_ID")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"url": s.GitHub.AuthURL(s.redirectURI(r)),
	})
}

func (s *Server) handleGitHubCallback(w http.ResponseWriter, r *http.Request) {
	if s.GitHub == nil || s.GitHub.ClientID == "" {
		writeErr(w, http.StatusServiceUnavailable, "github_not_configured", "未配置 GITHUB_CLIENT_ID")
		return
	}
	code := r.URL.Query().Get("code")
	if code == "" {
		writeErr(w, http.StatusBadRequest, "no_code", "缺少授权码")
		return
	}
	acct, err := s.GitHub.Exchange(r.Context(), code, s.redirectURI(r))
	if err != nil {
		writeErr(w, http.StatusBadGateway, "github_exchange_failed", err.Error())
		return
	}
	// 回调成功后跳回前端（携带成功标记）
	http.Redirect(w, r, "/?github=connected&login="+acct.Login, http.StatusFound)
}

func (s *Server) handleGitHubMe(w http.ResponseWriter, r *http.Request) {
	if s.GitHub == nil {
		writeErr(w, http.StatusServiceUnavailable, "github_not_configured", "GitHub 插件未配置")
		return
	}
	acct, err := s.GitHub.Me()
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"connected": false})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"connected": true, "account": acct})
}

func (s *Server) handleGitHubDisconnect(w http.ResponseWriter, r *http.Request) {
	if s.GitHub == nil {
		writeErr(w, http.StatusServiceUnavailable, "github_not_configured", "GitHub 插件未配置")
		return
	}
	_ = s.GitHub.Disconnect()
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
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
