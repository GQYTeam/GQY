package config

// 配置文件的轮询热加载（HR1）：不引入第三方依赖，按 mtime+size 变化触发整份 Reload。
// 覆盖 providers.json / routing.json / settings.json；改动非法时保留旧快照并记录错误。

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// WatchFiles 返回会被监听的三个配置文件路径。
func (m *Manager) WatchFiles() []string {
	home := m.startup.Home
	return []string{
		filepath.Join(home, "providers.json"),
		filepath.Join(home, "routing.json"),
		filepath.Join(home, "settings.json"),
	}
}

type fileStamp struct {
	mod  int64
	size int64
}

func statFiles(paths []string) map[string]fileStamp {
	out := make(map[string]fileStamp, len(paths))
	for _, p := range paths {
		info, err := os.Stat(p)
		if err != nil {
			out[p] = fileStamp{}
			continue
		}
		out[p] = fileStamp{mod: info.ModTime().UnixNano(), size: info.Size()}
	}
	return out
}

// Watch 轮询配置文件，变化时 Reload（成功与否都继续监听）。interval <= 0 用 2s。
func (m *Manager) Watch(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 2 * time.Second
	}
	paths := m.WatchFiles()
	// 启动时先对齐一次：如果配置文件在上次 Load 之后、Watch 之前被改过，
	// 直接 Reload 保证「已加载状态 == 监听基线」，否则这次改动会被当成基线而永久漏掉。
	if _, err := m.Reload(); err != nil {
		m.setLastError(err.Error())
	}
	last := statFiles(paths)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			now := statFiles(paths)
			changed := false
			for p, st := range now {
				if st != last[p] {
					changed = true
					break
				}
			}
			if !changed {
				continue
			}
			last = now
			if _, err := m.Reload(); err != nil {
				// 配置写坏了：保留旧快照，等下一次修改再试（CF4）。
				m.setLastError(fmt.Sprintf("配置热加载失败: %v", err))
			}
		}
	}
}

// setLastError 记录最近一次热加载错误（只读诊断用）。
func (m *Manager) setLastError(msg string) {
	m.mu.Lock()
	m.lastErr = msg
	m.mu.Unlock()
}
