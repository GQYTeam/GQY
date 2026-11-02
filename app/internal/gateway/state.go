package gateway

import (
	"time"

	"gqy/internal/config"
	"gqy/internal/provider"
)

type accountState struct {
	sem           chan struct{}
	cooldownUntil time.Time
	day           string
	usedTokens    int64
	calls         int64
	failures      int64
	lastUsed      time.Time
}

func accountKey(providerID, accountID string) string {
	return providerID + "\x00" + accountID
}

func today(now time.Time) string {
	return now.UTC().Format("2006-01-02")
}

func (r *Router) stateFor(providerID, accountID string, capacity int) *accountState {
	if capacity < 1 {
		capacity = 1
	}
	key := accountKey(providerID, accountID)
	r.mu.Lock()
	defer r.mu.Unlock()
	st := r.accounts[key]
	if st == nil {
		st = &accountState{sem: make(chan struct{}, capacity)}
		r.accounts[key] = st
	}
	return st
}

func resetQuotaLocked(st *accountState, now time.Time) {
	day := today(now)
	if st.day != day {
		st.day = day
		st.usedTokens = 0
	}
}

func (r *Router) quotaExhausted(st *accountState, limit int64, now time.Time) bool {
	if limit <= 0 {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	resetQuotaLocked(st, now)
	return st.usedTokens >= limit
}

func (r *Router) cooling(st *accountState, now time.Time) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return st.cooldownUntil.After(now)
}

func (r *Router) recordCall(c *candidate) {
	r.mu.Lock()
	defer r.mu.Unlock()
	c.State.calls++
}

func (r *Router) recordSuccess(c *candidate, usage provider.Usage, sessionID string, now time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	resetQuotaLocked(c.State, now)
	c.State.usedTokens += int64(usage.Total())
	c.State.lastUsed = now
	for _, key := range c.MemberKeys {
		r.memberLastUsed[key] = now
	}
	if sessionID != "" {
		r.affinity[sessionID] = accountKey(c.Provider.ID, c.Account.ID)
	}
}

func (r *Router) recordFailure(c *candidate, pe *provider.Error, cooldownSeconds int, now time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	c.State.failures++
	if pe != nil && pe.Retryable {
		if cooldownSeconds <= 0 {
			cooldownSeconds = config.DefaultCooldownSecs
		}
		c.State.cooldownUntil = now.Add(time.Duration(cooldownSeconds) * time.Second)
	}
}

func (r *Router) affinityKey(sessionID string) string {
	if sessionID == "" {
		return ""
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.affinity[sessionID]
}

// AccountStatus 是一个账号的运行时状态（R7、A9）。
type AccountStatus struct {
	Provider           string `json:"provider"`
	ID                 string `json:"id"`
	Healthy            bool   `json:"healthy"`
	Calls              int64  `json:"calls"`
	Failures           int64  `json:"failures"`
	InFlight           int    `json:"in_flight"`
	Capacity           int    `json:"capacity"`
	CooldownRemainingS int    `json:"cooldown_remaining_s"`
	TokenUsedToday     int64  `json:"token_used_today"`
	TokenLimit         int64  `json:"token_limit"`
	QuotaExhausted     bool   `json:"quota_exhausted"`
}

// AccountStatuses 按当前配置顺序输出账号运行时状态。
func (r *Router) AccountStatuses(snap *config.Snapshot) []AccountStatus {
	now := r.now()
	r.mu.Lock()
	defer r.mu.Unlock()

	out := make([]AccountStatus, 0)
	for _, p := range snap.Providers {
		capacity := p.MaxConcurrency
		if capacity < 1 {
			capacity = 1
		}
		for _, a := range p.Accounts {
			st := r.accounts[accountKey(p.ID, a.ID)]
			status := AccountStatus{
				Provider:   p.ID,
				ID:         a.ID,
				Capacity:   capacity,
				TokenLimit: a.DailyTokenLimit,
			}
			if st != nil {
				resetQuotaLocked(st, now)
				status.Calls = st.calls
				status.Failures = st.failures
				status.InFlight = len(st.sem)
				status.Capacity = cap(st.sem)
				status.TokenUsedToday = st.usedTokens
				if !st.cooldownUntil.IsZero() {
					remaining := st.cooldownUntil.Sub(now)
					if remaining > 0 {
						status.CooldownRemainingS = int(remaining.Seconds())
					}
				}
			}
			status.QuotaExhausted = a.DailyTokenLimit > 0 && status.TokenUsedToday >= a.DailyTokenLimit
			status.Healthy = !a.Disabled && status.CooldownRemainingS == 0 && !status.QuotaExhausted
			out = append(out, status)
		}
	}
	return out
}

type requestLogEntry = RequestLogEntry

func (r *Router) logRequest(entry requestLogEntry) {
	if r.home == "" {
		return
	}
	entry.TS = r.now().UTC().Format(time.RFC3339)
	if err := AppendRequestLog(r.home, entry); err != nil {
		r.log.Warn("请求日志写入失败", "error", err)
	}
}
