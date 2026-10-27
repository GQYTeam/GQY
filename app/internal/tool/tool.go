// 包 tool：工具契约与注册表。
//
// 对齐 docs/AboutPlugin.md 的「统一模型」：核心只认 Tool 接口，
// 工具来源（内置 / 插件 / 远程）对 agent 透明。
package tool

import (
	"context"
	"fmt"
	"sync"
)

// Kind 标记工具来源：核心工具（agent 内置）还是外部工具（插件/skill/mcp）。
type Kind int

const (
	KindCore Kind = iota
	KindExternal
)

// Tool 是 agent 可调用的最小契约。
type Tool interface {
	Name() string
	Description() string
	// Schema 返回 JSON Schema（parameters 部分），用于声明给模型。
	Schema() map[string]any
	// Execute 执行工具，返回给模型回填的结果文本。
	Execute(ctx context.Context, args map[string]any) (string, error)
	// Kind 返回工具类别（核心 / 外部）。
	Kind() Kind
}

// Registry 是线程安全的工具注册表（读多写少，读写锁足够）。
type Registry struct {
	mu    sync.RWMutex
	tools map[string]Tool
}

func NewRegistry() *Registry {
	return &Registry{tools: map[string]Tool{}}
}

// Register 注册工具；重名返回错误（对齐 AboutPlugin.md：注册重复名必须报错）。
func (r *Registry) Register(t Tool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.tools[t.Name()]; exists {
		return fmt.Errorf("工具 %q 已注册", t.Name())
	}
	r.tools[t.Name()] = t
	return nil
}

func (r *Registry) Get(name string) (Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	return t, ok
}

// Unregister 注销工具；不存在时返回 false。
func (r *Registry) Unregister(name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.tools[name]; !ok {
		return false
	}
	delete(r.tools, name)
	return true
}

// List 返回所有工具。
func (r *Registry) List() []Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Tool, 0, len(r.tools))
	for _, t := range r.tools {
		out = append(out, t)
	}
	return out
}

// ListCore 返回核心工具。
func (r *Registry) ListCore() []Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Tool, 0, len(r.tools))
	for _, t := range r.tools {
		if t.Kind() == KindCore {
			out = append(out, t)
		}
	}
	return out
}

// ListExternal 返回外部工具。
func (r *Registry) ListExternal() []Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Tool, 0, len(r.tools))
	for _, t := range r.tools {
		if t.Kind() == KindExternal {
			out = append(out, t)
		}
	}
	return out
}
