// Package plugin 实现插件运行时：四层边界、生命周期、并发、回收与可观测。
//
// 设计依据：docs/designs/04-插件运行时.md（P1–P12）、
// docs/designs/11-安全与沙盒.md（SEC5、SEC6）。
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
	"sync/atomic"
	"time"

	"gqy/internal/metrics"
	"gqy/internal/tool"
)

// 统一错误（P1）。
var (
	ErrNotFound   = errors.New("not_found")
	ErrNotReady   = errors.New("not_ready")
	ErrTimeout    = errors.New("timeout")
	ErrInvalidArg = errors.New("invalid_argument")
	ErrCallFailed = errors.New("call_failed")
	ErrExited     = errors.New("exited")
	ErrCanceled   = errors.New("canceled")
)

// State 是生命周期状态（P5）。
type State string

const (
	StateDiscovered State = "discovered"
	StateLoading    State = "loading"
	StateReady      State = "ready"
	StateClosing    State = "closing"
	StateFailed     State = "failed"
)

// 协议常量（P3）。
const (
	ProtocolNative = "gqy-jsonrpc-v1"
	ProtocolMCP    = "mcp"
	ProtocolUnix   = "unix"
	ProtocolTCP    = "tcp"
)

var (
	idRe         = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)
	versionRe    = regexp.MustCompile(`^\d{1,4}\.\d{1,4}\.\d{1,4}$`)
	communityRe  = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)
	serviceRe    = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,63}$`)
	actionNameRe = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_-]{0,63}$`)
)

// Action 是原生协议声明的一个动作（P3）。
type Action struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Schema      map[string]any `json:"schema"`
}

// Manifest 是插件清单（P3）。
// Setting 是插件可配置项（PL3）：WebUI 据此渲染设置表单，值存 plugin-settings。
type Setting struct {
	Key         string   `json:"key"`
	Title       string   `json:"title"`
	Type        string   `json:"type"` // string | number | boolean | select
	Default     any      `json:"default,omitempty"`
	Options     []string `json:"options,omitempty"`
	Description string   `json:"description,omitempty"`
}

type Manifest struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Version     string            `json:"version"`
	Description string            `json:"description"`
	Protocol    string            `json:"protocol"`
	Community   string            `json:"community"`
	Command     []string          `json:"command"`
	Cwd         string            `json:"cwd"`
	Env         map[string]string `json:"env"`
	TimeoutMS   int               `json:"timeout_ms"`
	Concurrency string            `json:"concurrency"`
	Actions     []Action          `json:"actions"`
	Address     string            `json:"address"`
	Provides    []string          `json:"provides"`
	Requires    []string          `json:"requires"`
	// Settings 是插件声明的可配置项（PL3），可空。
	Settings []Setting `json:"settings,omitempty"`
	// Source 声明来源：installed（安装，只读）| local（本地自建，可改）。缺省 local。
	Source string `json:"source,omitempty"`
	// Expose 控制该插件动作是否暴露给模型（默认 true；HR2）。
	Expose *bool `json:"expose,omitempty"`

	Dir string `json:"-"`
	// ReadOnly 由来源派生（PL2）：installed 为 true，禁止改动插件本体。
	ReadOnly bool `json:"-"`
	// Values 是加载时计算好的生效设置（默认值 + 已保存值），随 initialize 传给插件（PL3）。
	Values map[string]any `json:"-"`
}

// ValidID 判断插件 id 合法。
func ValidID(id string) bool { return idRe.MatchString(id) }

// timeout 把毫秒转成 time.Duration。
func timeout(ms int) time.Duration {
	if ms <= 0 {
		ms = 10000
	}
	return time.Duration(ms) * time.Millisecond
}

