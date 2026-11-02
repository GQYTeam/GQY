package plugin

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInstallFromDir(t *testing.T) {
	home := t.TempDir()
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "manifest.json"), []byte(`{"id":"demo-plugin","name":"Demo","version":"1.0.0","protocol":"gqy-jsonrpc-v1","command":["node","index.js"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "index.js"), []byte("console.log('demo')"), 0o600); err != nil {
		t.Fatal(err)
	}
	m := NewManager(home)
	if err := m.installFromDir(src, "demo-plugin"); err != nil {
		t.Fatalf("installFromDir: %v", err)
	}
	target := filepath.Join(home, "plugins", "demo-plugin")
	if _, err := os.Stat(filepath.Join(target, "index.js")); err != nil {
		t.Fatalf("index.js 未安装: %v", err)
	}
	if _, err := os.Stat(filepath.Join(target, InstalledMarker)); err != nil {
		t.Fatalf("安装标记缺失: %v", err)
	}
	if status := m.Status(); len(status) != 1 || status[0].ID != "demo-plugin" || status[0].Source != SourceInstalled || !status[0].ReadOnly {
		t.Fatalf("状态 = %+v", status)
	}
	if err := m.installFromDir(src, "demo-plugin"); err == nil {
		t.Fatal("重复安装应失败")
	}
	if err := m.installFromDir(src, "other-id"); err == nil {
		t.Fatal("manifest id 不一致应失败")
	}
}
