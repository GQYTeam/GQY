package runner

import (
	"fmt"
	"strings"
)

// RenderSystemPrompt 按 prompt 维度渲染系统提示。none 返回空串（请求里不插入 system）。
func RenderSystemPrompt(variant string, views []ToolView) (string, error) {
	if variant == "none" {
		return "", nil
	}

	var b strings.Builder
	b.WriteString("你是评测环境中的 AI 助手，可用工具如下：\n")
	if len(views) == 0 {
		b.WriteString("（当前没有注入工具）\n")
	}
	for _, v := range views {
		desc := firstSentence(v.Description, 200)
		if desc == "" {
			fmt.Fprintf(&b, "- %s\n", v.ExposedName)
			continue
		}
		fmt.Fprintf(&b, "- %s: %s\n", v.ExposedName, desc)
	}

	switch variant {
	case "basic":
		b.WriteString("需要时调用工具；不需要时直接回答。")
	case "when":
		b.WriteString(`
什么时候调用：需要真实信息（文件、目录、命令输出、外部数据）或需要执行实际操作时。
什么时候不调用：寒暄、常识问答、纯创作，或仅凭已有上下文即可回答时。`)
	case "when-how":
		b.WriteString(`
什么时候调用：需要真实信息（文件、目录、命令输出、外部数据）或需要执行实际操作时。
什么时候不调用：寒暄、常识问答、纯创作，或仅凭已有上下文即可回答时。
怎么调用：按工具 schema 填写参数；必填字段不能省略，不要编造不存在的参数；一次只调用必要的工具。`)
	case "fewshot":
		name := "工具"
		if len(views) > 0 {
			name = views[0].ExposedName
		}
		fmt.Fprintf(&b, `
什么时候调用：需要真实信息或执行实际操作时。
什么时候不调用：寒暄、常识问答、纯创作，或仅凭已有上下文即可回答时。
怎么调用：按工具 schema 填写参数，不省略必填字段，不编造参数。

示例 1（应当调用）：用户的问题需要真实数据才能回答 → 调用 %s，参数严格按 schema 填写。
示例 2（不应调用）：用户只是打招呼或问常识 → 直接回答，不调用任何工具。`, name)
	case "strict":
		b.WriteString(`
强约束：
- 不确定是否该调用时，不要调用。
- 凡是需要真实信息或实际操作，必须先调用工具拿到结果再回答。
- 不得声称已经调用过工具，也不得编造工具返回值。
- 工具返回错误时，如实说明失败原因，不要伪造成功结果。`)
	default:
		return "", fmt.Errorf("未知 prompt 变体 %q", variant)
	}
	return strings.TrimSpace(b.String()), nil
}
