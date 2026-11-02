// Package httpapi 提供 HTTP 路由、鉴权、限流与协议端点。
//
// 设计依据：docs/designs/08-鉴权与配额.md（A1–A4、A9）、
// docs/designs/02-网关与路由.md（R8）。
package httpapi

import (
	"log/slog"
	"net/http"
	"sync"
	"time"

	"gqy/internal/agent"
	"gqy/internal/config"
	"gqy/internal/gateway"
	"gqy/internal/goal"
	"gqy/internal/plugin"
)

// Server 装配全部 HTTP 路由。
type Server struct {
	mgr       *config.Manager
	gw        *gateway.Router
	agentLoop *agent.Loop
	plugins   *plugin.Manager
	goals     *goal.Manager
	log       *slog.Logger
	mux       *http.ServeMux
	limiter   *rateLimiter
	started   time.Time
	now       func() time.Time
	webDir    string

	// runMu/running 记录正在跑 loop 的会话，用于主线抢占判断（013）。
	runMu   sync.Mutex
	running map[string]bool

	// events 是 SSE 广播中心（HR7）：配置/插件/全局模型变化实时推送。
	events *eventHub
}

// New 构造 HTTP 服务。gw 为 nil 时只提供配置与目录端点，聊天端点返回 503。
func New(mgr *config.Manager, gw *gateway.Router, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	s := &Server{
		mgr:     mgr,
		gw:      gw,
		log:     logger,
		limiter: newRateLimiter(),
		started: time.Now(),
		now:     time.Now,
		running: make(map[string]bool),
		events:  newEventHub(),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.HandleFunc("GET /metrics", s.handleMetrics)
	mux.HandleFunc("GET /v1/models", s.handleModels)
	mux.HandleFunc("POST /v1/chat/completions", s.handleChat)
	mux.HandleFunc("POST /v1/messages", s.handleAnthropicMessages)
	mux.HandleFunc("POST /v1beta/models/", s.handleGeminiGenerate)
	mux.HandleFunc("POST /api/chat", s.handleAgentChat)
	mux.HandleFunc("GET /api/plugins", s.handlePlugins)
	mux.HandleFunc("POST /api/plugins/reload", s.handlePluginsReload)
	mux.HandleFunc("POST /api/plugins/install", s.handlePluginInstall)
	mux.HandleFunc("POST /api/plugins/{id}/load", s.handlePluginLoad)
	mux.HandleFunc("POST /api/plugins/{id}/unload", s.handlePluginUnload)
	mux.HandleFunc("POST /api/plugins/{id}/call", s.handlePluginCall)
	mux.HandleFunc("GET /api/plugins/{id}/settings", s.handlePluginSettingsGet)
	mux.HandleFunc("PUT /api/plugins/{id}/settings", s.handlePluginSettingsPut)
	mux.HandleFunc("DELETE /api/plugins/{id}", s.handlePluginDelete)

	// 记忆子系统（MEM1–MEM8、blueprint/memory.md §3）：九个端点。
	mux.HandleFunc("GET /api/memory", s.handleMemoryList)
	mux.HandleFunc("GET /api/memory/{persona}/stats", s.handleMemoryStats)
	mux.HandleFunc("GET /api/memory/{persona}/facts", s.handleMemoryFacts)
	mux.HandleFunc("POST /api/memory/{persona}/facts", s.handleMemoryFactCreate)
	mux.HandleFunc("PATCH /api/memory/{persona}/facts/{id}", s.handleMemoryFactPatch)
	mux.HandleFunc("GET /api/memory/{persona}/episodes", s.handleMemoryEpisodes)
	mux.HandleFunc("GET /api/memory/{persona}/evicted", s.handleMemoryEvicted)
	mux.HandleFunc("GET /api/memory/{persona}/revisions", s.handleMemoryRevisions)
	mux.HandleFunc("POST /api/memory/{persona}/recall", s.handleMemoryRecall)
	mux.HandleFunc("POST /api/goal", s.handleGoalCreate)
	mux.HandleFunc("GET /api/goal", s.handleGoalList)
	mux.HandleFunc("POST /api/goal/{id}/pause", s.handleGoalPause)
	mux.HandleFunc("POST /api/goal/{id}/resume", s.handleGoalResume)
	mux.HandleFunc("POST /api/goal/{id}/queue", s.handleGoalQueue)
	mux.HandleFunc("POST /api/goal/{id}/adopt", s.handleGoalAdopt)
	mux.HandleFunc("POST /api/sessions", s.handleSessionCreate)
	mux.HandleFunc("GET /api/sessions", s.handleSessionList)
	mux.HandleFunc("GET /api/sessions/{id}", s.handleSessionGet)
	mux.HandleFunc("DELETE /api/sessions/{id}", s.handleSessionDelete)
	mux.HandleFunc("GET /api/sessions/{id}/draft", s.handleSessionDraftGet)
	mux.HandleFunc("PUT /api/sessions/{id}/draft", s.handleSessionDraftPut)
	mux.HandleFunc("POST /api/sessions/{id}/uploads", s.handleSessionUpload)
	mux.HandleFunc("POST /api/sessions/{id}/artifacts", s.handleSessionArtifacts)
	mux.HandleFunc("POST /api/auth", s.handleAuth)
	mux.HandleFunc("GET /api/config", s.handleConfig)
	mux.HandleFunc("POST /api/config/reload", s.handleReload)
	mux.HandleFunc("GET /api/gateway/status", s.handleStatus)
	mux.HandleFunc("GET /api/tools", s.handleTools)
	mux.HandleFunc("GET /api/events", s.handleEvents)
	mux.HandleFunc("GET /api/model/active", s.handleActiveModelGet)
	mux.HandleFunc("PUT /api/model/active", s.handleActiveModelPut)
	mux.HandleFunc("GET /api/persona", s.handlePersonaGet)
	mux.HandleFunc("PUT /api/persona", s.handlePersonaPut)
	mux.HandleFunc("GET /", s.handleStatic)
	s.mux = mux
	return s
}

// RefreshTools 按当前快照重建外部（插件）工具表（HR2/HR3）：
// 插件加载/卸载/删除、配置热更新后调用，模型立即感知，无需重启。
func (s *Server) RefreshTools() {
	if s.agentLoop == nil || s.plugins == nil {
		return
	}
	expose := true
	if snap := s.mgr.Current(); snap != nil {
		expose = snap.Settings.ExposeExternalTools
	}
	s.agentLoop.Tools.ResetExternal()
	s.plugins.RegisterTools(s.agentLoop.Tools, expose)
}

// SetAgent 装配 agent loop；/api/chat 在 loop 存在时走工具执行闭环。
func (s *Server) SetAgent(l *agent.Loop) { s.agentLoop = l }

// SetPlugins 装配插件运行时。
func (s *Server) SetPlugins(p *plugin.Manager) { s.plugins = p }

// SetGoals 装配 goal 运行时。
func (s *Server) SetGoals(g *goal.Manager) { s.goals = g }

// Handler 返回带中间件的处理器。
func (s *Server) Handler() http.Handler {
	var h http.Handler = s.mux
	h = s.auth(h)
	h = s.rateLimit(h)
	h = s.securityHeaders(h)
	h = s.accessLog(h)
	h = s.recoverer(h)
	h = s.requestID(h)
	h = s.withSnapshot(h)
	h = s.tenant(h)
	return h
}

// isSessionRunning 报告该会话是否正在跑 loop（013）。
func (s *Server) isSessionRunning(id string) bool {
	s.runMu.Lock()
	defer s.runMu.Unlock()
	return s.running[id]
}

// setSessionRunning 标记会话 loop 运行状态（013）。
func (s *Server) setSessionRunning(id string, running bool) {
	s.runMu.Lock()
	defer s.runMu.Unlock()
	if running {
		s.running[id] = true
		return
	}
	delete(s.running, id)
}
