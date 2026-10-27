// 包 gateway：本地多 provider 网关。
//
// 负责：模型解析、路由组、候选排序、响应开始前的失败转移、健康/冷却、
// 每 provider 并发上限、会话亲和、请求日志。
//
// 设计见 docs/design/Gateway.md。评测网关是另一件事，见 internal/eval。
package gateway

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"gqy/internal/config"
	"gqy/internal/provider"
)

// Provider 是一个 OpenAI 兼容上游。
type Provider struct {
	ID             string   `json:"id"`
	Name           string   `json:"display_name"`
	BaseURL        string   `json:"base_url"`
	Protocol       string   `json:"protocol"`
	DefaultModel   string   `json:"default_model"`
	Models         []string `json:"models"`
	APIKey         string   `json:"api_key"`
	UserAgent      string   `json:"user_agent"`
	MaxConcurrency int      `json:"max_concurrency"`
}

// Group 是一组候选，按 strategy 排序。
type Group struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Strategy string   `json:"strategy"`
	Members  []string `json:"members"`
}

type routingFile struct {
	Default         *Ref    `json:"default"`
	Groups          []Group `json:"groups"`
	MaxAttempts     int     `json:"max_attempts"`
	CooldownSeconds int     `json:"cooldown_seconds"`
	QueueTimeoutMS  int     `json:"queue_timeout_ms"`
}

// Ref 是 provider/model 引用。
type Ref struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
}

type providerState struct {
	sem           chan struct{}
	cooldownUntil time.Time
	lastErr       string
	calls         atomic.Int64
	failures      atomic.Int64
}

// Router 实现 agent.Completer。
type Router struct {
	mu        sync.RWMutex
	providers []*Provider
	byID      map[string]*Provider
	groups    map[string]*Group
	def       Ref

	maxAttempts  int
	cooldown     time.Duration
	queueTimeout time.Duration

	client *http.Client
	home   string

	states      map[string]*providerState
	affinity    map[string]string
	groupCursor map[string]uint64

	logMu   sync.Mutex
	logFile *os.File

	now func() time.Time
	seq atomic.Uint64
}

// New 从环境变量 + $GQY_HOME/providers.json + routing.json 构造网关。
func New(cfg config.Config, client *http.Client) *Router {
	if client == nil {
		client = &http.Client{Timeout: 0}
	}
	r := &Router{
		byID:         map[string]*Provider{},
		groups:       map[string]*Group{},
		client:       client,
		home:         cfg.Home,
		maxAttempts:  3,
		cooldown:     30 * time.Second,
		queueTimeout: 1500 * time.Millisecond,
		states:       map[string]*providerState{},
		affinity:     map[string]string{},
		groupCursor:  map[string]uint64{},
		now:          time.Now,
	}

	// 环境变量 provider 兜底；没有 providers.json 时它就是唯一上游。
	if cfg.APIKey != "" {
		r.addProvider(&Provider{
			ID:             "env",
			Name:           "环境变量",
			BaseURL:        strings.TrimRight(cfg.APIBase, "/"),
			DefaultModel:   cfg.Model,
			Models:         []string{cfg.Model},
			APIKey:         cfg.APIKey,
			UserAgent:      cfg.APIUA,
			MaxConcurrency: 4,
		})
		r.def = Ref{Provider: "env", Model: cfg.Model}
	}

	if raw, err := os.ReadFile(filepath.Join(cfg.Home, "providers.json")); err == nil {
		var data struct {
			Providers []*Provider `json:"providers"`
		}
		if json.Unmarshal(raw, &data) == nil {
			for _, p := range data.Providers {
				normalizeProvider(p)
				if p.APIKey == "" {
					p.APIKey = os.Getenv("GQY_PROVIDER_" + envName(p.ID) + "_API_KEY")
				}
				if p.ID == "" || p.BaseURL == "" {
					continue
				}
				r.addProvider(p)
			}
		}
	}

	// routing.json 覆盖默认与组。
	if raw, err := os.ReadFile(filepath.Join(cfg.Home, "routing.json")); err == nil {
		var data routingFile
		if json.Unmarshal(raw, &data) == nil {
			if data.Default != nil && data.Default.Provider != "" {
				r.def = *data.Default
			}
			for i := range data.Groups {
				g := data.Groups[i]
				if g.ID == "" {
					continue
				}
				if g.Strategy == "" {
					g.Strategy = "order"
				}
				r.groups[g.ID] = &g
			}
			if data.MaxAttempts > 0 {
				r.maxAttempts = clamp(data.MaxAttempts, 1, 5)
			}
			if data.CooldownSeconds > 0 {
				r.cooldown = time.Duration(clamp(data.CooldownSeconds, 1, 600)) * time.Second
			}
			if data.QueueTimeoutMS > 0 {
				r.queueTimeout = time.Duration(data.QueueTimeoutMS) * time.Millisecond
			}
		}
	}

	if r.def.Provider == "" && len(r.providers) > 0 {
		p := r.providers[0]
		r.def = Ref{Provider: p.ID, Model: p.DefaultModel}
	}
	r.openLog()
	return r
}

