// 内置工具：读文件、列目录、写文件、跑 shell 命令。
//
// 安全模型（本阶段）：信任本机，工具拥有完整本机权限。
// 后续接 AboutPlugin.md 的工作区隔离 / 人工确认时，在 Execute 前统一加闸。
package tool

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

/* ---------------- 读文件 ---------------- */

type ReadFile struct{}

func (ReadFile) Name() string { return "read_file" }
func (ReadFile) Description() string {
	return "读取一个文件的文本内容。返回文件全文。用于查看代码、配置或文档。"
}
func (ReadFile) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{"type": "string", "description": "绝对或相对路径"},
		},
		"required": []any{"path"},
	}
}
func (ReadFile) Execute(ctx context.Context, args map[string]any) (string, error) {
	path, _ := args["path"].(string)
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("path 不能为空")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

/* ---------------- 列目录 ---------------- */

type ListDir struct{}

func (ListDir) Name() string { return "list_dir" }
func (ListDir) Description() string {
	return "列出目录下的文件和子目录名。返回每项一行（目录以 / 结尾）。"
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

/* ---------------- 写文件 ---------------- */

type WriteFile struct{}

func (WriteFile) Name() string { return "write_file" }
func (WriteFile) Description() string {
	return "写入（覆盖）一个文件。会自动创建父目录。用于产出代码或修改文件。"
}
func (WriteFile) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path":    map[string]any{"type": "string", "description": "要写入的文件路径"},
			"content": map[string]any{"type": "string", "description": "完整文件内容"},
		},
		"required": []any{"path", "content"},
	}
}
func (WriteFile) Execute(ctx context.Context, args map[string]any) (string, error) {
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

/* ---------------- 跑 shell ---------------- */

type RunShell struct{}

func (RunShell) Name() string { return "run_shell" }
func (RunShell) Description() string {
	return "执行一条 shell 命令，返回合并后的 stdout+stderr（最多截断到 16KB）。用于编译、运行测试、查 git 状态等。"
}
func (RunShell) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"command": map[string]any{"type": "string", "description": "要执行的 shell 命令"},
		},
		"required": []any{"command"},
	}
}
func (RunShell) Execute(ctx context.Context, args map[string]any) (string, error) {
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
