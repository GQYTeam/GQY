package gateway

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"gqy/internal/config"
	"gqy/internal/provider"
)

// Meta 是一次请求的非正文元数据。
type Meta struct {
	RequestID string
	SessionID string
	// OnSelect 在每次尝试调用上游前触发，供 HTTP 层设置响应头（R4）。
	// 流式请求在首帧前可能多次触发；首帧写出后不再切换候选。
	OnSelect func(providerID, accountID, model string)
}

func (m Meta) notifySelect(c candidate) {
	if m.OnSelect != nil {
		m.OnSelect(c.Provider.ID, c.Account.ID, c.Model)
	}
}

// Result 是一次成功调用的结果与归属。
type Result struct {
	Provider string
	Account  string
	Model    string
	Response *provider.Response
	Attempts int
}

// Router 是网关运行时：选候选、限并发、失败转移、冷却、配额与亲和。
// 配置通过每次请求传入的快照读取，因此热更新不影响在途请求（CF5）。
type Router struct {
	registry *provider.Registry
	log      *slog.Logger
	home     string
	now      func() time.Time

	mu             sync.Mutex
	accounts       map[string]*accountState
	affinity       map[string]string
	groupCursor    map[string]uint64
	memberLastUsed map[string]time.Time
}

// New 构造网关运行时。
func New(registry *provider.Registry, logger *slog.Logger, home string) *Router {
	if registry == nil {
		registry = provider.NewRegistry()
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Router{
		registry:       registry,
		log:            logger,
		home:           home,
		now:            time.Now,
		accounts:       make(map[string]*accountState),
		affinity:       make(map[string]string),
		groupCursor:    make(map[string]uint64),
		memberLastUsed: make(map[string]time.Time),
	}
}

type modelRef struct {
	ProviderID string
	Model      string
	memberKeys []string
}

type candidate struct {
	Provider   *config.Provider
	Account    config.Account
	Model      string
	MemberKeys []string
	State      *accountState
}

// Complete 发起一次非流式调用，返回首个成功候选。
func (r *Router) Complete(ctx context.Context, snap *config.Snapshot, req *provider.Request, meta Meta) (*Result, error) {
	ctx, cancel := withUpstreamTimeout(ctx, snap)
	defer cancel()
	start := r.now()
	refs, err := r.resolveOrdered(snap, req.Model)
	if err != nil {
		code := provider.CodeInvalidRequest
		if errors.Is(err, ErrModelNotFound) {
			code = provider.CodeModelNotFound
		}
		r.finishLog(start, meta, req.Model, "", "", "no_candidate", resolveStatus(code), 0, code)
		return nil, &provider.Error{Code: code, Message: err.Error(), HTTPStatus: resolveStatus(code)}
	}
	candidates := r.orderByAffinity(r.expandCandidates(snap, refs, start), meta.SessionID)
	if len(candidates) == 0 {
		r.finishLog(start, meta, req.Model, "", "", "no_candidate", 503, 0, provider.CodeNoCandidate)
		return nil, provider.NewError(provider.CodeNoCandidate, false, 503, "没有可用候选")
	}

	maxAttempts := snap.Routing.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = config.DefaultMaxAttempts
	}
	attempts := 0
	var lastErr error

	for i := 0; i < len(candidates) && attempts < maxAttempts; i++ {
		c := candidates[i]
		attempts++
		meta.notifySelect(c)
		r.recordCall(&c)
		if err := r.acquire(ctx, c, snap.Routing.QueueTimeoutMS); err != nil {
			r.finishLog(start, meta, c.Model, c.Provider.ID, c.Account.ID, "error", provider.AsError(err).ClientStatus(), attempts, provider.ErrorCode(err))
			return nil, err
		}
		resp, callErr := r.callChat(ctx, c, req)
		r.release(c)
		if callErr == nil {
			r.recordSuccess(&c, resp.Usage, meta.SessionID, r.now())
			result := &Result{Provider: c.Provider.ID, Account: c.Account.ID, Model: c.Model, Response: resp, Attempts: attempts}
			r.finishLog(start, meta, c.Model, c.Provider.ID, c.Account.ID, "ok", 200, attempts, "")
			return result, nil
		}

		pe := provider.AsError(callErr)
		if ctx.Err() != nil {
			pe = provider.NetworkError(ctx.Err())
		}
		lastErr = pe
		r.recordFailure(&c, pe, snap.Routing.CooldownSeconds, r.now())
		if !pe.Retryable {
			r.finishLog(start, meta, c.Model, c.Provider.ID, c.Account.ID, "error", pe.ClientStatus(), attempts, pe.Code)
			return nil, pe
		}
	}

	final := provider.NewError(provider.CodeNoCandidate, false, 503, "候选已耗尽")
	final.Err = lastErr
	// 全部候选都失败时，若最后一次是超时/连接错误，直接把它返回给调用方（019）：
	// 比笼统的 no_candidate 更能说明「上游不可达/太慢」，也便于客户端按语义重试。
	if pe := provider.AsError(lastErr); pe != nil && (pe.Code == provider.CodeUpstreamTimeout || pe.Code == provider.CodeConnectionError) {
		return nil, pe
	}
	providerID, accountID, model := "", "", req.Model
	if len(candidates) > 0 {
		last := candidates[minInt(attempts, len(candidates))-1]
		providerID, accountID, model = last.Provider.ID, last.Account.ID, last.Model
	}
	r.finishLog(start, meta, model, providerID, accountID, "no_candidate", 503, attempts, provider.CodeNoCandidate)
	return nil, final
}

