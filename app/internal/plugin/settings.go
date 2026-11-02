package plugin

// 插件设置与卸载（PL3/PL4）：
// - 设置值存 $GQY_HOME/plugin-settings/<id>.json，插件目录保持不动（安装的插件只读）。
// - 卸载/删除插件时连同设置一起清理；删除前必须确认目录在 plugins/ 之内。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// settingsDir 返回插件设置目录（与插件目录分离，保证插件本体只读）。
func (m *Manager) settingsDir() string { return filepath.Join(m.home, "plugin-settings") }

// settingsPath 返回某插件的设置文件路径。
func (m *Manager) settingsPath(id string) string {
	return filepath.Join(m.settingsDir(), id+".json")
}

// manifestOf 取插件 manifest（未发现返回 nil）。
func (m *Manager) manifestOf(id string) *Manifest {
	m.mu.Lock()
	defer m.mu.Unlock()
	inst := m.plugins[id]
	if inst == nil {
		return nil
	}
	return inst.Manifest
}

// effectiveSettings 计算生效设置（默认值 + 已保存值），不加 manager 锁（PL3）。
func (m *Manager) effectiveSettings(mf *Manifest) map[string]any {
	out := make(map[string]any, len(mf.Settings))
	for _, s := range mf.Settings {
		if s.Default != nil {
			out[s.Key] = s.Default
		}
	}
	data, err := os.ReadFile(m.settingsPath(mf.ID))
	if err != nil {
		return out
	}
	stored := map[string]any{}
	if err := json.Unmarshal(data, &stored); err != nil {
		return out
	}
	for k, v := range stored {
		out[k] = v
	}
	return out
}

// SettingSchema 返回插件的设置 schema、只读标记与来源（PL3）。
func (m *Manager) SettingSchema(id string) ([]Setting, bool, string, error) {
	mf := m.manifestOf(id)
	if mf == nil {
		return nil, false, "", ErrNotFound
	}
	return mf.Settings, mf.ReadOnly, mf.Source, nil
}

// Settings 返回用户保存的设置值（不含默认值）。
func (m *Manager) Settings(id string) (map[string]any, error) {
	if m.manifestOf(id) == nil {
		return nil, ErrNotFound
	}
	data, err := os.ReadFile(m.settingsPath(id))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return map[string]any{}, nil
		}
		return nil, err
	}
	values := map[string]any{}
	if err := json.Unmarshal(data, &values); err != nil {
		return nil, fmt.Errorf("plugin: 设置文件损坏: %w", err)
	}
	return values, nil
}

// EffectiveSettings 返回「默认值 + 已保存值」合并后的生效配置（PL3）。
func (m *Manager) EffectiveSettings(id string) (map[string]any, error) {
	mf := m.manifestOf(id)
	if mf == nil {
		return nil, ErrNotFound
	}
	stored, err := m.Settings(id)
	if err != nil {
		return nil, err
	}
	out := make(map[string]any, len(mf.Settings))
	for _, s := range mf.Settings {
		if s.Default != nil {
			out[s.Key] = s.Default
		}
	}
	for k, v := range stored {
		out[k] = v
	}
	return out, nil
}

// SaveSettings 校验并覆盖保存设置；未知键与类型不符直接拒绝（PL3）。
// values 中值为 nil 表示删除该项（回落到默认）。
func (m *Manager) SaveSettings(id string, values map[string]any) error {
	mf := m.manifestOf(id)
	if mf == nil {
		return ErrNotFound
	}
	if err := ValidateSettingValues(mf.Settings, values); err != nil {
		return err
	}
	clean := make(map[string]any, len(values))
	for k, v := range values {
		if v != nil {
			clean[k] = v
		}
	}
	if err := os.MkdirAll(m.settingsDir(), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(clean, "", "  ")
	if err != nil {
		return err
	}
	path := m.settingsPath(id)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// ValidateSettingValues 按 manifest 的 settings schema 校验值（PL3）。
func ValidateSettingValues(schema []Setting, values map[string]any) error {
	byKey := make(map[string]Setting, len(schema))
	for _, s := range schema {
		byKey[s.Key] = s
	}
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, key := range keys {
		v := values[key]
		s, ok := byKey[key]
		if !ok {
			return fmt.Errorf("plugin: 未知设置项 %q", key)
		}
		if v == nil {
			continue
		}
		switch s.Type {
		case "string":
			if _, ok := v.(string); !ok {
				return fmt.Errorf("plugin: 设置项 %q 应为字符串", key)
			}
		case "number":
			switch v.(type) {
			case float64, int, int64:
			default:
				return fmt.Errorf("plugin: 设置项 %q 应为数字", key)
			}
		case "boolean":
			if _, ok := v.(bool); !ok {
				return fmt.Errorf("plugin: 设置项 %q 应为布尔值", key)
			}
		case "select":
			sval, ok := v.(string)
			if !ok {
				return fmt.Errorf("plugin: 设置项 %q 应为字符串选项", key)
			}
			found := false
			for _, opt := range s.Options {
				if opt == sval {
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("plugin: 设置项 %q 的值 %q 不在可选范围内", key, sval)
			}
		default:
			return fmt.Errorf("plugin: 设置项 %q 的类型 %q 不支持", key, s.Type)
		}
	}
	return nil
}

// Delete 卸载并删除插件目录及其设置（PL4）。只允许删除 plugins/ 之下的目录。
func (m *Manager) Delete(ctx context.Context, id string) error {
	mf := m.manifestOf(id)
	if mf == nil {
		return ErrNotFound
	}
	if inst := m.instance(id); inst != nil && inst.State() == StateReady {
		if err := m.Unload(ctx, id); err != nil {
			return err
		}
	}
	root := m.pluginsDir()
	dir := filepath.Join(root, id)
	rel, err := filepath.Rel(root, dir)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return fmt.Errorf("plugin: 拒绝删除 plugins/ 之外的路径: %s", dir)
	}
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	// 一并清掉插件的运行日志（$GQY_HOME/plugins/<id>.log）与设置（PL4）。
	_ = os.Remove(filepath.Join(root, id+".log"))
	_ = os.Remove(m.settingsPath(id))
	m.mu.Lock()
	delete(m.plugins, id)
	m.mu.Unlock()
	m.fireChange()
	return nil
}

// instance 取实例（未加载返回 nil）。
func (m *Manager) instance(id string) *Instance {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.plugins[id]
}
