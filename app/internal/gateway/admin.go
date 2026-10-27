package gateway

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var providerIDRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)

// ProviderViews 返回脱敏后的 provider 列表。
func (r *Router) ProviderViews() []ProviderStatus {
	return r.Status().Providers
}

// UpsertProvider 新增或更新 provider，并写回 providers.json。
// 更新时 api_key 留空表示保留原密钥。
func (r *Router) UpsertProvider(p Provider) error {
	p.ID = strings.TrimSpace(p.ID)
	if !providerIDRe.MatchString(p.ID) {
		return fmt.Errorf("provider id 不合法：%q", p.ID)
	}
	p.BaseURL = strings.TrimRight(strings.TrimSpace(p.BaseURL), "/")
	if err := validateBaseURL(p.BaseURL); err != nil {
		return err
	}
	normalizeProvider(&p)
	if p.DefaultModel == "" && len(p.Models) > 0 {
		p.DefaultModel = p.Models[0]
	}
	if p.DefaultModel == "" {
		return fmt.Errorf("default_model 不能为空")
	}
	if p.MaxConcurrency <= 0 || p.MaxConcurrency > 64 {
		p.MaxConcurrency = 2
	}
	if p.Models == nil {
		p.Models = []string{p.DefaultModel}
	}
	if !contains(p.Models, p.DefaultModel) {
		p.Models = append(p.Models, p.DefaultModel)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if old := r.byID[p.ID]; old != nil {
		if p.APIKey == "" {
			p.APIKey = old.APIKey
		}
		if p.UserAgent == "" {
			p.UserAgent = old.UserAgent
		}
		if len(p.Accounts) == 0 && len(old.Accounts) > 0 {
			p.Accounts = old.Accounts
		}
		for i := range p.Accounts {
			if p.Accounts[i].APIKey != "" {
				continue
			}
			for _, oldAcc := range old.Accounts {
				if oldAcc.ID == p.Accounts[i].ID {
					p.Accounts[i].APIKey = oldAcc.APIKey
					break
				}
			}
		}
		*old = p
		if st := r.states[p.ID]; st == nil || cap(st.sem) != p.MaxConcurrency {
			r.states[p.ID] = &providerState{sem: make(chan struct{}, p.MaxConcurrency)}
		}
	} else {
		r.addProvider(&p)
	}
	if r.def.Provider == "" {
		r.def = Ref{Provider: p.ID, Model: p.DefaultModel}
	}
	return r.saveProvidersLocked()
}

// UpsertAccount 新增/更新 provider 下的账号；key 留空保留原值。
func (r *Router) UpsertAccount(providerID string, acc Account) error {
	acc.ID = strings.TrimSpace(acc.ID)
	if !providerIDRe.MatchString(acc.ID) {
		return fmt.Errorf("account id 不合法：%q", acc.ID)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	p := r.byID[providerID]
	if p == nil {
		return fmt.Errorf("provider 不存在：%s", providerID)
	}
	for i := range p.Accounts {
		if p.Accounts[i].ID == acc.ID {
			if acc.APIKey == "" {
				acc.APIKey = p.Accounts[i].APIKey
			}
			if acc.Name == "" {
				acc.Name = p.Accounts[i].Name
			}
			p.Accounts[i] = acc
			return r.saveProvidersLocked()
		}
	}
	if acc.Name == "" {
		acc.Name = acc.ID
	}
	p.Accounts = append(p.Accounts, acc)
	return r.saveProvidersLocked()
}

// DeleteAccount 删除 provider 下的账号。
func (r *Router) DeleteAccount(providerID, accountID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	p := r.byID[providerID]
	if p == nil {
		return fmt.Errorf("provider 不存在：%s", providerID)
	}
	out := p.Accounts[:0]
	found := false
	for _, a := range p.Accounts {
		if a.ID == accountID {
			found = true
			continue
		}
		out = append(out, a)
	}
	if !found {
		return fmt.Errorf("账号不存在：%s", accountID)
	}
	p.Accounts = out
	delete(r.states, providerID+"|"+accountID)
	delete(r.quota, providerID+"|"+accountID)
	return r.saveProvidersLocked()
}

// DeleteProvider 删除 provider；环境变量 provider 不可删。
func (r *Router) DeleteProvider(id string) error {
	if id == "env" {
		return fmt.Errorf("环境变量 provider 不能删除，请清空 GQY_API_KEY")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.byID[id]; !ok {
		return fmt.Errorf("provider 不存在：%s", id)
	}
	delete(r.byID, id)
	delete(r.states, id)
	out := r.providers[:0]
	for _, p := range r.providers {
		if p.ID != id {
			out = append(out, p)
		}
	}
	r.providers = out
	if r.def.Provider == id {
		r.def = Ref{}
		if len(r.providers) > 0 {
			r.def = Ref{Provider: r.providers[0].ID, Model: r.providers[0].DefaultModel}
		}
	}
	return r.saveProvidersLocked()
}

// UpsertGroup 新增或更新路由组。
func (r *Router) UpsertGroup(g Group) error {
	g.ID = strings.TrimSpace(g.ID)
	if !providerIDRe.MatchString(g.ID) {
		return fmt.Errorf("路由组 id 不合法：%q", g.ID)
	}
	if g.Strategy == "" {
		g.Strategy = "order"
	}
	if g.Strategy != "order" && g.Strategy != "rotate" && g.Strategy != "usage" {
		return fmt.Errorf("strategy 只能是 order / rotate / usage")
	}
	if len(g.Members) == 0 {
		return fmt.Errorf("members 不能为空")
	}
	clean := make([]string, 0, len(g.Members))
	for _, m := range g.Members {
		if m = strings.TrimSpace(m); m != "" {
			clean = append(clean, m)
		}
	}
	if len(clean) == 0 {
		return fmt.Errorf("members 不能为空")
	}
	g.Members = clean
	if g.Name == "" {
		g.Name = g.ID
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.groups[g.ID] = &g
	return r.saveRoutingLocked()
}

// DeleteGroup 删除路由组。
func (r *Router) DeleteGroup(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.groups[id]; !ok {
		return fmt.Errorf("路由组不存在：%s", id)
	}
	delete(r.groups, id)
	delete(r.groupCursor, id)
	return r.saveRoutingLocked()
}

// SetDefault 设置默认 provider/model。
func (r *Router) SetDefault(providerID, model string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	p := r.byID[providerID]
	if p == nil {
		return fmt.Errorf("provider 不存在：%s", providerID)
	}
	if strings.TrimSpace(model) == "" {
		model = p.DefaultModel
	}
	r.def = Ref{Provider: providerID, Model: model}
	return r.saveRoutingLocked()
}

func validateBaseURL(raw string) error {
	if raw == "" {
		return fmt.Errorf("base_url 不能为空")
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("base_url 必须是 http(s) 地址")
	}
	return nil
}

func (r *Router) saveProvidersLocked() error {
	persisted := make([]Provider, 0, len(r.providers))
	for _, p := range r.providers {
		if p.ID == "env" {
			continue
		}
		persisted = append(persisted, *p)
	}
	return writeJSONAtomic(filepath.Join(r.home, "providers.json"), map[string]any{"providers": persisted})
}

func (r *Router) saveRoutingLocked() error {
	groups := make([]Group, 0, len(r.groups))
	for _, g := range r.groups {
		groups = append(groups, *g)
	}
	data := routingFile{
		Default:         &r.def,
		Groups:          groups,
		MaxAttempts:     r.maxAttempts,
		CooldownSeconds: int(r.cooldown / 1e9),
		QueueTimeoutMS:  int(r.queueTimeout / 1e6),
	}
	return writeJSONAtomic(filepath.Join(r.home, "routing.json"), data)
}

func writeJSONAtomic(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
