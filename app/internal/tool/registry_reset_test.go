package tool_test

import (
	"context"
	"testing"

	"gqy/internal/tool"
)

type fakeTool struct {
	name string
}

func (f fakeTool) Name() string                                            { return f.name }
func (f fakeTool) Description() string                                     { return "fake" }
func (f fakeTool) Schema() map[string]any                                  { return map[string]any{"type": "object"} }
func (f fakeTool) Source() tool.Source                                     { return tool.SourceCore }
func (f fakeTool) Execute(context.Context, map[string]any) (string, error) { return "", nil }

// ownedTool 同时实现 Owned（模拟插件工具）。
type ownedTool struct{ fakeTool }

func (o ownedTool) Owner() string { return "plugin:test" }

// TestResetExternalRemovesOnlyPluginTools 验证热更新重建只清插件来源工具（HR2）。
func TestResetExternalRemovesOnlyPluginTools(t *testing.T) {
	reg := tool.NewRegistry()
	if err := reg.Register(fakeTool{name: "read"}); err != nil {
		t.Fatal(err)
	}
	if err := reg.Register(ownedTool{fakeTool{name: "echo__echo"}}); err != nil {
		t.Fatal(err)
	}
	// 插件工具以 core 身份注册时也应被清掉。
	if len(reg.Core()) != 2 {
		t.Fatalf("注册后 core 应有 2 个工具，实际 %d", len(reg.Core()))
	}
	reg.ResetExternal()
	if _, ok := reg.Get("read"); !ok {
		t.Fatal("核心工具不应被热重建清掉")
	}
	if _, ok := reg.Get("echo__echo"); ok {
		t.Fatal("插件工具应被热重建清掉")
	}
}
