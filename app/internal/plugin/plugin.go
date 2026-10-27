// 包 plugin：外部插件运行时（原生 stdio JSON-RPC + MCP stdio）。
//
// 设计见 docs/design/PluginRuntime.md。核心只认 Manifest / Adapter / Instance，
// 进程生命周期、超时、状态机都在这里，适配器只负责协议翻译。
package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"gqy/internal/tool"
)

var (
	ErrNotFound        = errors.New("插件不存在")
	ErrNotReady        = errors.New("插件未就绪")
	ErrExited          = errors.New("插件进程已退出")
	ErrDuplicate       = errors.New("插件 id 重复")
	ErrMissingService  = errors.New("缺少依赖服务")
	ErrServiceConflict = errors.New("服务已由其他插件提供")
	ErrHasDependents   = errors.New("仍有插件依赖该服务")
	idPattern          = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)
	toolNameClean      = regexp.MustCompile(`[^A-Za-z0-9_-]+`)
)

// Action 是插件声明或 MCP 发现的工具。
type Action struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Schema      map[string]any `json:"schema,omitempty"`
}

// Manifest 是插件清单。Dir 由发现阶段填入，不从 JSON 读取。
type Manifest struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Version     string            `json:"version"`
	Description string            `json:"description"`
	Protocol    string            `json:"protocol"`
	Command     []string          `json:"command"`
	Cwd         string            `json:"cwd"`
	Env         map[string]string `json:"env"`
	Socket      string            `json:"socket"` // unix: socket 路径；tcp: host:port
	TimeoutMS   int               `json:"timeout_ms"`
	Concurrency string            `json:"concurrency"`
	Actions     []Action          `json:"actions"`
	Provides    []string          `json:"provides"`
	Requires    []string          `json:"requires"`
	Dir         string            `json:"-"`
}

// Host 是适配器可用的宿主环境。
type Host struct {
	Home string
	Logf func(format string, args ...any)
}

// Info 是实例信息。
type Info struct {
	PID       int
	StartedAt time.Time
	Protocol  string
	Actions   []Action
}

// Instance 是一个已加载插件。
type Instance interface {
	Call(ctx context.Context, action string, args map[string]any) (string, error)
	Close(ctx context.Context) error
	Info() Info
}

// Adapter 把一种插件协议翻译成 Instance。
type Adapter interface {
	ID() string
	Match(Manifest) bool
	Load(ctx context.Context, m Manifest, host Host) (Instance, error)
}

// Status 是插件状态（前端用）。
type Status struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Version     string   `json:"version"`
	Description string   `json:"description"`
	Protocol    string   `json:"protocol"`
	State       string   `json:"state"` // discovered / loading / ready / closing / failed
	PID         int      `json:"pid,omitempty"`
	Actions     []Action `json:"actions"`
	Provides    []string `json:"provides,omitempty"`
	Requires    []string `json:"requires,omitempty"`
	Calls       int64    `json:"calls"`
	Failures    int64    `json:"failures"`
	LastError   string   `json:"lastError,omitempty"`
	StartedAt   string   `json:"startedAt,omitempty"`
}

type managed struct {
	mu        sync.Mutex
	manifest  Manifest
	state     string
	instance  Instance
	info      Info
	calls     int64
	failures  int64
	lastError string
	callMu    sync.Mutex // exclusive 插件串行
	toolNames []string
}

// Manager 管理插件实例表。
type Manager struct {
	mu        sync.RWMutex
	home      string
	dir       string
	registry  *tool.Registry
	adapters  []Adapter
	manifests map[string]Manifest
	plugins   map[string]*managed
	services  map[string]string // service name → plugin id
}

// NewManager 创建运行时。dir 为空时用 $GQY_HOME/plugins。
func NewManager(home string, reg *tool.Registry) *Manager {
	dir := filepath.Join(home, "plugins")
	m := &Manager{
		home:      home,
		dir:       dir,
		registry:  reg,
		manifests: map[string]Manifest{},
		plugins:   map[string]*managed{},
		services:  map[string]string{},
	}
	m.adapters = []Adapter{NativeAdapter{}, MCPAdapter{}, RemoteAdapter{}}
	return m
}

// Dir 返回插件目录。
func (m *Manager) Dir() string { return m.dir }

// Host 返回适配器宿主环境。
func (m *Manager) Host() Host {
	return Host{
		Home: m.home,
		Logf: func(format string, args ...any) {
			// 日志交给调用方；这里保持安静，避免刷屏。
			_ = format
			_ = args
		},
	}
}