func normalizeProvider(p *Provider) {
	p.BaseURL = strings.TrimRight(p.BaseURL, "/")
	if p.Protocol == "" {
		p.Protocol = "openai-chat"
	}
	if p.MaxConcurrency <= 0 {
		p.MaxConcurrency = 2
	}
	if p.Name == "" {
		p.Name = p.ID
	}
	if p.DefaultModel == "" && len(p.Models) > 0 {
		p.DefaultModel = p.Models[0]
	}
}

func (r *Router) addProvider(p *Provider) {
	if _, exists := r.byID[p.ID]; exists {
		return
	}
	r.providers = append(r.providers, p)
	r.byID[p.ID] = p
	r.states[p.ID] = &providerState{sem: make(chan struct{}, p.MaxConcurrency)}
}

func envName(id string) string {
	var b strings.Builder
	for _, c := range strings.ToUpper(id) {
		if (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') {
			b.WriteRune(c)
		} else {
			b.WriteByte('_')
		}
	}
	return b.String()
}

/* ---------------- 上下文 ---------------- */

type ctxKey int

const sessionKey ctxKey = 1

// WithSession 把会话 id 放进 context，供亲和使用。
func WithSession(ctx context.Context, id string) context.Context {
	if id == "" {
		return ctx
	}
	return context.WithValue(ctx, sessionKey, id)
}

func sessionOf(ctx context.Context) string {
	if v, ok := ctx.Value(sessionKey).(string); ok {
		return v
	}
	return ""
}

/* ---------------- 模型解析 ---------------- */

// Candidate 是一个可尝试的 provider + model。
type Candidate struct {
	Provider *Provider
	Model    string
}

// Available 表示至少有一个配置了密钥的 provider。
func (r *Router) Available() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, p := range r.providers {
		if p.APIKey != "" {
			return true
		}
	}
	return false
}

// DefaultModel 返回默认 provider 的模型名。
func (r *Router) DefaultModel() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if p := r.byID[r.def.Provider]; p != nil {
		if r.def.Model != "" {
			return r.def.Model
		}
		return p.DefaultModel
	}
	return ""
}

// Candidates 返回按策略排好序的候选，供测试与状态查看。
func (r *Router) Candidates(model, session string) []Candidate {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.candidatesLocked(model, session)
}

func (r *Router) candidatesLocked(model, session string) []Candidate {
	model = strings.TrimSpace(model)
	if g := r.groupFor(model); g != nil {
		out := r.groupCandidates(g)
		r.applyAffinity(out, session)
		return r.order(out)
	}
	if p, m, ok := r.splitProviderModel(model); ok {
		out := []Candidate{{Provider: p, Model: m}}
		r.applyAffinity(out, session)
		return out
	}
	if model == "" {
		out := r.defaultCandidates()
		r.applyAffinity(out, session)
		return out
	}
	// 裸模型名：先找声明过它的 provider。
	out := make([]Candidate, 0, 2)
	for _, p := range r.providers {
		if p.DefaultModel == model || contains(p.Models, model) {
			out = append(out, Candidate{Provider: p, Model: model})
		}
	}
	if len(out) == 0 && r.def.Provider != "" {
		if p := r.byID[r.def.Provider]; p != nil {
			out = append(out, Candidate{Provider: p, Model: model})
		}
	}
	r.applyAffinity(out, session)
	return out
}