// ParseManifest 解析并校验 manifest（未知字段忽略；校验失败整体拒绝）。
func ParseManifest(data []byte) (*Manifest, error) {
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("manifest: JSON 解析失败: %w", err)
	}
	m.ID = strings.TrimSpace(m.ID)
	m.Name = strings.TrimSpace(m.Name)
	m.Version = strings.TrimSpace(m.Version)
	if !idRe.MatchString(m.ID) {
		return nil, fmt.Errorf("manifest: id 非法: %q", m.ID)
	}
	if m.Name == "" || len([]rune(m.Name)) > 64 {
		return nil, fmt.Errorf("manifest: name 必填且不超过 64 字符")
	}
	if !versionRe.MatchString(m.Version) {
		return nil, fmt.Errorf("manifest: version 非法: %q", m.Version)
	}
	if m.Protocol == "" {
		if m.Community != "" && !communityRe.MatchString(m.Community) {
			return nil, fmt.Errorf("manifest: community 非法: %q", m.Community)
		}
	} else {
		switch m.Protocol {
		case ProtocolNative, ProtocolMCP, ProtocolUnix, ProtocolTCP:
		default:
			return nil, fmt.Errorf("manifest: 未知 protocol %q", m.Protocol)
		}
	}
	switch m.Protocol {
	case ProtocolNative, ProtocolMCP:
		if len(m.Command) == 0 {
			return nil, fmt.Errorf("manifest: %s 需要 command", m.Protocol)
		}
	case ProtocolUnix, ProtocolTCP:
		if m.Address == "" {
			return nil, fmt.Errorf("manifest: %s 需要 address", m.Protocol)
		}
	}
	if m.Cwd != "" && strings.Contains(filepath.Clean(m.Cwd), "..") {
		return nil, fmt.Errorf("manifest: cwd 不得包含上级路径")
	}
	if m.TimeoutMS == 0 {
		m.TimeoutMS = 10000
	}
	if m.TimeoutMS < 1 || m.TimeoutMS > 120000 {
		return nil, fmt.Errorf("manifest: timeout_ms 必须在 1–120000")
	}
	switch m.Concurrency {
	case "", "concurrent", "single-channel", "exclusive":
	default:
		return nil, fmt.Errorf("manifest: concurrency 非法: %q", m.Concurrency)
	}
	if m.Concurrency == "" {
		m.Concurrency = "concurrent"
	}
	for i := range m.Actions {
		a := &m.Actions[i]
		a.Name = strings.TrimSpace(a.Name)
		a.Description = strings.TrimSpace(a.Description)
		if !actionNameRe.MatchString(a.Name) {
			return nil, fmt.Errorf("manifest: action name 非法: %q", a.Name)
		}
		if a.Description == "" || len([]rune(a.Description)) > 200 {
			return nil, fmt.Errorf("manifest: action %s description 非法", a.Name)
		}
		if a.Schema == nil || a.Schema["type"] != "object" {
			return nil, fmt.Errorf("manifest: action %s schema 根 type 必须为 object", a.Name)
		}
	}
	m.Provides = dedupeStrings(m.Provides, serviceRe)
	m.Requires = dedupeStrings(m.Requires, serviceRe)
	return &m, nil
}

func dedupeStrings(in []string, re *regexp.Regexp) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" || !re.MatchString(s) || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

// Instance 是运行中（或已发现）的插件实例。
type Instance struct {
	Manifest *Manifest

	mu        sync.Mutex
	state     State
	impl      adapterInstance
	adapter   Adapter
	sem       chan struct{}
	wg        sync.WaitGroup
	pid       int
	startedAt time.Time
	calls     int64
	failures  int64
	lastError string
}

func newInstance(m *Manifest, adapter Adapter) *Instance {
	inst := &Instance{Manifest: m, adapter: adapter, state: StateDiscovered}
	if m.Concurrency != "concurrent" {
		inst.sem = make(chan struct{}, 1)
	}
	return inst
}

func (i *Instance) State() State {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.state
}