// Discover 扫描 $GQY_HOME/plugins/*/manifest.json。
func (m *Manager) Discover() []error {
	entries, err := os.ReadDir(m.dir)
	if err != nil {
		return nil
	}
	var errs []error
	seen := map[string]string{}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(m.dir, e.Name())
		raw, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
		if err != nil {
			continue
		}
		var manifest Manifest
		if err := json.Unmarshal(raw, &manifest); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", dir, err))
			continue
		}
		manifest.Dir = dir
		if err := normalizeManifest(&manifest); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", dir, err))
			continue
		}
		if prev, ok := seen[manifest.ID]; ok {
			errs = append(errs, fmt.Errorf("%w: %s 与 %s", ErrDuplicate, prev, dir))
			continue
		}
		seen[manifest.ID] = dir
		m.manifests[manifest.ID] = manifest
		if _, ok := m.plugins[manifest.ID]; !ok {
			m.plugins[manifest.ID] = &managed{manifest: manifest, state: "discovered"}
		}
	}
	return errs
}

func normalizeManifest(m *Manifest) error {
	m.ID = strings.TrimSpace(m.ID)
	if !idPattern.MatchString(m.ID) {
		return fmt.Errorf("id 不合法：%q", m.ID)
	}
	if m.Name == "" {
		m.Name = m.ID
	}
	if m.Protocol == "" {
		m.Protocol = "gqy-jsonrpc-v1"
	}
	if m.Protocol == "unix" || m.Protocol == "tcp" {
		if strings.TrimSpace(m.Socket) == "" {
			return fmt.Errorf("%s 协议需要 socket 地址", m.Protocol)
		}
	} else if len(m.Command) == 0 {
		return fmt.Errorf("command 不能为空")
	}
	if m.TimeoutMS <= 0 {
		m.TimeoutMS = 10000
	}
	if m.TimeoutMS > 120000 {
		m.TimeoutMS = 120000
	}
	if m.Concurrency == "" {
		m.Concurrency = "concurrent"
	}
	if m.Concurrency != "concurrent" && m.Concurrency != "exclusive" {
		return fmt.Errorf("concurrency 只能是 concurrent / exclusive")
	}
	return nil
}

