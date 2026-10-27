// 包 host：评测宿主环境的工具装配。
//
// 当前插件运行时尚未落地（见 docs/AboutPlugin.md），所以这里注册核心工具，
// 外加一个确定性的 echo 工具作为 plugin__echo__echo 的适配占位。
package host

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"gqy/internal/config"
	"gqy/internal/tool"
)

// Echo 是评测示例工具，模拟一个「原样回显」的外部插件。
type Echo struct{}

func (Echo) Name() string    { return "plugin__echo__echo" }
func (Echo) Kind() tool.Kind { return tool.KindExternal }
func (Echo) Description() string {
	return "把输入文本原样回显，并返回长度。用于验证工具调用链路，不影响真实系统状态。"
}
func (Echo) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"text": map[string]any{
				"type":        "string",
				"description": "要回显的文本",
			},
		},
		"required": []any{"text"},
	}
}
func (Echo) Execute(ctx context.Context, args map[string]any) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	text, _ := args["text"].(string)
	if strings.TrimSpace(text) == "" {
		return "", fmt.Errorf("text 不能为空")
	}
	out, err := json.Marshal(map[string]any{
		"echo":   text,
		"length": len([]rune(text)),
	})
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// DefaultRegistry 返回评测默认可用的工具集。
// 不注册 ask_user：评测里等待人工输入会挂住对话循环。
// 文件与 shell 工具复用服务端的路径沙箱配置，避免评测进程绕过权限策略。
func DefaultRegistry() (*tool.Registry, error) {
	appCfg := config.Load()
	policy := tool.Policy{
		Workspace:      appCfg.Workspace,
		Roots:          appCfg.ToolRoots,
		Denied:         []string{filepath.Join(appCfg.Home, "github_token.json")},
		ShellTimeout:   appCfg.ShellTimeout,
		MaxOutput:      appCfg.ToolMaxOutput,
		ShellAllowlist: appCfg.ShellAllowlist,
	}
	reg := tool.NewRegistry()
	tools := []tool.Tool{
		tool.Read{Policy: policy},
		tool.Edit{Policy: policy},
		tool.Shell{Policy: policy},
		tool.ListDir{Policy: policy},
		tool.WebSearch{},
		Echo{},
	}
	for _, t := range tools {
		if err := reg.Register(t); err != nil {
			return nil, err
		}
	}
	return reg, nil
}
