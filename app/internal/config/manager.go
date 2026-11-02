package config

import (
	"os"
	"sync"
	"sync/atomic"
	"time"
)

// Manager 持有唯一配置快照，并负责整份替换（CF1、CF5）。
type Manager struct {
	startup Startup
	getenv  func(string) string
	now     func() time.Time

	snap atomic.Pointer[Snapshot]

	mu         sync.Mutex
	lastReload time.Time
	lastErr    string
}

// NewManager 构造管理器并加载第一份快照。配置非法时启动失败。
func NewManager(startup Startup, getenv func(string) string) (*Manager, error) {
	if getenv == nil {
		getenv = os.Getenv
	}
	m := &Manager{startup: startup, getenv: getenv, now: time.Now}
	snap, err := Build(startup, 1, getenv, m.now())
	if err != nil {
		return nil, err
	}
	m.snap.Store(snap)
	return m, nil
}

// Current 返回当前快照。调用方不应修改返回值。
func (m *Manager) Current() *Snapshot {
	return m.snap.Load()
}

// Reload 重新读取并校验全部配置；成功后原子切换，失败保留旧快照（CF4、CF5）。
func (m *Manager) Reload() (*Snapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	current := m.snap.Load()
	next, err := Build(m.startup, current.Version+1, m.getenv, m.now())
	m.lastReload = m.now()
	if err != nil {
		m.lastErr = err.Error()
		return nil, err
	}
	m.snap.Store(next)
	m.lastErr = ""
	return next, nil
}

// ManagerStatus 是配置层的可观测信息，不含任何密钥。
type ManagerStatus struct {
	Version    uint64    `json:"version"`
	LoadedAt   time.Time `json:"loaded_at"`
	LastReload time.Time `json:"last_reload,omitempty"`
	LastError  string    `json:"last_error,omitempty"`
}

// Status 返回当前版本与最近一次重载结果。
func (m *Manager) Status() ManagerStatus {
	snap := m.snap.Load()
	m.mu.Lock()
	defer m.mu.Unlock()
	return ManagerStatus{
		Version:    snap.Version,
		LoadedAt:   snap.LoadedAt,
		LastReload: m.lastReload,
		LastError:  m.lastErr,
	}
}