// List 返回所有已发现插件。
func (m *Manager) List() []Status {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Status, 0, len(m.plugins))
	for _, p := range m.plugins {
		p.mu.Lock()
		st := Status{
			ID: p.manifest.ID, Name: p.manifest.Name, Version: p.manifest.Version,
			Description: p.manifest.Description, Protocol: p.manifest.Protocol,
			State: p.state, Calls: p.calls, Failures: p.failures,
			Provides:  append([]string{}, p.manifest.Provides...),
			Requires:  append([]string{}, p.manifest.Requires...),
			LastError: p.lastError, Actions: append([]Action{}, p.manifest.Actions...),
		}
		if p.instance != nil {
			st.PID = p.info.PID
			if !p.info.StartedAt.IsZero() {
				st.StartedAt = p.info.StartedAt.UTC().Format(time.RFC3339)
			}
			if len(p.info.Actions) > 0 {
				st.Actions = append([]Action{}, p.info.Actions...)
			}
		}
		p.mu.Unlock()
		out = append(out, st)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Load 加载插件（幂等）。
func (m *Manager) Load(ctx context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.plugins[id]
	if !ok {
		return ErrNotFound
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	switch p.state {
	case "ready":
		return nil
	case "loading":
		return fmt.Errorf("插件 %s 正在加载", id)
	}
	for _, need := range p.manifest.Requires {
		if m.services[need] == "" {
			p.state = "failed"
			p.lastError = "缺少依赖服务：" + need
			return fmt.Errorf("%w: %s", ErrMissingService, need)
		}
	}
	adapter := m.adapterFor(p.manifest)
	if adapter == nil {
		p.state = "failed"
		p.lastError = "没有匹配的适配器：" + p.manifest.Protocol
		return errors.New(p.lastError)
	}
	p.state = "loading"
	p.lastError = ""
	inst, err := adapter.Load(ctx, p.manifest, m.Host())
	if err != nil {
		p.state = "failed"
		p.lastError = truncate(err.Error(), 500)
		return err
	}
	p.instance = inst
	p.info = inst.Info()
	p.state = "ready"
	if len(p.info.Actions) > 0 {
		p.manifest.Actions = append([]Action{}, p.info.Actions...)
	}
	if err := m.registerToolsLocked(p); err != nil {
		_ = inst.Close(context.Background())
		p.instance = nil
		p.state = "failed"
		p.lastError = err.Error()
		return err
	}
	if err := m.registerServicesLocked(p); err != nil {
		for _, name := range p.toolNames {
			m.registry.Unregister(name)
		}
		p.toolNames = nil
		_ = inst.Close(context.Background())
		p.instance = nil
		p.state = "failed"
		p.lastError = err.Error()
		return err
	}
	return nil
}

func (m *Manager) registerServicesLocked(p *managed) error {
	for _, svc := range p.manifest.Provides {
		if owner := m.services[svc]; owner != "" && owner != p.manifest.ID {
			return fmt.Errorf("%w: %s 由 %s 提供", ErrServiceConflict, svc, owner)
		}
	}
	for _, svc := range p.manifest.Provides {
		m.services[svc] = p.manifest.ID
	}
	return nil
}

func (m *Manager) releaseServicesLocked(p *managed) {
	for _, svc := range p.manifest.Provides {
		if m.services[svc] == p.manifest.ID {
			delete(m.services, svc)
		}
	}
}

// Unload 卸载插件（幂等）。
func (m *Manager) Unload(ctx context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.plugins[id]
	if !ok {
		return ErrNotFound
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.state == "discovered" || p.state == "failed" || p.instance == nil {
		p.state = "discovered"
		return ErrNotFound
	}
	for _, other := range m.plugins {
		if other.manifest.ID == p.manifest.ID || other.state != "ready" {
			continue
		}
		for _, need := range other.manifest.Requires {
			for _, svc := range p.manifest.Provides {
				if need == svc {
					return fmt.Errorf("%w: %s 依赖 %s", ErrHasDependents, other.manifest.ID, svc)
				}
			}
		}
	}
	p.state = "closing"
	for _, name := range p.toolNames {
		m.registry.Unregister(name)
	}
	p.toolNames = nil
	inst := p.instance
	p.instance = nil
	p.state = "discovered"
	if inst != nil {
		if err := inst.Close(ctx); err != nil {
			p.lastError = truncate(err.Error(), 500)
			return err
		}
	}
	m.releaseServicesLocked(p)
	return nil
}

// Reload 重新扫描 manifest；已加载的插件保留实例。
func (m *Manager) Reload() []error {
	return m.Discover()
}

// Call 调用插件动作。
func (m *Manager) Call(ctx context.Context, id, action string, args map[string]any) (string, error) {
	m.mu.RLock()
	p, ok := m.plugins[id]
	m.mu.RUnlock()
	if !ok {
		return "", ErrNotFound
	}
	p.mu.Lock()
	if p.state != "ready" || p.instance == nil {
		p.mu.Unlock()
		return "", ErrNotReady
	}
	inst := p.instance
	timeout := time.Duration(p.manifest.TimeoutMS) * time.Millisecond
	exclusive := p.manifest.Concurrency == "exclusive"
	p.calls++
	p.mu.Unlock()

	if exclusive {
		p.callMu.Lock()
		defer p.callMu.Unlock()
	}
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	out, err := inst.Call(callCtx, action, args)
	if err != nil {
		p.mu.Lock()
		p.failures++
		p.lastError = truncate(err.Error(), 500)
		p.mu.Unlock()
	}
	return out, err
}

// Close 关闭所有插件。
func (m *Manager) Close() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, p := range m.plugins {
		p.mu.Lock()
		if p.instance != nil {
			for _, name := range p.toolNames {
				m.registry.Unregister(name)
			}
			_ = p.instance.Close(ctx)
			p.instance = nil
			p.state = "discovered"
		}
		p.mu.Unlock()
		_ = id
	}
	m.services = map[string]string{}
}

func (m *Manager) adapterFor(manifest Manifest) Adapter {
	for _, a := range m.adapters {
		if a.Match(manifest) {
			return a
		}
	}
	return nil
}

/* ---------------- 工具注册 ---------------- */

func (m *Manager) registerToolsLocked(p *managed) error {
	names := make([]string, 0, len(p.manifest.Actions))
	tools := make([]*pluginTool, 0, len(p.manifest.Actions))
	for _, a := range p.manifest.Actions {
		name := toolName(p.manifest.ID, a.Name)
		if name == "" {
			continue
		}
		if _, exists := m.registry.Get(name); exists {
			return fmt.Errorf("工具名冲突：%s", name)
		}
		names = append(names, name)
		tools = append(tools, &pluginTool{
			manager: m, pluginID: p.manifest.ID, pluginName: p.manifest.Name,
			action: a, name: name,
		})
	}
	for _, t := range tools {
		if err := m.registry.Register(t); err != nil {
			for _, n := range p.toolNames {
				m.registry.Unregister(n)
			}
			return err
		}
	}
	p.toolNames = append(p.toolNames, names...)
	return nil
}

func toolName(pluginID, action string) string {
	clean := toolNameClean.ReplaceAllString(action, "_")
	clean = strings.Trim(clean, "_")
	if clean == "" {
		return ""
	}
	name := pluginID + "__" + clean
	if len(name) > 64 {
		name = name[:64]
	}
	return name
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