// Adapter 是协议适配器（P4）。
type Adapter interface {
	Protocol() string
	// Load 建立连接并完成初始化，返回可调用的实例句柄。
	Load(ctx context.Context, m *Manifest, dir string) (adapterInstance, error)
	// Match 判断社区名是否匹配（P2 第二级）。
	Match(community string) bool
}

// adapterInstance 是加载成功的插件句柄。
type adapterInstance interface {
	Call(ctx context.Context, action string, args map[string]any) (string, error)
	Actions() []Action
	Close(ctx context.Context) error
	PID() int
}

// Manager 管理插件发现、加载、调用、卸载与状态（P1/P5/P9/P10/P12）。
type Manager struct {
	home     string
	mu       sync.Mutex
	plugins  map[string]*Instance
	adapters map[string]Adapter
	orphans  atomic.Int64
	// onChange 在插件集合/状态变化后回调（HR2）：用于重建工具表，不需要重启服务。
	onChange func()
}

// SetOnChange 注册插件变化回调；回调在锁外执行，允许重活。
func (m *Manager) SetOnChange(fn func()) {
	m.mu.Lock()
	m.onChange = fn
	m.mu.Unlock()
}

// fireChange 触发变更回调（须在锁外调用）。
func (m *Manager) fireChange() {
	m.mu.Lock()
	fn := m.onChange
	m.mu.Unlock()
	if fn != nil {
		fn()
	}
}

// NewManager 构造管理器并注册三种适配器。
func NewManager(home string) *Manager {
	m := &Manager{home: home, plugins: make(map[string]*Instance), adapters: make(map[string]Adapter)}
	remote := &RemoteAdapter{}
	for _, a := range []Adapter{&NativeAdapter{}, &MCPAdapter{}, remote} {
		m.adapters[a.Protocol()] = a
	}
	// P4：unix / tcp 都只连不拉起，由同一个 RemoteAdapter 服务。
	m.adapters[ProtocolTCP] = remote
	return m
}

// OrphanEvents 返回孤儿兜底触发次数（恒为 0，非零告警）。
func (m *Manager) OrphanEvents() int64 { return m.orphans.Load() }

func (m *Manager) pluginsDir() string { return filepath.Join(m.home, "plugins") }

// Scan 扫描 $GQY_HOME/plugins/*/manifest.json（P9：重扫不重启运行中实例）。
func (m *Manager) Scan() error {
	entries, err := os.ReadDir(m.pluginsDir())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		id := entry.Name()
		data, err := os.ReadFile(filepath.Join(m.pluginsDir(), id, "manifest.json"))
		if err != nil {
			continue
		}
		manifest, err := ParseManifest(data)
		if err != nil || manifest.ID != id {
			continue
		}
		manifest.Dir = filepath.Join(m.pluginsDir(), id)
		deriveSource(manifest)

		m.mu.Lock()
		existing, ok := m.plugins[id]
		if ok {
			if existing.Manifest.Version == manifest.Version && existing.State() != StateFailed {
				m.mu.Unlock()
				continue // 重扫不重启
			}
			// 新版本：先卸载旧实例再登记新 manifest。
			m.mu.Unlock()
			_ = m.Unload(context.Background(), id)
			m.mu.Lock()
		}
		m.plugins[id] = newInstance(manifest, m.selectAdapter(manifest))
		m.mu.Unlock()
	}
	m.fireChange()
	return nil
}

func (m *Manager) selectAdapter(manifest *Manifest) Adapter {
	if manifest.Protocol != "" {
		return m.adapters[manifest.Protocol]
	}
	var hit Adapter
	for _, a := range m.adapters {
		if a.Match(manifest.Community) {
			if hit != nil {
				return nil // 歧义报错
			}
			hit = a
		}
	}
	return hit
}

