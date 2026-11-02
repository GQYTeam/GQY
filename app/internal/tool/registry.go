package tool

import (
	"fmt"
	"sort"
	"sync"
)

// Registry 是工具注册表：名称全局唯一，重名报错，注销幂等（T3）。
type Registry struct {
	mu       sync.RWMutex
	core     map[string]Tool
	external map[string]Tool
}

// NewRegistry 构造空注册表。
func NewRegistry() *Registry {
	return &Registry{core: make(map[string]Tool), external: make(map[string]Tool)}
}

// Register 注册工具；重名返回错误，不静默覆盖。
func (r *Registry) Register(t Tool) error {
	if t == nil || t.Name() == "" {
		return fmt.Errorf("tool: 注册表收到空工具")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.core[t.Name()]; ok {
		return fmt.Errorf("tool: 工具 %s 已注册", t.Name())
	}
	if _, ok := r.external[t.Name()]; ok {
		return fmt.Errorf("tool: 工具 %s 已注册", t.Name())
	}
	if t.Source() == SourceExternal {
		r.external[t.Name()] = t
	} else {
		r.core[t.Name()] = t
	}
	return nil
}

// Unregister 注销工具；不存在返回 ErrNotFound，不 panic（T3）。
func (r *Registry) Unregister(name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.core[name]; ok {
		delete(r.core, name)
		return nil
	}
	if _, ok := r.external[name]; ok {
		delete(r.external, name)
		return nil
	}
	return fmt.Errorf("%w: %s", ErrNotFound, name)
}

// Get 返回工具。
func (r *Registry) Get(name string) (Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if t, ok := r.core[name]; ok {
		return t, true
	}
	if t, ok := r.external[name]; ok {
		return t, true
	}
	return nil, false
}

// Core 返回核心工具，按名称排序。
func (r *Registry) Core() []Tool { return r.list(r.core) }

// External 返回外部工具，按名称排序。
func (r *Registry) External() []Tool { return r.list(r.external) }

func (r *Registry) list(m map[string]Tool) []Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Tool, 0, len(m))
	for _, t := range m {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}