// Stream 发起一次流式调用。首个事件交给客户端前允许转移；之后不再切换。
func (r *Router) Stream(ctx context.Context, snap *config.Snapshot, req *provider.Request, meta Meta, emit func(provider.Event) error) (*Result, error) {
	ctx, cancel := withUpstreamTimeout(ctx, snap)
	defer cancel()
	// 流式空闲超时：连续 idle 时间没有新事件就中止，避免上游卡住不动（019/022）。
	idle := time.Duration(streamIdleTimeout(snap)) * time.Second
	var idleFired atomic.Bool
	idleTimer := time.AfterFunc(idle, func() {
		idleFired.Store(true)
		cancel()
	})
	defer idleTimer.Stop()
	start := r.now()
	refs, err := r.resolveOrdered(snap, req.Model)
	if err != nil {
		code := provider.CodeInvalidRequest
		if errors.Is(err, ErrModelNotFound) {
			code = provider.CodeModelNotFound
		}
		r.finishLog(start, meta, req.Model, "", "", "no_candidate", resolveStatus(code), 0, code)
		return nil, &provider.Error{Code: code, Message: err.Error(), HTTPStatus: resolveStatus(code)}
	}
	candidates := r.orderByAffinity(r.expandCandidates(snap, refs, start), meta.SessionID)
	if len(candidates) == 0 {
		r.finishLog(start, meta, req.Model, "", "", "no_candidate", 503, 0, provider.CodeNoCandidate)
		return nil, provider.NewError(provider.CodeNoCandidate, false, 503, "没有可用候选")
	}

	maxAttempts := snap.Routing.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = config.DefaultMaxAttempts
	}
	attempts := 0
	var lastErr error

	for i := 0; i < len(candidates) && attempts < maxAttempts; i++ {
		c := candidates[i]
		attempts++
		meta.notifySelect(c)
		r.recordCall(&c)
		if err := r.acquire(ctx, c, snap.Routing.QueueTimeoutMS); err != nil {
			r.finishLog(start, meta, c.Model, c.Provider.ID, c.Account.ID, "error", provider.AsError(err).ClientStatus(), attempts, provider.ErrorCode(err))
			return nil, err
		}
		started := false
		wrapped := func(ev provider.Event) error {
			started = true
			idleTimer.Reset(idle)
			return emit(ev)
		}
		resp, callErr := r.callStream(ctx, c, req, wrapped)
		r.release(c)
		if callErr == nil {
			r.recordSuccess(&c, resp.Usage, meta.SessionID, r.now())
			result := &Result{Provider: c.Provider.ID, Account: c.Account.ID, Model: c.Model, Response: resp, Attempts: attempts}
			r.finishLog(start, meta, c.Model, c.Provider.ID, c.Account.ID, "ok", 200, attempts, "")
			return result, nil
		}

		pe := provider.AsError(callErr)
		if idleFired.Load() {
			pe = &provider.Error{Code: provider.CodeUpstreamTimeout, Retryable: true, HTTPStatus: 504, Message: "上游流空闲超时"}
		} else if ctx.Err() != nil {
			pe = provider.NetworkError(ctx.Err())
		}
		lastErr = pe
		r.recordFailure(&c, pe, snap.Routing.CooldownSeconds, r.now())
		if started {
			r.finishLog(start, meta, c.Model, c.Provider.ID, c.Account.ID, "error", 200, attempts, pe.Code)
			return nil, pe
		}
		if !pe.Retryable {
			r.finishLog(start, meta, c.Model, c.Provider.ID, c.Account.ID, "error", pe.ClientStatus(), attempts, pe.Code)
			return nil, pe
		}
	}

	final := provider.NewError(provider.CodeNoCandidate, false, 503, "候选已耗尽")
	final.Err = lastErr
	// 全部候选都失败时，若最后一次是超时/连接错误，直接把它返回给调用方（019）：
	// 比笼统的 no_candidate 更能说明「上游不可达/太慢」，也便于客户端按语义重试。
	if pe := provider.AsError(lastErr); pe != nil && (pe.Code == provider.CodeUpstreamTimeout || pe.Code == provider.CodeConnectionError) {
		return nil, pe
	}
	providerID, accountID, model := "", "", req.Model
	if len(candidates) > 0 {
		last := candidates[minInt(attempts, len(candidates))-1]
		providerID, accountID, model = last.Provider.ID, last.Account.ID, last.Model
	}
	r.finishLog(start, meta, model, providerID, accountID, "no_candidate", 503, attempts, provider.CodeNoCandidate)
	return nil, final
}

