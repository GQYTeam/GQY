package eval

import (
	"fmt"
	"sort"
	"strings"
)

// Report 生成七项报告（E8）。
func Report(cfg *EvalConfig, cases *Cases, runs []*RunResult) string {
	var b strings.Builder
	b.WriteString("# 评测报告\n\n")

	// 1. 配置快照摘要
	b.WriteString("## 1. 配置快照\n\n")
	fmt.Fprintf(&b, "- 模型: %s\n", cfg.Model)
	fmt.Fprintf(&b, "- 维度: prompt=%v schema=%v return=%v\n", cfg.PromptVars, cfg.SchemaVars, cfg.ReturnVars)
	fmt.Fprintf(&b, "- 用例数: %d，重复: %d\n\n", len(cases.Cases), cfg.Repeat)

	// 2. 矩阵汇总表
	b.WriteString("## 2. 矩阵汇总\n\n| 组合 | 端到端成功率 | 样本数 |\n| --- | --- | --- |\n")
	groups := groupByCombo(runs)
	for _, g := range groups {
		fmt.Fprintf(&b, "| %s/%s/%s | %.0f%% | %d |\n", g.combo.Prompt, g.combo.Schema, g.combo.Return, successRate(g.runs)*100, len(g.runs))
	}
	b.WriteString("\n")

	// 3. 单因素对比
	b.WriteString("## 3. 单因素对比\n\n按维度内变体成功率排序：\n\n")
	for _, dim := range []string{"prompt", "schema", "return"} {
		b.WriteString("- " + dim + ": ")
		rates := make([]string, 0)
		for _, g := range groups {
			val := comboValue(g.combo, dim)
			rates = append(rates, fmt.Sprintf("%s=%.0f%%", val, successRate(g.runs)*100))
		}
		b.WriteString(strings.Join(rates, ", ") + "\n")
	}
	b.WriteString("\n")

	// 4. 交互效应（简化）
	b.WriteString("## 4. 交互效应\n\npair/full 阶段的两两组合成功率见矩阵汇总表；差异超过 10% 记为疑似交互效应。\n\n")

	// 5. 反例清单
	b.WriteString("## 5. 反例清单\n\n")
	fails := failingRuns(runs)
	if len(fails) == 0 {
		b.WriteString("无反例。\n\n")
	} else {
		if len(fails) > 10 {
			fails = fails[:10]
		}
		for _, r := range fails {
			fmt.Fprintf(&b, "- %s（%s）status=%s\n", r.RunID, r.CaseID, r.Status)
		}
		b.WriteString("\n")
	}

	// 6. 结论与建议
	b.WriteString("## 6. 结论与建议\n\n")
	best := bestCombo(groups)
	if best != nil {
		fmt.Fprintf(&b, "胜出组合: %s/%s/%s（成功率 %.0f%%）。\n", best.combo.Prompt, best.combo.Schema, best.combo.Return, successRate(best.runs)*100)
	}
	b.WriteString("建议在 full 阶段放大该组合的重复次数验证稳定性。\n\n")

	// 7. 成本
	b.WriteString("## 7. 成本\n\n")
	var in, out, totalMS int64
	for _, r := range runs {
		in += int64(r.Tokens.In)
		out += int64(r.Tokens.Out)
		totalMS += r.TotalMS
	}
	fmt.Fprintf(&b, "- 总 token: in=%d out=%d\n", in, out)
	fmt.Fprintf(&b, "- 总耗时: %d ms；样本数: %d\n", totalMS, len(runs))
	return b.String()
}

type comboGroup struct {
	combo Combo
	runs  []*RunResult
}

func groupByCombo(runs []*RunResult) []comboGroup {
	index := map[string]*comboGroup{}
	var order []string
	for _, r := range runs {
		key := r.Config["prompt"] + "/" + r.Config["schema"] + "/" + r.Config["return"]
		g, ok := index[key]
		if !ok {
			g = &comboGroup{combo: Combo{Prompt: r.Config["prompt"], Schema: r.Config["schema"], Return: r.Config["return"]}}
			index[key] = g
			order = append(order, key)
		}
		g.runs = append(g.runs, r)
	}
	out := make([]comboGroup, 0, len(order))
	for _, key := range order {
		out = append(out, *index[key])
	}
	sort.Slice(out, func(i, j int) bool { return successRate(out[i].runs) > successRate(out[j].runs) })
	return out
}

func successRate(runs []*RunResult) float64 {
	if len(runs) == 0 {
		return 0
	}
	ok := 0
	for _, r := range runs {
		if r.Status == StatusOK && r.Judge.Rule.Pass {
			ok++
		}
	}
	return float64(ok) / float64(len(runs))
}

func failingRuns(runs []*RunResult) []*RunResult {
	out := make([]*RunResult, 0)
	for _, r := range runs {
		if r.Status != StatusOK || !r.Judge.Rule.Pass {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CaseID < out[j].CaseID })
	return out
}

func bestCombo(groups []comboGroup) *comboGroup {
	if len(groups) == 0 {
		return nil
	}
	return &groups[0]
}

func comboValue(c Combo, dim string) string {
	switch dim {
	case "prompt":
		return c.Prompt
	case "schema":
		return c.Schema
	case "return":
		return c.Return
	}
	return ""
}
