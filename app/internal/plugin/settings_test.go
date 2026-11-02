package plugin

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"gqy/internal/tool"
)

// settingsManifest 带 setting 声明的 manifest。
const settingsManifest = `{"id":"%s","name":"带设置插件","version":"1.0.0","protocol":"gqy-jsonrpc-v1","command":["x"],
 "settings":[{"key":"greeting","title":"问候语","type":"string","default":"hi"},
             {"key":"limit","title":"上限","type":"number","default":3},
             {"key":"loud","title":"大声","type":"boolean","default":false},
             {"key":"mode","title":"模式","type":"select","default":"a","options":["a","b"]}]}`

func newSettingsManager(t *testing.T) (*Manager, string) {
	t.Helper()
	home := t.TempDir()
	m := NewManager(home)
	m.adapters[ProtocolNative] = &fakeAdapter{protocol: ProtocolNative, loadFn: func(ctx context.Context, mf *Manifest, dir string) (adapterInstance, error) {
		return &fakeInst{actions: mf.Actions}, nil
	}}
	return m, home
}

// TestPluginSourceDerivation 验证 installed/local 两种模式（PL2）。
func TestPluginSourceDerivation(t *testing.T) {
	m, home := newSettingsManager(t)
	dir := filepath.Join(home, "plugins")
	// 本地自建：无标记 → local / 可改。
	if err := os.MkdirAll(filepath.Join(dir, "local-one"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "local-one", "manifest.json"),
		[]byte(`{"id":"local-one","name":"L","version":"1.0.0","protocol":"gqy-jsonrpc-v1","command":["x"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	// 在线安装：有标记 → installed / 只读。
	writeManifest(t, dir, "installed-one", `{"id":"installed-one","name":"I","version":"1.0.0","protocol":"gqy-jsonrpc-v1","command":["x"]}`)
	if err := os.WriteFile(filepath.Join(dir, "installed-one", InstalledMarker), []byte("registry"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := m.Scan(); err != nil {
		t.Fatal(err)
	}
	byID := map[string]PluginStatus{}
	for _, st := range m.Status() {
		byID[st.ID] = st
	}
	if got := byID["local-one"]; got.Source != SourceLocal || got.ReadOnly {
		t.Fatalf("本地插件应 local/可改，实际 %+v", got)
	}
	if got := byID["installed-one"]; got.Source != SourceInstalled || !got.ReadOnly {
		t.Fatalf("安装插件应 installed/只读，实际 %+v", got)
	}
}

// TestSettingsSaveAndEffective 验证设置校验、保存与生效值合并（PL3）。
func TestSettingsSaveAndEffective(t *testing.T) {
	m, home := newSettingsManager(t)
	writeManifest(t, filepath.Join(home, "plugins"), "p1", fmt.Sprintf(settingsManifest, "p1"))
	if err := m.Scan(); err != nil {
		t.Fatal(err)
	}
	schema, readOnly, source, err := m.SettingSchema("p1")
	if err != nil || len(schema) != 4 || readOnly || source != SourceLocal {
		t.Fatalf("schema=%v readOnly=%v source=%v err=%v", schema, readOnly, source, err)
	}

	// 默认值生效。
	eff, err := m.EffectiveSettings("p1")
	if err != nil {
		t.Fatal(err)
	}
	if eff["greeting"] != "hi" || eff["limit"] != float64(3) || eff["loud"] != false || eff["mode"] != "a" {
		t.Fatalf("默认生效值不对: %v", eff)
	}

	// 合法保存。
	if err := m.SaveSettings("p1", map[string]any{"greeting": "hello", "limit": float64(10), "loud": true, "mode": "b"}); err != nil {
		t.Fatalf("合法设置应保存: %v", err)
	}
	eff, _ = m.EffectiveSettings("p1")
	if eff["greeting"] != "hello" || eff["limit"] != float64(10) || eff["loud"] != true || eff["mode"] != "b" {
		t.Fatalf("保存后生效值不对: %v", eff)
	}

	// 非法：未知键、类型不符、选项越界。
	if err := m.SaveSettings("p1", map[string]any{"nope": 1}); err == nil {
		t.Fatal("未知设置项应被拒绝")
	}
	if err := m.SaveSettings("p1", map[string]any{"limit": "十"}); err == nil {
		t.Fatal("数字类型不符应被拒绝")
	}
	if err := m.SaveSettings("p1", map[string]any{"mode": "c"}); err == nil {
		t.Fatal("select 越界应被拒绝")
	}

	// nil 表示回落到默认。
	if err := m.SaveSettings("p1", map[string]any{"greeting": nil}); err != nil {
		t.Fatalf("nil 应表示删除该项: %v", err)
	}
	eff, _ = m.EffectiveSettings("p1")
	if eff["greeting"] != "hi" {
		t.Fatalf("删除后应回落默认，实际 %v", eff["greeting"])
	}
	// 设置文件在 plugin-settings/ 下、权限 0600。
	info, err := os.Stat(m.settingsPath("p1"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("设置文件权限应为 0600，实际 %v", info.Mode().Perm())
	}
}

// TestPluginDelete 验证删除：卸载、删目录、删设置、路径安全（PL4）。
func TestPluginDelete(t *testing.T) {
	m, home := newSettingsManager(t)
	dir := filepath.Join(home, "plugins")
	writeManifest(t, dir, "del-one", `{"id":"del-one","name":"D","version":"1.0.0","protocol":"gqy-jsonrpc-v1","command":["x"],"actions":[]}`)
	if err := m.Scan(); err != nil {
		t.Fatal(err)
	}
	if err := m.Load(context.Background(), "del-one"); err != nil {
		t.Fatal(err)
	}
	if err := m.SaveSettings("del-one", map[string]any{}); err != nil {
		t.Fatalf("空设置应允许保存: %v", err)
	}

	if err := m.Delete(context.Background(), "del-one"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "del-one")); !os.IsNotExist(err) {
		t.Fatalf("插件目录应被删除: %v", err)
	}
	if _, err := os.Stat(m.settingsPath("del-one")); !os.IsNotExist(err) {
		t.Fatalf("设置文件应被删除: %v", err)
	}
	if got := len(m.Status()); got != 0 {
		t.Fatalf("删除后应无插件，实际 %d", got)
	}
	if err := m.Delete(context.Background(), "del-one"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("重复删除应 ErrNotFound，实际 %v", err)
	}
	if err := m.Delete(context.Background(), ".."); !errors.Is(err, ErrNotFound) {
		t.Fatalf("路径穿越应被拒绝，实际 %v", err)
	}
}

// TestLoadPassesEffectiveSettings 验证加载时把生效设置随 initialize 传给插件（PL3）。
func TestLoadPassesEffectiveSettings(t *testing.T) {
	m, home := newSettingsManager(t)
	var got map[string]any
	m.adapters[ProtocolNative] = &fakeAdapter{protocol: ProtocolNative, loadFn: func(ctx context.Context, mf *Manifest, dir string) (adapterInstance, error) {
		got = mf.Values
		return &fakeInst{actions: mf.Actions}, nil
	}}
	writeManifest(t, filepath.Join(home, "plugins"), "p1", fmt.Sprintf(settingsManifest, "p1"))
	if err := m.Scan(); err != nil {
		t.Fatal(err)
	}
	if err := m.SaveSettings("p1", map[string]any{"greeting": "hello"}); err != nil {
		t.Fatal(err)
	}
	if err := m.Load(context.Background(), "p1"); err != nil {
		t.Fatal(err)
	}
	if got["greeting"] != "hello" {
		t.Fatalf("保存值应下发，实际 %v", got["greeting"])
	}
	if got["limit"] != float64(3) {
		t.Fatalf("默认值应下发，实际 %v", got["limit"])
	}
	if got["loud"] != false {
		t.Fatalf("布尔默认值应下发，实际 %v", got["loud"])
	}
}

// TestPluginChangeHookFires 验证插件变化会触发工具表重建回调（HR2）。
func TestPluginChangeHookFires(t *testing.T) {
	m, home := newSettingsManager(t)
	var fires int
	m.SetOnChange(func() { fires++ })
	writeManifest(t, filepath.Join(home, "plugins"), "h1", `{"id":"h1","name":"H","version":"1.0.0","protocol":"gqy-jsonrpc-v1","command":["x"],"actions":[]}`)
	if err := m.Scan(); err != nil {
		t.Fatal(err)
	}
	afterScan := fires
	if afterScan == 0 {
		t.Fatal("Scan 后应触发变更回调")
	}
	if err := m.Load(context.Background(), "h1"); err != nil {
		t.Fatal(err)
	}
	if fires <= afterScan {
		t.Fatal("Load 后应触发变更回调")
	}
	afterLoad := fires
	if err := m.Unload(context.Background(), "h1"); err != nil {
		t.Fatal(err)
	}
	if fires <= afterLoad {
		t.Fatal("Unload 后应触发变更回调")
	}
	afterUnload := fires
	if err := m.Delete(context.Background(), "h1"); err != nil {
		t.Fatal(err)
	}
	if fires <= afterUnload {
		t.Fatal("Delete 后应触发变更回调")
	}
}

// TestRegisterToolsExpose 验证 expose 开关决定插件动作是否进入模型可见工具表（HR2）。
func TestRegisterToolsExpose(t *testing.T) {
	m, home := newSettingsManager(t)
	writeManifest(t, filepath.Join(home, "plugins"), "x1", `{"id":"x1","name":"X","version":"1.0.0","protocol":"gqy-jsonrpc-v1","command":["x"],
	 "actions":[{"name":"go","description":"跑一下","schema":{"type":"object"}}]}`)
	if err := m.Scan(); err != nil {
		t.Fatal(err)
	}
	if err := m.Load(context.Background(), "x1"); err != nil {
		t.Fatal(err)
	}

	reg := tool.NewRegistry()
	m.RegisterTools(reg, true) // 暴露：进 core
	if len(reg.Core()) != 1 {
		t.Fatalf("暴露时应有 1 个可见工具，实际 %d", len(reg.Core()))
	}
	if _, ok := reg.Get("x1__go"); !ok {
		t.Fatal("工具名应为 <plugin>__<action>")
	}

	reg.ResetExternal()
	m.RegisterTools(reg, false) // 不暴露：只进 external
	if len(reg.Core()) != 0 {
		t.Fatalf("关闭暴露后不应有可见工具，实际 %d", len(reg.Core()))
	}
	if len(reg.External()) != 1 {
		t.Fatalf("关闭暴露后应仍在外部表，实际 %d", len(reg.External()))
	}
}