func (r *Router) callChat(ctx context.Context, c candidate, req *provider.Request) (*provider.Response, error) {
	adapter, ok := r.registry.Get(c.Provider.Protocol)
	if !ok {
		return nil, provider.NewError(provider.CodeNoCandidate, false, 503, "协议的适配器未注册: "+c.Provider.Protocol)
	}
	callReq := *req
	callReq.Model = c.Model
	callReq.Stream = false
	return adapter.Chat(ctx, endpoint(c), &callReq)
}

func (r *Router) callStream(ctx context.Context, c candidate, req *provider.Request, emit func(provider.Event) error) (*provider.Response, error) {
	adapter, ok := r.registry.Get(c.Provider.Protocol)
	if !ok {
		return nil, provider.NewError(provider.CodeNoCandidate, false, 503, "协议的适配器未注册: "+c.Provider.Protocol)
	}
	callReq := *req
	callReq.Model = c.Model
	callReq.Stream = true
	return adapter.Stream(ctx, endpoint(c), &callReq, emit)
}

func endpoint(c candidate) provider.Endpoint {
	return provider.Endpoint{
		ProviderID: c.Provider.ID,
		BaseURL:    c.Provider.BaseURL,
		APIKey:     c.Account.APIKey,
		Protocol:   c.Provider.Protocol,
		UserAgent:  c.Provider.UserAgent,
	}
}

