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
	written := make([]string, 0, len(raw))
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
		resolved, err := a.Policy.Resolve(path)
		if err != nil {
			return "", err
		}
		if err := os.MkdirAll(filepath.Dir(resolved), 0o755); err != nil {
			return "", err
		}
		if err := os.WriteFile(resolved, []byte(content), 0o644); err != nil {
			return "", err
		}
		written = append(written, path)
	}
	if len(written) == 0 {
		return "", fmt.Errorf("没有有效文件")
	}
	out, _ := json.Marshal(map[string]any{"title": title, "files": written})
	return string(out), nil
}
