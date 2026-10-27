// 包 report：把运行记录汇总成 Markdown 报告。
//
// 报告只引用本轮快照里的数据：矩阵表按端到端成功率排序，单因素对比按维度聚合，
// 失败反例取 Top N，成本按 token/耗时汇总。没有样本时明确留空，不编造结论。
package report

import (
	"fmt"
	"sort"
	"strings"

	"gqy/internal/eval/cases"
	"gqy/internal/eval/config"
	"gqy/internal/eval/result"
)

const topFailures = 10

type agg struct {
	prompt, schema, ret string
	runs, pass          int
	rounds, ms          int64
	tokensIn, tokensOut int
}

type failure struct {
	caseID string
	fails  int
	total  int
	runID  string
	status result.Status
	reason string
}

// Build 生成报告。tracePrefix 是 trace 文件相对报告目录的路径前缀，
// 例如 runs/r1-20260101-120000/trace。
func Build(cfg *config.Config, set *cases.Set, traces []*result.Trace, tracePrefix string) string {
	var b strings.Builder
	b.WriteString("# 工具调用评测报告\n\n")
	if cfg != nil {
		fmt.Fprintf(&b, "- 实验：`%s`\n", cfg.Name)
		fmt.Fprintf(&b, "- 模型：`%s`\n", cfg.Model)
		fmt.Fprintf(&b, "- 工具：`%s`\n", cfg.Tool)
		fmt.Fprintf(&b, "- 阶段：`%s`；轮次标签：`%s`\n", cfg.Stage, cfg.Round)
		fmt.Fprintf(&b, "- 三维度：prompt=%v；schema=%v；return=%v\n", cfg.Axes.Prompt, cfg.Axes.Schema, cfg.Axes.Return)
		fmt.Fprintf(&b, "- 固定值：prompt=`%s` schema=`%s` return=`%s`\n", cfg.Fixed.Prompt, cfg.Fixed.Schema, cfg.Fixed.Return)
		fmt.Fprintf(&b, "- 重复次数：%d；并发度：%d；最大轮次：%d；严格 schema：%v\n", cfg.Repeats, cfg.Concurrency, cfg.MaxRounds, cfg.StrictSchema)
	}
	if set != nil {
		fmt.Fprintf(&b, "- 用例集版本：`%s`；用例数：%d；哈希：`%s`\n", set.Version, len(set.Cases), set.Hash)
		if len(set.Filter) > 0 {
			fmt.Fprintf(&b, "- 用例过滤：%s\n", strings.Join(set.Filter, ", "))
		}
	}
	fmt.Fprintf(&b, "- 运行数：%d\n\n", len(traces))

	if len(traces) == 0 {
		b.WriteString("## 结论与建议\n\n没有可分析的样本，本报告不生成结论。\n")
		return b.String()
	}

	groups := groupByConfig(traces)
	writeMatrix(&b, groups)
	writeModelJudge(&b, cfg, traces)
	writeSingleFactor(&b, traces, "prompt")
	writeSingleFactor(&b, traces, "schema")
	writeSingleFactor(&b, traces, "return")
	writeInteraction(&b, cfg, groups)
	writeCombos(&b, groups)
	writeFailures(&b, traces, tracePrefix)
	writeCost(&b, traces)
	writeConclusion(&b, groups, set)
	return b.String()
}

func groupByConfig(traces []*result.Trace) []*agg {
	byKey := map[string]*agg{}
	for _, tr := range traces {
		key := tr.Config.Prompt + "\x00" + tr.Config.Schema + "\x00" + tr.Config.Return
		g := byKey[key]
		if g == nil {
			g = &agg{prompt: tr.Config.Prompt, schema: tr.Config.Schema, ret: tr.Config.Return}
			byKey[key] = g
		}
		g.runs++
		if tr.Judge != nil && tr.Judge.Pass {
			g.pass++
		}
		g.rounds += int64(tr.RoundsUsed)
		g.ms += tr.TotalMS
		g.tokensIn += tr.Tokens.In
		g.tokensOut += tr.Tokens.Out
	}
	out := make([]*agg, 0, len(byKey))
	for _, g := range byKey {
		out = append(out, g)
	}
	sort.Slice(out, func(i, j int) bool {
		ri, rj := rate(out[i]), rate(out[j])
		if ri != rj {
			return ri > rj
		}
		if out[i].runs != out[j].runs {
			return out[i].runs > out[j].runs
		}
		return keyOf(out[i]) < keyOf(out[j])
	})
	return out
}

func rate(a *agg) float64 {
	if a.runs == 0 {
		return 0
	}
	return float64(a.pass) / float64(a.runs)
}

