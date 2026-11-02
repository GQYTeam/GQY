package gateway

// 多租户并发与配额（MT1/MT3/MT5/MT8）：
// 每个租户（任务/工作区/agent）有独立的并发池与每分钟请求配额，
// 租户之间不共享队列，一个租户打满不会拖垮其他租户（全局上游容量除外）。

import (
	"context"
	"sort"
	"sync"
	"time"

	"gqy/internal/config"
	"gqy/internal/provider"
)

// DefaultTenant 是未显式指定租户时的名字。
const DefaultTenant = "default"

type tenantState struct {
	sem    chan struct{} // nil 表示不单独限制并发
	mu     sync.Mutex
	window time.Time
	count  int
	limit  int
	// 观测
	inFlight int
	peak     int
	queued   int
	accepted int64
	rejected int64
}

// tenantStateFor 取（或创建）租户状态；limit 为该租户每分钟请求上限（0 不限）。
func (r *Router) tenantStateFor(name string, maxConcurrency, reqPerMin int) *tenantState {
	r.tenantsMu.Lock()
	defer r.tenantsMu.Unlock()
	st := r.tenants[name]
	if st == nil {
		st = &tenantState{limit: reqPerMin}
		if maxConcurrency > 0 {
			st.sem = make(chan struct{}, maxConcurrency)
		}
		r.tenants[name] = st
	}
	return st
}

// tenantFor 按配置解析租户；返回 nil 表示「未配置任何租户」，此时不做租户级限制。
func tenantFor(snap *config.Snapshot, name string) *config.Tenant {
	if snap == nil || len(snap.Routing.Tenants) == 0 {
		return nil
	}
	for i := range snap.Routing.Tenants {
		if snap.Routing.Tenants[i].Name == name {
			return &snap.Routing.Tenants[i]
		}
	}
	return nil
}

// acquireTenant 占用一个租户并发槽并检查每分钟配额（MT1/MT3）。
// 返回的 release 必须调用一次。
func (r *Router) acquireTenant(ctx context.Context, snap *config.Snapshot, meta Meta, queueTimeoutMS int) (func(), error) {
	cfg := tenantFor(snap, meta.Tenant)
	if cfg == nil {
		if snap != nil && len(snap.Routing.Tenants) > 0 && meta.Tenant != "" {
			// 配置了租户但请求带了未登记的名字：直接拒绝，避免绕过配额（MT3）。
			return func() {}, &provider.Error{Code: provider.CodeTenantUnknown, Retryable: false, HTTPStatus: 400, Message: "租户未登记: " + meta.Tenant}
		}
		cfg = &config.Tenant{Name: DefaultTenant}
	}
	st := r.tenantStateFor(meta.Tenant, cfg.MaxConcurrency, cfg.ReqPerMin)

	// 每分钟配额（固定窗口，够用且常数开销）。
	if st.limit > 0 {
		now := r.now()
		st.mu.Lock()
		if st.window.IsZero() || now.Sub(st.window) >= time.Minute {
			st.window = now.Truncate(time.Minute)
			st.count = 0
		}
		if st.count >= st.limit {
			st.rejected++
			st.mu.Unlock()
			retry := 60 - now.Second()
			if retry <= 0 {
				retry = 60
			}
			perr := &provider.Error{Code: provider.CodeTenantQuota, Retryable: false, HTTPStatus: 429, Message: "租户每分钟请求数超限"}
			perr.RetryAfter = retry
			return func() {}, perr
		}
		st.count++
		st.accepted++
		st.mu.Unlock()
	}

	// 并发池：满则排队，超时返回 tenant_queue_timeout。
	if st.sem != nil {
		if queueTimeoutMS <= 0 {
			queueTimeoutMS = config.DefaultQueueTimeout
		}
		select {
		case st.sem <- struct{}{}:
		default:
			st.mu.Lock()
			st.queued++
			st.mu.Unlock()
			timer := time.NewTimer(time.Duration(queueTimeoutMS) * time.Millisecond)
			defer timer.Stop()
			select {
			case st.sem <- struct{}{}:
			case <-timer.C:
				st.mu.Lock()
				st.queued--
				st.rejected++
				st.mu.Unlock()
				return func() {}, &provider.Error{Code: provider.CodeTenantQueueTimeout, Retryable: false, HTTPStatus: 503, Message: "租户并发队列等待超时"}
			case <-ctx.Done():
				st.mu.Lock()
				st.queued--
				st.mu.Unlock()
				return func() {}, provider.NetworkError(ctx.Err())
			}
			st.mu.Lock()
			st.queued--
			st.mu.Unlock()
		}
	}

	st.mu.Lock()
	st.inFlight++
	if st.inFlight > st.peak {
		st.peak = st.inFlight
	}
	st.mu.Unlock()

	return func() {
		if st.sem != nil {
			<-st.sem
		}
		st.mu.Lock()
		st.inFlight--
		st.mu.Unlock()
	}, nil
}

// TenantStatus 是对外的租户观测快照（MT8）。
type TenantStatus struct {
	Name     string `json:"name"`
	InFlight int    `json:"in_flight"`
	Peak     int    `json:"peak"`
	Queued   int    `json:"queued"`
	Accepted int64  `json:"accepted"`
	Rejected int64  `json:"rejected"`
	Limit    int    `json:"req_per_min"`
	MaxConc  int    `json:"max_concurrency"`
}

// TenantStatuses 返回全部租户的观测快照（按名字排序）。
func (r *Router) TenantStatuses() []TenantStatus {
	r.tenantsMu.Lock()
	defer r.tenantsMu.Unlock()
	out := make([]TenantStatus, 0, len(r.tenants))
	for name, st := range r.tenants {
		st.mu.Lock()
		maxConc := 0
		if st.sem != nil {
			maxConc = cap(st.sem)
		}
		out = append(out, TenantStatus{
			Name:     name,
			InFlight: st.inFlight,
			Peak:     st.peak,
			Queued:   st.queued,
			Accepted: st.accepted,
			Rejected: st.rejected,
			Limit:    st.limit,
			MaxConc:  maxConc,
		})
		st.mu.Unlock()
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
