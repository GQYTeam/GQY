package oneshot

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gqy/internal/plugin"
)

// manifest 是一次性插件清单：公共字段复用 plugin.Manifest（15 NX1）。
type manifest struct {
	*plugin.Manifest
	Mode   string `json:"mode"`
	Hash   string `json:"hash"`
	Source string `json:"source"`
}

func parseManifestFile(path string) (*manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("oneshot: 读 manifest: %w", err)
	}
	return parseManifest(data)
}

func parseManifest(data []byte) (*manifest, error) {
	common, err := plugin.ParseManifest(data)
	if err != nil {
		return nil, err
	}
	var m manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("oneshot: manifest 解析失败: %w", err)
	}
	m.Manifest = common

	if m.Mode != "oneshot" {
		return nil, fmt.Errorf("oneshot: mode 必须为 oneshot，当前 %q", m.Mode)
	}
	switch common.Protocol {
	case plugin.ProtocolNative, plugin.ProtocolMCP:
	default:
		return nil, fmt.Errorf("oneshot: protocol 只支持 %s / %s", plugin.ProtocolNative, plugin.ProtocolMCP)
	}
	if m.Hash != "" {
		hash := strings.TrimPrefix(m.Hash, "sha256:")
		if len(hash) != 64 || !isLowerHex(hash) {
			return nil, fmt.Errorf("oneshot: hash 必须是 sha256:<64 位小写 hex>")
		}
		m.Hash = hash
	}
	return &m, nil
}

func isLowerHex(s string) bool {
	for _, r := range s {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

// hasRunAction 判断是否声明了 run 动作（NX3）。
func (m *manifest) hasRunAction() bool {
	for _, a := range m.Actions {
		if a.Name == "run" {
			return true
		}
	}
	return false
}

// manifestPath 返回插件目录下的 manifest.json 路径。
func manifestPath(dir string) string { return filepath.Join(dir, "manifest.json") }