func (r *Router) defaultCandidates() []Candidate {
	if r.def.Provider != "" {
		if p := r.byID[r.def.Provider]; p != nil {
			m := r.def.Model
			if m == "" {
				m = p.DefaultModel
			}
			return []Candidate{{Provider: p, Model: m}}
		}
	}
	if len(r.providers) > 0 {
		p := r.providers[0]
		return []Candidate{{Provider: p, Model: p.DefaultModel}}
	}
	return nil
}

func (r *Router) groupFor(model string) *Group {
	if g, ok := r.groups[strings.TrimPrefix(model, "group/")]; ok {
		return g
	}
	return nil
}

func (r *Router) splitProviderModel(model string) (*Provider, string, bool) {
	i := strings.Index(model, "/")
	if i <= 0 || i == len(model)-1 {
		return nil, "", false
	}
	p := r.byID[model[:i]]
	if p == nil {
		return nil, "", false
	}
	return p, model[i+1:], true
}

func (r *Router) groupCandidates(g *Group) []Candidate {
	out := make([]Candidate, 0, len(g.Members))
	for _, member := range g.Members {
		member = strings.TrimSpace(member)
		if member == "" {
			continue
		}
		if p, m, ok := r.splitProviderModel(member); ok {
			out = append(out, Candidate{Provider: p, Model: m})
			continue
		}
		for _, p := range r.providers {
			if p.DefaultModel == member || contains(p.Models, member) {
				out = append(out, Candidate{Provider: p, Model: member})
				break
			}
		}
	}
	switch g.Strategy {
	case "rotate":
		r.groupCursor[g.ID]++
		if len(out) > 1 {
			n := int(r.groupCursor[g.ID] % uint64(len(out)))
			out = append(out[n:], out[:n]...)
		}
	case "usage":
		sort.SliceStable(out, func(i, j int) bool {
			return r.callsOf(out[i].Provider.ID) < r.callsOf(out[j].Provider.ID)
		})
	}
	return out
}

func (r *Router) applyAffinity(out []Candidate, session string) {
	if session == "" || len(out) < 2 {
		return
	}
	want := r.affinity[session]
	if want == "" {
		return
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Provider.ID == want && out[j].Provider.ID != want
	})
}

func (r *Router) order(out []Candidate) []Candidate { return out }

func (r *Router) callsOf(id string) int64 {
	if s := r.states[id]; s != nil {
		return s.calls.Load()
	}
	return 0
}

/* ---------------- 调用 ---------------- */

// ErrNoCandidate 表示没有可用的 provider。
var ErrNoCandidate = errors.New("没有可用的 provider")

