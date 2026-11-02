package provider

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

// Adapter 是一个协议适配器。新增社区 = 新增 Adapter + 注册，核心不改（G4）。
type Adapter interface {
	Protocol() string
	// Chat 发起一次非流式调用。
	Chat(ctx context.Context, endpoint Endpoint, req *Request) (*Response, error)
	// Stream 发起一次流式调用，按序产生统一事件；返回聚合结果。
	Stream(ctx context.Context, endpoint Endpoint, req *Request, emit func(Event) error) (*Response, error)
}

// Endpoint 是一次出站调用的目标信息，来自配置，不包含任何协议细节。
type Endpoint struct {
	ProviderID string
	BaseURL    string
	APIKey     string
	Protocol   string
	UserAgent  string
}

// NormalizeProtocol 把空协议归一为 openai-chat。
func NormalizeProtocol(protocol string) string {
	protocol = strings.ToLower(strings.TrimSpace(protocol))
	if protocol == "" {
		return "openai-chat"
	}
	return protocol
}

// Registry 按协议名查找适配器。
type Registry struct {
	mu       sync.RWMutex
	adapters map[string]Adapter
}

// NewRegistry 构造空注册表。
func NewRegistry(adapters ...Adapter) *Registry {
	r := &Registry{adapters: make(map[string]Adapter, len(adapters))}
	for _, a := range adapters {
		_ = r.Register(a)
	}
	return r
}

// Register 注册适配器；协议名重复返回错误，不静默覆盖。
func (r *Registry) Register(a Adapter) error {
	if a == nil {
		return fmt.Errorf("provider: 适配器为 nil")
	}
	protocol := NormalizeProtocol(a.Protocol())
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.adapters[protocol]; exists {
		return fmt.Errorf("provider: 协议 %s 已注册", protocol)
	}
	r.adapters[protocol] = a
	return nil
}

// Get 返回协议对应的适配器。
func (r *Registry) Get(protocol string) (Adapter, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	a, ok := r.adapters[NormalizeProtocol(protocol)]
	return a, ok
}

// Protocols 返回已注册的协议名，按注册顺序不保证。
func (r *Registry) Protocols() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.adapters))
	for protocol := range r.adapters {
		out = append(out, protocol)
	}
	return out
}