func keyOf(a *agg) string { return a.prompt + "/" + a.schema + "/" + a.ret }

func writeMatrix(b *strings.Builder, groups []*agg) {
	b.WriteString("## 矩阵汇总（按端到端成功率排序）\n\n")
	b.WriteString("| prompt | schema | return | 运行 | 通过 | 成功率 | 平均轮次 | 平均耗时(ms) | token in/out |\n")
	b.WriteString("| --- | --- | --- | ---: | ---: | ---: | ---: | ---: | ---: |\n")
	for _, g := range groups {
		fmt.Fprintf(b, "| %s | %s | %s | %d | %d | %.1f%% | %.2f | %.1f | %d/%d |\n",
			cell(g.prompt), cell(g.schema), cell(g.ret), g.runs, g.pass, rate(g)*100,
			avg(g.rounds, g.runs), avg(g.ms, g.runs), g.tokensIn, g.tokensOut)
	}
	b.WriteString("\n")
	b.WriteString("端到端成功率 = judge 规则全部通过且运行状态为 ok 的比例。运行时错误不计入模型决策错误。\n\n")
}

func writeSingleFactor(b *strings.Builder, traces []*result.Trace, axis string) {
	type valAgg struct {
		runs, pass int
	}
	byVal := map[string]*valAgg{}
	for _, tr := range traces {
		v := axisValue(tr, axis)
		a := byVal[v]
		if a == nil {
			a = &valAgg{}
			byVal[v] = a
		}
		a.runs++
		if tr.Judge != nil && tr.Judge.Pass {
			a.pass++
		}
	}
	vals := make([]string, 0, len(byVal))
	for v := range byVal {
		vals = append(vals, v)
	}
	sort.Slice(vals, func(i, j int) bool {
		ri := float64(byVal[vals[i]].pass) / float64(byVal[vals[i]].runs)
		rj := float64(byVal[vals[j]].pass) / float64(byVal[vals[j]].runs)
		if ri != rj {
			return ri > rj
		}
		return vals[i] < vals[j]
	})

	fmt.Fprintf(b, "## 单因素对比：%s\n\n", axis)
	b.WriteString("| 变体 | 运行 | 通过 | 成功率 |\n| --- | ---: | ---: | ---: |\n")
	for _, v := range vals {
		a := byVal[v]
		fmt.Fprintf(b, "| %s | %d | %d | %.1f%% |\n", cell(v), a.runs, a.pass, 100*float64(a.pass)/float64(a.runs))
	}
	b.WriteString("\n未做显著性检验；表中只是本轮观察值，不能据此宣称因果。\n\n")
}

func axisValue(tr *result.Trace, axis string) string {
	switch axis {
	case "prompt":
		return tr.Config.Prompt
	case "schema":
		return tr.Config.Schema
	default:
		return tr.Config.Return
	}
}

func writeInteraction(b *strings.Builder, cfg *config.Config, groups []*agg) {
	b.WriteString("## 交互效应说明\n\n")
	if cfg != nil && cfg.Stage == config.StageSingle {
		b.WriteString("本轮是单因素扫描，其余维度固定，无法识别交互效应。\n\n")
		return
	}
	if len(groups) <= 1 {
		b.WriteString("本轮没有足够的组合数据，不做交互效应推断。\n\n")
		return
	}
	b.WriteString("本轮包含两两/全组合。表中成功率差异只是观察值，未做显著性检验，不能仅凭差异断言交互效应；需要更多重复样本复核。\n\n")
}

func writeCombos(b *strings.Builder, groups []*agg) {
	b.WriteString("## 组合观察\n\n")
	limit := 3
	if len(groups) < limit {
		limit = len(groups)
	}
	if limit == 0 {
		b.WriteString("没有组合数据。\n\n")
		return
	}
	b.WriteString("本轮成功率最高的组合（仅观察值，不做交互效应显著性推断）：\n\n")
	for i := 0; i < limit; i++ {
		g := groups[i]
		fmt.Fprintf(b, "%d. prompt=%s / schema=%s / return=%s：%d/%d（%.1f%%）\n",
			i+1, g.prompt, g.schema, g.ret, g.pass, g.runs, rate(g)*100)
	}
	b.WriteString("\n")
}