// Complete 实现 agent.Completer。
func (r *Router) Complete(ctx context.Context, req provider.Request) (provider.Message, *provider.Usage, error) {
	session := sessionOf(ctx)
	cands := r.Candidates(req.Model, session)
	if len(cands) == 0 {
		return provider.Message{}, nil, ErrNoCandidate
	}

	attempts := r.maxAttempts
	if attempts > len(cands) {
		attempts = len(cands)
	}
	var last error
	for i := 0; i < attempts; i++ {
		c := cands[i]
		if ctx.Err() != nil {
			return provider.Message{}, nil, ctx.Err()
		}
		if until := r.cooldownUntil(c.Provider.ID); until.After(r.now()) {
			last = fmt.Errorf("%s 冷却中，至 %s", c.Provider.ID, until.Format(time.RFC3339))
			continue
		}
		release, err := r.acquire(ctx, c.Provider.ID)
		if err != nil {
			last = err
			continue
		}
		start := r.now()
		creq := req
		creq.Model = c.Model
		up := &provider.Upstream{
			APIBase: c.Provider.BaseURL,
			APIKey:  c.Provider.APIKey,
			APIUA:   c.Provider.UserAgent,
			Model:   c.Model,
			Wire:    "chat",
			Client:  r.client,
		}
		msg, usage, err := up.Complete(ctx, creq)
		release()

		r.noteCall(c.Provider.ID, err)
		r.logRequest(session, c, err, r.now().Sub(start), i+1)

		if err == nil {
			r.setAffinity(session, c.Provider.ID)
			return msg, usage, nil
		}
		last = err
		if !retryable(err) {
			return msg, usage, err
		}
		r.markFailure(c.Provider.ID, err)
	}
	if last == nil {
		last = ErrNoCandidate
	}
	return provider.Message{}, nil, fmt.Errorf("所有候选失败: %w", last)
}

func (r *Router) acquire(ctx context.Context, id string) (func(), error) {
	r.mu.RLock()
	st := r.states[id]
	r.mu.RUnlock()
	if st == nil {
		return func() {}, nil
	}
	timer := time.NewTimer(r.queueTimeout)
	defer timer.Stop()
	select {
	case st.sem <- struct{}{}:
		return func() { <-st.sem }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-timer.C:
		return nil, fmt.Errorf("%s 并发已满，排队超时", id)
	}
}

func (r *Router) noteCall(id string, err error) {
	if s := r.states[id]; s != nil {
		s.calls.Add(1)
		if err != nil {
			s.failures.Add(1)
			s.lastErr = truncate(err.Error(), 300)
		}
	}
}

func (r *Router) markFailure(id string, err error) {
	if s := r.states[id]; s != nil {
		until := r.now().Add(r.cooldown)
		var ue *provider.UpstreamError
		if errors.As(err, &ue) {
			if ue.Status != http.StatusTooManyRequests && ue.Status < 500 {
				return
			}
			if ue.Status != http.StatusTooManyRequests {
				until = r.now().Add(r.cooldown / 2)
			}
		}
		s.cooldownUntil = until
	}
}

func (r *Router) cooldownUntil(id string) time.Time {
	if s := r.states[id]; s != nil {
		return s.cooldownUntil
	}
	return time.Time{}
}

func (r *Router) setAffinity(session, providerID string) {
	if session == "" {
		return
	}
	r.mu.Lock()
	r.affinity[session] = providerID
	// 简单上限，避免无限增长。
	if len(r.affinity) > 500 {
		for k := range r.affinity {
			if k != session {
				delete(r.affinity, k)
				break
			}
		}
	}
	r.mu.Unlock()
}

func retryable(err error) bool {
	if err == nil {
		return false
	}
	var ue *provider.UpstreamError
	if errors.As(err, &ue) {
		return ue.Status == http.StatusTooManyRequests || ue.Status >= 500 || ue.Code == "need_responses"
	}
	if errors.Is(err, context.Canceled) {
		return false
	}
	// 网络错误、DNS、超时都重试。
	return true
}

/* ---------------- 状态 ---------------- */

// ProviderStatus 是脱敏后的 provider 状态。
type ProviderStatus struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	BaseURL        string   `json:"baseUrl"`
	Protocol       string   `json:"protocol"`
	DefaultModel   string   `json:"defaultModel"`
	Models         []string `json:"models"`
	MaxConcurrency int      `json:"maxConcurrency"`
	KeySet         bool     `json:"keySet"`
	Inflight       int      `json:"inflight"`
	Calls          int64    `json:"calls"`
	Failures       int64    `json:"failures"`
	CooldownAt     *string  `json:"cooldownUntil"`
	LastError      string   `json:"lastError"`
}

// GroupStatus 是路由组状态。
type GroupStatus struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Strategy string   `json:"strategy"`
	Members  []string `json:"members"`
}

