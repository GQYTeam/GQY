// 核心工具：read（支持图片）、edit、shell、websearch、list_dir。
//
// 核心工具由 agent 内置，直接暴露给模型；其余能力走外部工具（插件/skill/mcp）。
package tool

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

/* ---------------- read（读取文件，支持图片） ---------------- */

type Read struct{}

func (Read) Name() string { return "read" }
func (Read) Kind() Kind   { return KindCore }
func (Read) Description() string {
	return "读取一个文件的内容。文本文件返回全文；图片（png/jpg/jpeg/gif/webp）返回 base64 数据供视觉理解。"
}
func (Read) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{"type": "string", "description": "绝对或相对路径"},
		},
		"required": []any{"path"},
	}
}
func (Read) Execute(ctx context.Context, args map[string]any) (string, error) {
	path, _ := args["path"].(string)
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("path 不能为空")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	// 图片：转 base64 供视觉模型
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp":
		return "data:image/" + strings.TrimPrefix(filepath.Ext(path), ".") + ";base64," + base64.StdEncoding.EncodeToString(b), nil
	}
	return string(b), nil
}

/* ---------------- edit（写入/覆盖文件） ---------------- */

type Edit struct{}

func (Edit) Name() string { return "edit" }
func (Edit) Kind() Kind   { return KindCore }
func (Edit) Description() string {
	return "写入（覆盖）一个文件。会自动创建父目录。用于产出或修改代码/配置。"
}
func (Edit) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path":    map[string]any{"type": "string", "description": "要写入的文件路径"},
			"content": map[string]any{"type": "string", "description": "完整文件内容"},
		},
		"required": []any{"path", "content"},
	}
}
func (Edit) Execute(ctx context.Context, args map[string]any) (string, error) {
	path, _ := args["path"].(string)
	content, _ := args["content"].(string)
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("path 不能为空")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return "", err
	}
	return fmt.Sprintf("已写入 %s（%d 字节）", path, len(content)), nil
}

/* ---------------- shell（跑 shell 命令） ---------------- */

type Shell struct{}

func (Shell) Name() string { return "shell" }
func (Shell) Kind() Kind   { return KindCore }
func (Shell) Description() string {
	return "执行一条 shell 命令，返回合并后的 stdout+stderr（最多 16KB）。用于编译、测试、git 等。"
}
func (Shell) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"command": map[string]any{"type": "string", "description": "要执行的 shell 命令"},
		},
		"required": []any{"command"},
	}
}
func (Shell) Execute(ctx context.Context, args map[string]any) (string, error) {
	command, _ := args["command"].(string)
	if strings.TrimSpace(command) == "" {
		return "", fmt.Errorf("command 不能为空")
	}
	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	out, err := cmd.CombinedOutput()
	const max = 16 * 1024
	if len(out) > max {
		out = append(append([]byte{}, out[:max]...), []byte("\n...[截断]")...)
	}
	if err != nil {
		return string(out), fmt.Errorf("exit %v: %s", err, string(out))
	}
	return string(out), nil
}

/* ---------------- list_dir（列目录） ---------------- */

type ListDir struct{}

func (ListDir) Name() string { return "list_dir" }
func (ListDir) Kind() Kind   { return KindCore }
func (ListDir) Description() string {
	return "列出目录下的文件和子目录名（目录以 / 结尾），每项一行。"
}
func (ListDir) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{"type": "string", "description": "目录路径，默认当前目录"},
		},
	}
}
func (ListDir) Execute(ctx context.Context, args map[string]any) (string, error) {
	path, _ := args["path"].(string)
	if path == "" {
		path = "."
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() {
			name += "/"
		}
		b.WriteString(name)
		b.WriteByte('\n')
	}
	return b.String(), nil
}

/* ---------------- websearch（网络搜索） ---------------- */

type WebSearch struct{}

func (WebSearch) Name() string { return "websearch" }
func (WebSearch) Kind() Kind   { return KindCore }
func (WebSearch) Description() string {
	return "在网络上搜索信息。需要服务端配置搜索 API（未配置时返回提示）。"
}
func (WebSearch) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{"type": "string", "description": "搜索关键词（英文效果更好）"},
		},
		"required": []any{"query"},
	}
}
func (WebSearch) Execute(ctx context.Context, args map[string]any) (string, error) {
	// 未接搜索后端：明确返回，让模型不要编造
	return "", fmt.Errorf("websearch 尚未接入搜索后端，请用 shell 调用本地工具，或等待后续接入")
}
