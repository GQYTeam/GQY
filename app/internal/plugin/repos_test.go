package plugin

// 插件仓库规范检查（PL1/PL2 的「强行规范」）：
// 校验本组织各插件仓库与内置插件目录的 manifest 是否合规；不合规直接让测试失败。

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// repoRoot 返回 GQY 仓库根目录（app/internal/plugin → ../../..）。
func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("无法定位测试文件")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", ".."))
}

type manifestDoc struct {
	ID       string            `json:"id"`
	Name     string            `json:"name"`
	Version  string            `json:"version"`
	Protocol string            `json:"protocol"`
	Command  []string          `json:"command"`
	Source   string            `json:"source"`
	Actions  []json.RawMessage `json:"actions"`
	Settings []Setting         `json:"settings"`
}

func validatePluginManifest(t *testing.T, dir, wantDirName, source string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		t.Fatalf("%s: 读取 manifest.json: %v", wantDirName, err)
	}
	var mf manifestDoc
	if err := json.Unmarshal(data, &mf); err != nil {
		t.Fatalf("%s: manifest.json 不是合法 JSON: %v", wantDirName, err)
	}
	if mf.ID == "" {
		t.Fatalf("%s: manifest.id 不能为空", wantDirName)
	}
	id := mf.ID
	if strings.TrimSpace(mf.Name) == "" || strings.TrimSpace(mf.Version) == "" {
		t.Fatalf("%s: name/version 不能为空", id)
	}
	if mf.Protocol == "" {
		t.Fatalf("%s: protocol 不能为空", id)
	}
	if len(mf.Command) == 0 && mf.Source != "" && !strings.Contains(string(data), `"address"`) {
		t.Fatalf("%s: command 不能为空（远程插件必须声明 address）", id)
	}
	// native 协议必须静态声明 actions；MCP / 远程插件的动作在运行时发现，允许为空（P3/P4）。
	if len(mf.Actions) == 0 && mf.Protocol == ProtocolNative {
		t.Fatalf("%s: native 插件必须在 manifest 里声明 actions", id)
	}
	seen := map[string]bool{}
	for _, s := range mf.Settings {
		if s.Key == "" {
			t.Fatalf("%s: settings 项缺少 key", id)
		}
		if seen[s.Key] {
			t.Fatalf("%s: settings 键重复: %s", id, s.Key)
		}
		seen[s.Key] = true
		switch s.Type {
		case "string", "number", "boolean", "select":
		default:
			t.Fatalf("%s: settings.%s 类型 %q 不支持", id, s.Key, s.Type)
		}
		if s.Type == "select" && len(s.Options) == 0 {
			t.Fatalf("%s: settings.%s 是 select 但缺少 options", id, s.Key)
		}
	}
	// 开发模式仓库（本组织源码）不得带安装标记。
	if source == SourceLocal {
		if _, err := os.Stat(filepath.Join(dir, InstalledMarker)); err == nil {
			t.Fatalf("%s: 源码仓库不应包含 %s（那是安装后的标记）", id, InstalledMarker)
		}
	}
}

// TestPluginReposConform 校验组织内所有插件仓库与内置插件目录。
func TestPluginReposConform(t *testing.T) {
	root := repoRoot(t)
	// 1) GQY 自带的示例插件：app/plugins/*/manifest.json
	builtin := filepath.Join(root, "app", "plugins")
	if entries, err := os.ReadDir(builtin); err == nil {
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			validatePluginManifest(t, filepath.Join(builtin, e.Name()), e.Name(), SourceLocal)
		}
	}
	// 2) 同级插件仓库：<org>/gqy-plugins-*/manifest.json
	org := filepath.Clean(filepath.Join(root, ".."))
	entries, err := os.ReadDir(org)
	if err != nil {
		t.Logf("跳过同级插件仓库检查: %v", err)
		return
	}
	checked := 0
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), "gqy-plugins-") {
			continue
		}
		dir := filepath.Join(org, e.Name())
		if _, err := os.Stat(filepath.Join(dir, "manifest.json")); err != nil {
			continue
		}
		id := strings.TrimPrefix(e.Name(), "gqy-plugins-")
		// 目录名是仓库名，manifest.id 通常是插件 id（小写短横线），允许二者不同但必须自洽。
		validatePluginManifest(t, dir, id, SourceLocal)
		checked++
	}
	t.Logf("插件仓库规范检查完成：内置 + %d 个同级仓库", checked)
}