// Status 返回网关状态。
type Status struct {
	Default   Ref               `json:"default"`
	Providers []ProviderStatus  `json:"providers"`
	Groups    []GroupStatus     `json:"groups"`
	Affinity  map[string]string `json:"affinity"`
}

func (r *Router) Status() Status {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := Status{Default: r.def, Affinity: map[string]string{}, Providers: []ProviderStatus{}, Groups: []GroupStatus{}}
	for _, p := range r.providers {
		st := r.states[p.ID]
		ps := ProviderStatus{
			ID: p.ID, Name: p.Name, BaseURL: p.BaseURL, Protocol: p.Protocol,
			DefaultModel: p.DefaultModel, Models: append([]string{}, p.Models...),
			MaxConcurrency: p.MaxConcurrency, KeySet: p.APIKey != "",
		}
		if st != nil {
			ps.Inflight = len(st.sem)
			ps.Calls = st.calls.Load()
			ps.Failures = st.failures.Load()
			ps.LastError = st.lastErr
			if st.cooldownUntil.After(r.now()) {
				s := st.cooldownUntil.UTC().Format(time.RFC3339)
				ps.CooldownAt = &s
			}
		}
		out.Providers = append(out.Providers, ps)
	}
	for _, g := range r.groups {
		out.Groups = append(out.Groups, GroupStatus{ID: g.ID, Name: g.Name, Strategy: g.Strategy, Members: append([]string{}, g.Members...)})
	}
	sort.Slice(out.Providers, func(i, j int) bool { return out.Providers[i].ID < out.Providers[j].ID })
	sort.Slice(out.Groups, func(i, j int) bool { return out.Groups[i].ID < out.Groups[j].ID })
	for k, v := range r.affinity {
		out.Affinity[k] = v
	}
	return out
}

// Models 返回 OpenAI /v1/models 的条目。
func (r *Router) Models() []map[string]any {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := []map[string]any{}
	for _, p := range r.providers {
		models := p.Models
		if len(models) == 0 && p.DefaultModel != "" {
			models = []string{p.DefaultModel}
		}
		for _, m := range models {
			out = append(out, map[string]any{"id": p.ID + "/" + m, "object": "model", "owned_by": p.ID})
		}
	}
	for id := range r.groups {
		out = append(out, map[string]any{"id": "group/" + id, "object": "model", "owned_by": "gqy"})
	}
	sort.Slice(out, func(i, j int) bool { return out[i]["id"].(string) < out[j]["id"].(string) })
	return out
}

/* ---------------- 日志 ---------------- */

func (r *Router) openLog() {
	dir := filepath.Join(r.home, "gateway")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	f, err := os.OpenFile(filepath.Join(dir, "requests.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err == nil {
		r.logFile = f
	}
}

func (r *Router) logRequest(session string, c Candidate, err error, d time.Duration, attempt int) {
	if r.logFile == nil {
		return
	}
	status := "ok"
	code := ""
	if err != nil {
		status = "error"
		code = "gateway_error"
		var ue *provider.UpstreamError
		if errors.As(err, &ue) {
			code = ue.Code
		}
	}
	line, _ := json.Marshal(map[string]any{
		"ts":          r.now().UTC().Format(time.RFC3339Nano),
		"request_id":  r.newID(),
		"session":     session,
		"provider":    c.Provider.ID,
		"model":       c.Model,
		"status":      status,
		"error_code":  code,
		"duration_ms": d.Milliseconds(),
		"attempt":     attempt,
	})
	r.logMu.Lock()
	_, _ = r.logFile.Write(append(line, '\n'))
	r.logMu.Unlock()
}

func (r *Router) newID() string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// Close 关闭日志文件。
func (r *Router) Close() error {
	r.logMu.Lock()
	defer r.logMu.Unlock()
	if r.logFile != nil {
		return r.logFile.Close()
	}
	return nil
}

/* ---------------- 工具 ---------------- */

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func truncate(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n])
}