func writeFailures(b *strings.Builder, traces []*result.Trace, tracePrefix string) {
	byCase := map[string]*failure{}
	for _, tr := range traces {
		f := byCase[tr.CaseID]
		if f == nil {
			f = &failure{caseID: tr.CaseID}
			byCase[tr.CaseID] = f
		}
		f.total++
		if tr.Judge != nil && tr.Judge.Pass {
			continue
		}
		f.fails++
		if f.runID == "" {
			f.runID = tr.RunID
			f.status = tr.Status
			if tr.Judge != nil && len(tr.Judge.Reasons) > 0 {
				f.reason = strings.Join(tr.Judge.Reasons, "；")
			} else if tr.Error != "" {
				f.reason = tr.Error
			}
		}
	}
	list := make([]*failure, 0, len(byCase))
	for _, f := range byCase {
		if f.fails > 0 {
			list = append(list, f)
		}
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].fails != list[j].fails {
			return list[i].fails > list[j].fails
		}
		return list[i].caseID < list[j].caseID
	})
	if len(list) > topFailures {
		list = list[:topFailures]
	}

	fmt.Fprintf(b, "## 失败反例 Top %d\n\n", topFailures)
	if len(list) == 0 {
		b.WriteString("本轮没有失败样本。\n\n")
		return
	}
	b.WriteString("| 用例 | 失败/运行 | 状态 | 首个失败原因 | 轨迹 |\n| --- | ---: | --- | --- | --- |\n")
	for _, f := range list {
		link := traceName(tracePrefix, f.runID)
		fmt.Fprintf(b, "| %s | %d/%d | %s | %s | %s |\n",
			cell(f.caseID), f.fails, f.total, cell(string(f.status)), cell(truncate(f.reason, 160)), link)
	}
	b.WriteString("\n")
}

func traceName(prefix, runID string) string {
	name := result.SafeToken(runID, "run") + ".json"
	if prefix == "" {
		return "`" + name + "`"
	}
	return "`" + strings.TrimRight(prefix, "/") + "/" + name + "`"
}

func writeCost(b *strings.Builder, traces []*result.Trace) {
	var in, out, ms int64
	var rounds int
	for _, tr := range traces {
		in += int64(tr.Tokens.In)
		out += int64(tr.Tokens.Out)
		ms += tr.TotalMS
		rounds += tr.RoundsUsed
	}
	n := int64(len(traces))
	b.WriteString("## 成本汇总\n\n")
	fmt.Fprintf(b, "- 总 token：in=%d / out=%d / total=%d\n", in, out, in+out)
	fmt.Fprintf(b, "- 总耗时：%d ms；单次平均：%.1f ms\n", ms, avg(ms, len(traces)))
	fmt.Fprintf(b, "- 单次平均 token：in=%.1f / out=%.1f\n", float64(in)/float64(n), float64(out)/float64(n))
	fmt.Fprintf(b, "- 单次平均工具轮次：%.2f\n\n", avg(int64(rounds), len(traces)))
}

func writeConclusion(b *strings.Builder, groups []*agg, set *cases.Set) {
	b.WriteString("## 结论与建议\n\n")
	if len(groups) == 0 {
		b.WriteString("没有可比较的配置组合，本报告不生成结论。\n")
		return
	}
	top := groups[0]
	fmt.Fprintf(b, "本轮端到端成功率最高的配置是 prompt=`%s`、schema=`%s`、return=`%s`（%d/%d，%.1f%%）。\n",
		top.prompt, top.schema, top.ret, top.pass, top.runs, rate(top)*100)
	b.WriteString("这只是本轮快照的观察结论；模型、用例集或判定器任一变化都必须重跑，不能跨轮直接比较。\n")
	if set != nil {
		fmt.Fprintf(b, "引用用例集哈希：`%s`。\n", set.Hash)
	}
	b.WriteString("建议：先扩大样本量复核该组合，再决定是否写入默认配置。当前 CLI 不会自动写回任何线上配置。\n")
}

func avg(total int64, n int) float64 {
	if n == 0 {
		return 0
	}
	return float64(total) / float64(n)
}

func cell(s string) string {
	s = strings.ReplaceAll(s, "|", "\\|")
	s = strings.ReplaceAll(s, "\n", " ")
	if s == "" {
		return "—"
	}
	return s
}

func truncate(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}

// writeModelJudge 汇总可选的模型裁判结果；规则判定仍是主判定。
func writeModelJudge(b *strings.Builder, cfg *config.Config, traces []*result.Trace) {
	if cfg == nil || cfg.JudgeModel == "" {
		return
	}
	runs, pass, errors := 0, 0, 0
	for _, tr := range traces {
		if tr.Judge == nil || tr.Judge.Model == nil {
			continue
		}
		runs++
		if tr.Judge.Model.Pass {
			pass++
		}
		if tr.Judge.Model.Error != "" {
			errors++
		}
	}
	fmt.Fprintf(b, "\n## 模型裁判\n\n- 裁判模型：`%s`\n- 运行：%d；通过：%d；错误：%d\n", cfg.JudgeModel, runs, pass, errors)
}