func (r *Router) resolveOrdered(snap *config.Snapshot, model string) ([]modelRef, error) {
	model = strings.TrimSpace(model)
	if model == "" {
		if snap.Routing.Default == nil {
			return nil, fmt.Errorf("%w：未指定 model 且没有默认模型", ErrModelNotFound)
		}
		return []modelRef{{ProviderID: snap.Routing.Default.Provider, Model: snap.Routing.Default.Model}}, nil
	}

	if strings.HasPrefix(model, "group/") {
		id := strings.TrimPrefix(model, "group/")
		refs, err := r.expandGroup(snap, id, 0, nil)
		if err != nil {
			return nil, err
		}
		return dedupeRefs(refs), nil
	}

	if providerID, name, ok := strings.Cut(model, "/"); ok {
		if providerID == "" || name == "" {
			return nil, fmt.Errorf("%w：%q 不是合法的 provider/model", ErrModelNotFound, model)
		}
		if findProvider(snap, providerID) == nil {
			return nil, fmt.Errorf("%w：provider %q 不存在", ErrModelNotFound, providerID)
		}
		return []modelRef{{ProviderID: providerID, Model: name}}, nil
	}

	out := make([]modelRef, 0, 2)
	for i := range snap.Providers {
		if snap.Providers[i].DefaultModel == model {
			out = append(out, modelRef{ProviderID: snap.Providers[i].ID, Model: model})
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%w：%q 未匹配任何 provider", ErrModelNotFound, model)
	}
	return out, nil
}

func (r *Router) expandGroup(snap *config.Snapshot, id string, depth int, parentKeys []string) ([]modelRef, error) {
	if depth > 64 {
		return nil, fmt.Errorf("%w：路由组嵌套过深", ErrModelNotFound)
	}
	var group *config.Group
	for i := range snap.Routing.Groups {
		if snap.Routing.Groups[i].ID == id {
			group = &snap.Routing.Groups[i]
			break
		}
	}
	if group == nil {
		return nil, fmt.Errorf("%w：路由组 %q 不存在", ErrModelNotFound, id)
	}

	members := r.orderMembers(group)
	out := make([]modelRef, 0, len(members))
	for _, member := range members {
		memberKey := group.ID + "\x00" + member
		childKeys := append(append([]string(nil), parentKeys...), memberKey)
		switch {
		case strings.HasPrefix(member, "group/"):
			refs, err := r.expandGroup(snap, strings.TrimPrefix(member, "group/"), depth+1, childKeys)
			if err != nil {
				return nil, err
			}
			out = append(out, refs...)
		case strings.Contains(member, "/"):
			providerID, name, _ := strings.Cut(member, "/")
			if providerID == "" || name == "" {
				return nil, fmt.Errorf("%w：组成员 %q 非法", ErrModelNotFound, member)
			}
			if findProvider(snap, providerID) == nil {
				return nil, fmt.Errorf("%w：组成员引用了不存在的 provider %q", ErrModelNotFound, providerID)
			}
			out = append(out, modelRef{ProviderID: providerID, Model: name, memberKeys: childKeys})
		default:
			found := false
			for i := range snap.Providers {
				p := &snap.Providers[i]
				if p.DefaultModel == member {
					out = append(out, modelRef{ProviderID: p.ID, Model: member, memberKeys: childKeys})
					found = true
				}
			}
			if !found {
				return nil, fmt.Errorf("%w：组成员 %q 未匹配任何 provider", ErrModelNotFound, member)
			}
		}
	}
	return out, nil
}

func (r *Router) orderMembers(group *config.Group) []string {
	members := append([]string(nil), group.Members...)
	switch group.Strategy {
	case config.StrategyRotate:
		r.mu.Lock()
		cursor := r.groupCursor[group.ID]
		r.groupCursor[group.ID] = cursor + 1
		r.mu.Unlock()
		if len(members) > 0 {
			offset := int(cursor % uint64(len(members))) // #nosec G115 -- 取模结果小于 len(members)
			rotated := make([]string, 0, len(members))
			rotated = append(rotated, members[offset:]...)
			rotated = append(rotated, members[:offset]...)
			members = rotated
		}
	case config.StrategyUsage:
		type memberUse struct {
			name string
			last time.Time
		}
		uses := make([]memberUse, 0, len(members))
		r.mu.Lock()
		for _, member := range members {
			uses = append(uses, memberUse{name: member, last: r.memberLastUsed[group.ID+"\x00"+member]})
		}
		r.mu.Unlock()
		sort.SliceStable(uses, func(i, j int) bool { return uses[i].last.Before(uses[j].last) })
		for i := range uses {
			members[i] = uses[i].name
		}
	}
	return members
}

func dedupeRefs(in []modelRef) []modelRef {
	seen := make(map[string]bool, len(in))
	out := make([]modelRef, 0, len(in))
	for _, ref := range in {
		key := ref.ProviderID + "\x00" + ref.Model
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, ref)
	}
	return out
}