// Load 加载插件（P5）。
func (m *Manager) Load(ctx context.Context, id string) error {
	m.mu.Lock()
	inst, ok := m.plugins[id]
	m.mu.Unlock()
	if !ok {
		return fmt.Errorf("%w: 插件 %s 未发现", ErrNotFound, id)
	}
	if inst.adapter == nil {
		return fmt.Errorf("%w: 无适配器匹配", ErrNotFound)
	}

	inst.mu.Lock()
	switch inst.state {
	case StateReady:
		inst.mu.Unlock()
		return nil // 幂等
	case StateLoading, StateClosing:
		inst.mu.Unlock()
		return fmt.Errorf("%w: %s", ErrNotReady, inst.state)
	}
	inst.state = StateLoading
	inst.mu.Unlock()

	if err := m.checkRequiresLocked(id); err != nil {
		inst.mu.Lock()
		inst.state = StateDiscovered
		inst.mu.Unlock()
		return err
	}

	inst.Manifest.Values = m.effectiveSettings(inst.Manifest)
	settings, err := m.EffectiveSettings(id)
	if err != nil {
		inst.mu.Lock()
		inst.state = StateDiscovered
		inst.lastError = err.Error()
		inst.mu.Unlock()
		return err
	}
	manifest := *inst.Manifest
	manifest.Env = cloneEnv(inst.Manifest.Env)
	if len(settings) > 0 {
		data, err := json.Marshal(settings)
		if err != nil {
			inst.mu.Lock()
			inst.state = StateDiscovered
			inst.lastError = err.Error()
			inst.mu.Unlock()
			return err
		}
		manifest.Env["GQY_PLUGIN_SETTINGS"] = string(data)
	}
	impl, err := inst.adapter.Load(ctx, &manifest, manifest.Dir)
	if err != nil {
		// P5：加载失败回滚到 discovered，不留半成品；下次 Load 从零开始。
		inst.mu.Lock()
		inst.state = StateDiscovered
		inst.impl = nil
		inst.pid = 0
		inst.lastError = err.Error()
		inst.mu.Unlock()
		return err
	}

	inst.mu.Lock()
	inst.impl = impl
	inst.pid = impl.PID()
	inst.startedAt = time.Now()
	inst.state = StateReady
	inst.mu.Unlock()
	m.fireChange()
	return nil
}

func (m *Manager) checkRequiresLocked(id string) error {
	inst := m.plugins[id]
	for _, req := range inst.Manifest.Requires {
		found := builtinService(req)
		for otherID, other := range m.plugins {
			if otherID == id {
				continue
			}
			if other.State() == StateReady && contains(other.Manifest.Provides, req) {
				found = true
			}
		}
		if !found {
			return fmt.Errorf("%w: 缺依赖 %s", ErrInvalidArg, req)
		}
	}
	return nil
}

func builtinService(name string) bool {
	return name == "text-echo"
}

// Unload 卸载插件（P5/P8/P10）。
func (m *Manager) Unload(ctx context.Context, id string) error {
	m.mu.Lock()
	inst, ok := m.plugins[id]
	m.mu.Unlock()
	if !ok {
		return fmt.Errorf("%w: %s", ErrNotFound, id)
	}

	inst.mu.Lock()
	if inst.state == StateDiscovered {
		inst.mu.Unlock()
		return nil // 幂等：保持 discovered，不再从实例表摘除（删除走 Delete）
	}
	if inst.state == StateClosing {
		inst.mu.Unlock()
		return nil // 幂等
	}
	if inst.state == StateFailed {
		inst.state = StateDiscovered
		inst.impl = nil
		inst.pid = 0
		inst.mu.Unlock()
		return nil // 失败实例卸载后回到 discovered，可再次 Load
	}
	inst.state = StateClosing
	inst.mu.Unlock()

	if err := m.checkDependentsLocked(id); err != nil {
		inst.mu.Lock()
		inst.state = StateReady
		inst.mu.Unlock()
		return err
	}

	inst.wg.Wait()
	if inst.impl != nil {
		closeCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = inst.impl.Close(closeCtx)
	}
	inst.mu.Lock()
	inst.state = StateDiscovered
	inst.impl = nil
	inst.pid = 0
	inst.startedAt = time.Time{}
	inst.mu.Unlock()
	m.fireChange()
	return nil // P9：卸载 = 停止实例；删除才移除目录与实例表项
}

