package host

import (
	"context"
	"fmt"
	"strings"

	"gqy/internal/config"
	"gqy/internal/plugin"
	"gqy/internal/tool"
)

// DefaultRegistryForTool 在核心工具之外，按工具名前缀加载对应的真实插件。
//
// 例如 tool 为 "echo__echo" 时，只加载 $GQY_HOME/plugins/echo；
// 没有匹配插件时返回核心工具 + 内置 Echo 占位，保持 testdata 可用。
func DefaultRegistryForTool(toolName string) (*tool.Registry, func(), []string, error) {
	reg, err := DefaultRegistry()
	if err != nil {
		return nil, func() {}, nil, err
	}
	appCfg := config.Load()
	pm := plugin.NewManager(appCfg.Home, reg)
	if errs := pm.Discover(); len(errs) > 0 {
		// manifest 错误不影响核心工具；返回给 CLI 打印。
		warnings := make([]string, 0, len(errs))
		for _, e := range errs {
			warnings = append(warnings, e.Error())
		}
		return reg, pm.Close, warnings, nil
	}

	target := ""
	for _, st := range pm.List() {
		if strings.HasPrefix(toolName, st.ID+"__") && len(st.ID) > len(target) {
			target = st.ID
		}
	}
	if target == "" {
		return reg, pm.Close, nil, nil
	}
	if err := pm.Load(context.Background(), target); err != nil {
		return reg, pm.Close, []string{fmt.Sprintf("插件 %s 加载失败: %v", target, err)}, nil
	}
	return reg, pm.Close, nil, nil
}