func (r *Router) expandCandidates(snap *config.Snapshot, refs []modelRef, now time.Time) []candidate {
	out := make([]candidate, 0, len(refs))
	for _, ref := range refs {
		p := findProvider(snap, ref.ProviderID)
		if p == nil {
			continue
		}
		if _, ok := r.registry.Get(p.Protocol); !ok {
			continue
		}
		for _, account := range p.Accounts {
			if account.Disabled {
				continue
			}
			st := r.stateFor(p.ID, account.ID, p.MaxConcurrency)
			if r.quotaExhausted(st, account.DailyTokenLimit, now) {
				continue
			}
			if r.cooling(st, now) {
				continue
			}
			out = append(out, candidate{
				Provider:   p,
				Account:    account,
				Model:      ref.Model,
				MemberKeys: ref.memberKeys,
				State:      st,
			})
		}
	}
	return out
}

func (r *Router) orderByAffinity(candidates []candidate, sessionID string) []candidate {
	key := r.affinityKey(sessionID)
	if key == "" {
		return candidates
	}
	for i := range candidates {
		if accountKey(candidates[i].Provider.ID, candidates[i].Account.ID) != key {
			continue
		}
		if i > 0 {
			c := candidates[i]
			copy(candidates[1:i+1], candidates[0:i])
			candidates[0] = c
		}
		break
	}
	return candidates
}

func (r *Router) acquire(ctx context.Context, c candidate, queueTimeoutMS int) error {
	if queueTimeoutMS <= 0 {
		queueTimeoutMS = config.DefaultQueueTimeout
	}
	select {
	case c.State.sem <- struct{}{}:
		return nil
	default:
	}

	timer := time.NewTimer(time.Duration(queueTimeoutMS) * time.Millisecond)
	defer timer.Stop()
	select {
	case c.State.sem <- struct{}{}:
		return nil
	case <-timer.C:
		return provider.NewError(provider.CodeQueueTimeout, false, 503, "账号并发队列等待超时")
	case <-ctx.Done():
		return provider.NetworkError(ctx.Err())
	}
}

func (r *Router) release(c candidate) {
	<-c.State.sem
}

func findProvider(snap *config.Snapshot, id string) *config.Provider {
	for i := range snap.Providers {
		if snap.Providers[i].ID == id {
			return &snap.Providers[i]
		}
	}
	return nil
}

func (r *Router) finishLog(start time.Time, meta Meta, model, providerID, accountID, status string, httpStatus, attempts int, errCode string) {
	r.logRequest(requestLogEntry{
		RequestID:  meta.RequestID,
		Session:    meta.SessionID,
		Provider:   providerID,
		Model:      model,
		Account:    accountID,
		Status:     status,
		HTTPStatus: httpStatus,
		DurationMS: r.now().Sub(start).Milliseconds(),
		Attempt:    attempts,
		ErrorCode:  errCode,
	})
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func resolveStatus(code string) int {
	if code == provider.CodeModelNotFound {
		return 404
	}
	return 400
}

// streamIdleTimeout 返回流式空闲超时秒数（settings.stream_idle_timeout_s）。
func streamIdleTimeout(snap *config.Snapshot) int {
	if snap != nil && snap.Settings.StreamIdleTimeoutS >= 5 {
		return snap.Settings.StreamIdleTimeoutS
	}
	return 60
}

// withUpstreamTimeout 给一次上游调用加整体超时（settings.upstream_timeout_s，热更新生效）。
func withUpstreamTimeout(ctx context.Context, snap *config.Snapshot) (context.Context, context.CancelFunc) {
	limit := 0
	if snap != nil {
		limit = snap.Settings.UpstreamTimeoutS
	}
	if limit <= 0 {
		limit = 120
	}
	return context.WithTimeout(ctx, time.Duration(limit)*time.Second)
}