func (m *Manager) checkDependentsLocked(id string) error {
	inst := m.plugins[id]
	var deps []string
	for otherID, other := range m.plugins {
		if otherID == id || other.State() != StateReady {
			continue
		}
		for _, req := range other.Manifest.Requires {
			if contains(inst.Manifest.Provides, req) {
				deps = append(deps, otherID)
			}
		}
	}
	if len(deps) > 0 {
		return fmt.Errorf("%w: 有依赖者 %v", ErrInvalidArg, deps)
	}
	return nil
}

// Call 调用插件动作（P1/P6/P7）。
func (m *Manager) Call(ctx context.Context, id, action string, args map[string]any) (string, error) {
	m.mu.Lock()
	inst, ok := m.plugins[id]
	m.mu.Unlock()
	if !ok {
		return "", fmt.Errorf("%w: %s", ErrNotFound, id)
	}

	inst.mu.Lock()
	if inst.state != StateReady {
		inst.mu.Unlock()
		return "", fmt.Errorf("%w: %s", ErrNotReady, inst.state)
	}
	if inst.sem != nil {
		select {
		case inst.sem <- struct{}{}:
		default:
			inst.mu.Unlock()
			select {
			case inst.sem <- struct{}{}:
			case <-ctx.Done():
				return "", ErrCanceled
			}
			inst.mu.Lock()
		}
	}
	inst.mu.Unlock()
	defer func() {
		if inst.sem != nil {
			<-inst.sem
		}
	}()

	inst.wg.Add(1)
	defer inst.wg.Done()

	timeout := time.Duration(inst.Manifest.TimeoutMS) * time.Millisecond
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	start := time.Now()
	content, err := inst.impl.Call(callCtx, action, args)
	elapsed := time.Since(start).Seconds()
	inst.mu.Lock()
	inst.calls++
	if err != nil {
		inst.failures++
		inst.lastError = err.Error()
	}
	inst.mu.Unlock()

	status := "ok"
	if err != nil {
		status = "error"
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			status = "timeout"
		case errors.Is(err, context.Canceled):
			status = "canceled"
		}
		metrics.Inc("gqy_plugin_failures_total", "Plugin call failures", map[string]string{
			"plugin": id, "action": action,
		})
	}
	metrics.Inc("gqy_plugin_calls_total", "Plugin calls", map[string]string{
		"plugin": id, "action": action, "status": status,
	})
	metrics.Observe("gqy_plugin_call_duration_seconds", "Plugin call duration in seconds", map[string]string{
		"plugin": id, "action": action,
	}, elapsed)

	if err != nil {
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			return "", ErrTimeout
		case errors.Is(err, context.Canceled):
			return "", ErrCanceled
		default:
			return "", err
		}
	}
	return content, nil
}

// Status 返回全部插件状态（P12）。
func (m *Manager) Status() []PluginStatus {
	m.mu.Lock()
	ids := make([]string, 0, len(m.plugins))
	for id := range m.plugins {
		ids = append(ids, id)
	}
	m.mu.Unlock()
	sort.Strings(ids)

	out := make([]PluginStatus, 0, len(ids))
	for _, id := range ids {
		m.mu.Lock()
		inst := m.plugins[id]
		m.mu.Unlock()
		inst.mu.Lock()
		actions := make([]string, 0, len(inst.Manifest.Actions))
		for _, a := range inst.Manifest.Actions {
			actions = append(actions, a.Name)
		}
		status := PluginStatus{
			ID:          inst.Manifest.ID,
			Name:        inst.Manifest.Name,
			Version:     inst.Manifest.Version,
			Protocol:    inst.Manifest.Protocol,
			State:       string(inst.state),
			Concurrency: inst.Manifest.Concurrency,
			PID:         inst.pid,
			StartedAt:   inst.startedAt,
			Calls:       inst.calls,
			Failures:    inst.failures,
			LastError:   inst.lastError,
			Actions:     actions,
			Provides:    inst.Manifest.Provides,
			Requires:    inst.Manifest.Requires,
			Source:      inst.Manifest.Source,
			ReadOnly:    inst.Manifest.ReadOnly,
			Settings:    inst.Manifest.Settings,
			HasSettings: len(inst.Manifest.Settings) > 0,
		}
		inst.mu.Unlock()
		out = append(out, status)
	}
	return out
}

