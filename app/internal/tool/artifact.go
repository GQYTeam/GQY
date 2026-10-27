package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Artifact 让模型一次产出多个文件。文件写入 workspace（受路径沙箱约束），
// 同时把文件内容留在工具参数里，前端据此渲染多文件产物。
type Artifact struct{ Policy Policy }

func (Artifact) Name() string { return "write_artifact" }
func (Artifact) Kind() Kind   { return KindCore }
func (Artifact) Description() string {
	return "生成一个或多个交付文件（代码、文档、配置）。会写入工作区，并在界面上生成可预览的产物。"
}
func (Artifact) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"title": map[string]any{"type": "string", "description": "产物标题"},
			"files": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"path":    map[string]any{"type": "string", "description": "相对工作区的路径"},
						"content": map[string]any{"type": "string", "description": "完整文件内容"},
					},
					"required": []any{"path", "content"},
				},
			},
		},
		"required": []any{"title", "files"},
	}
}

type artifactFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

func (a Artifact) Execute(ctx context.Context, args map[string]any) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	title, _ := args["title"].(string)
	raw, _ := args["files"].([]any)
	if len(raw) == 0 {
		return "", fmt.Errorf("files 不能为空")
	}
	if len(raw) > 20 {
		return "", fmt.Errorf("一次最多 20 个文件")
	}
	const maxTotal = 2 << 20 // 与单文件上限一致：产物会进 localStorage，总量必须受限
	type planned struct {
		display  string
		resolved string
		content  string
	}
	plans := make([]planned, 0, len(raw))
	total := 0
	// 先完整校验再写：总量或路径不合法时不能留下写了一半的产物。
	for _, item := range raw {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		path, _ := m["path"].(string)
		content, _ := m["content"].(string)
		if strings.TrimSpace(path) == "" {
			return "", fmt.Errorf("文件路径不能为空")
		}
		if len(content) > 2<<20 {
			return "", fmt.Errorf("%s 超过 2 MB", path)
		}
		total += len(content)
		if total > maxTotal {
			return "", fmt.Errorf("产物总量超过 2 MB，请拆分或精简")
		}
		resolved, err := a.Policy.Resolve(path)
		if err != nil {
			return "", err
		}
		plans = append(plans, planned{display: path, resolved: resolved, content: content})
	}
	if len(plans) == 0 {
		return "", fmt.Errorf("没有有效文件")
	}
	written := make([]string, 0, len(plans))
	for _, p := range plans {
		if err := os.MkdirAll(filepath.Dir(p.resolved), 0o755); err != nil {
			return "", err
		}
		if err := os.WriteFile(p.resolved, []byte(p.content), 0o644); err != nil {
			return "", err
		}
		written = append(written, p.display)
	}
	out, _ := json.Marshal(map[string]any{"title": title, "files": written})
	return string(out), nil
}