// RegisterTools 把已就绪插件的动作注册成外部工具（P11）。
// asCore 为 true 时按核心来源注册（供 006 注入开关使用）。
func (m *Manager) RegisterTools(reg *tool.Registry, asCore bool) {
	for _, inst := range m.plugins {
		inst.mu.Lock()
		ready := inst.state == StateReady
		inst.mu.Unlock()
		if !ready {
			continue
		}
		// 插件可显式声明 expose:false 隐藏自己的动作（HR2）。
		if inst.Manifest.Expose != nil && !*inst.Manifest.Expose {
			continue
		}
		for _, a := range inst.impl.Actions() {
			_ = reg.Register(&ExternalTool{
				PluginID: inst.Manifest.ID,
				Action:   a,
				asCore:   asCore,
				manager:  m,
			})
		}
	}
}

// PluginStatus 是 /api/plugins 的一项。
type PluginStatus struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Version     string    `json:"version"`
	Protocol    string    `json:"protocol"`
	State       string    `json:"state"`
	Concurrency string    `json:"concurrency"`
	PID         int       `json:"pid"`
	StartedAt   time.Time `json:"started_at,omitempty"`
	Calls       int64     `json:"calls"`
	Failures    int64     `json:"failures"`
	LastError   string    `json:"last_error"`
	Actions     []string  `json:"actions"`
	Provides    []string  `json:"provides"`
	Requires    []string  `json:"requires"`
	Source      string    `json:"source"`
	ReadOnly    bool      `json:"read_only"`
	Settings    []Setting `json:"settings,omitempty"`
	HasSettings bool      `json:"has_settings"`
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// Close 关闭全部已加载插件并回收进程组（P8）。
// 幂等；单个插件关闭失败不影响其余，错误聚合返回。
func (m *Manager) Close(ctx context.Context) error {
	m.mu.Lock()
	ids := make([]string, 0, len(m.plugins))
	for id := range m.plugins {
		ids = append(ids, id)
	}
	m.mu.Unlock()
	sort.Strings(ids)

	var errs []string
	for _, id := range ids {
		if err := m.Unload(ctx, id); err != nil {
			errs = append(errs, id+": "+err.Error())
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("plugin: 关闭失败: %s", strings.Join(errs, "; "))
	}
	return nil
}

// 插件来源（PL2）：installed 为「在线安装」，只读；local 为「GQY 自主创建」，可改。
const (
	SourceInstalled = "installed"
	SourceLocal     = "local"
)

// InstalledMarker 是安装标记文件：存在即视为在线安装，插件本体只读。
const InstalledMarker = ".gqy-installed"

// deriveSource 由标记文件与 manifest.source 派生只读语义（PL2）。
func deriveSource(mf *Manifest) {
	if mf.Source == "" {
		mf.Source = SourceLocal
	}
	if _, err := os.Stat(filepath.Join(mf.Dir, InstalledMarker)); err == nil || mf.Source == SourceInstalled {
		mf.Source = SourceInstalled
		mf.ReadOnly = true
		return
	}
	mf.Source = SourceLocal
	mf.ReadOnly = false
}

func cloneEnv(in map[string]string) map[string]string {
	out := make(map[string]string, len(in)+1)
	for k, v := range in {
		out[k] = v
	}
	return out
}
