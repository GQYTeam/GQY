// 命令 gqy-eval：工具调用评测网关 CLI。
//
// 用法：
//
//	go run ./cmd/gqy-eval -config <file> -out <dir> [-dry-run] [-replay <trace-dir>] [-filter key=value]
//
// 退出码：全部通过 0；有失败 1；配置/运行错误 2。
package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gqy/internal/eval/cases"
	evalconfig "gqy/internal/eval/config"
	"gqy/internal/eval/engine"
	"gqy/internal/eval/host"
	"gqy/internal/eval/matrix"
	"gqy/internal/eval/recorder"
	"gqy/internal/eval/replay"
	"gqy/internal/eval/report"
	"gqy/internal/eval/result"
	"gqy/internal/provider"
	"gqy/internal/tool"
)

type filterFlag []string

func (f *filterFlag) String() string { return strings.Join(*f, ",") }
func (f *filterFlag) Set(v string) error {
	*f = append(*f, v)
	return nil
}

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	fs := flag.NewFlagSet("gqy-eval", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var (
		configPath = fs.String("config", "", "实验配置 JSON 文件")
		outDir     = fs.String("out", "", "结果输出目录（非 dry-run/replay 必填）")
		dryRun     = fs.Bool("dry-run", false, "只打印矩阵与用例数，不执行、不落盘")
		replayDir  = fs.String("replay", "", "从 trace 目录回放，不请求模型")
		filters    filterFlag
	)
	fs.Var(&filters, "filter", "过滤 key=value（可重复；value 用逗号分隔）")
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "用法: go run ./cmd/gqy-eval -config <file> -out <dir> [-dry-run] [-replay <trace-dir>] [-filter key=value]\n\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if strings.TrimSpace(*configPath) == "" {
		fmt.Fprintln(os.Stderr, "配置错误: -config 必填")
		fs.Usage()
		return 2
	}

	cfg, err := evalconfig.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "配置错误: %v\n", err)
		return 2
	}
	caseSet, err := cases.Load(cfg.CasesPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "用例错误: %v\n", err)
		return 2
	}

	parsed, err := parseFilters(filters)
	if err != nil {
		fmt.Fprintf(os.Stderr, "过滤错误: %v\n", err)
		return 2
	}
	experiments := matrix.Generate(cfg)
	experiments, err = matrix.Apply(experiments, parsed)
	if err != nil {
		fmt.Fprintf(os.Stderr, "过滤错误: %v\n", err)
		return 2
	}
	if len(experiments) == 0 {
		fmt.Fprintln(os.Stderr, "过滤错误: 没有匹配的配置组合")
		return 2
	}
	caseIDs := matrix.CaseValues(parsed)
	if len(caseIDs) > 0 {
		caseSet, err = caseSet.Subset(caseIDs)
		if err != nil {
			fmt.Fprintf(os.Stderr, "过滤错误: %v\n", err)
			return 2
		}
	}

	if *dryRun {
		printDryRun(cfg, caseSet, experiments)
		return 0
	}
	if strings.TrimSpace(*replayDir) != "" {
		return runReplay(*replayDir, parsed)
	}
	if strings.TrimSpace(*outDir) == "" {
		fmt.Fprintln(os.Stderr, "运行错误: 非 dry-run/replay 模式必须提供 -out")
		return 2
	}
	return runEval(cfg, caseSet, experiments, *outDir)
}

func parseFilters(raw filterFlag) ([]matrix.Filter, error) {
	out := make([]matrix.Filter, 0, len(raw))
	for _, item := range raw {
		f, err := matrix.ParseFilter(item)
		if err != nil {
			return nil, err
		}
		if !matrix.KnownFilterKey(f.Key) {
			return nil, fmt.Errorf("未知键 %q（可选 prompt/schema/return/round/axis/case）", f.Key)
		}
		out = append(out, f)
	}
	return out, nil
}

func printDryRun(cfg *evalconfig.Config, set *cases.Set, exps []matrix.Experiment) {
	fmt.Printf("配置: %s（stage=%s, round=%s, model=%s）\n", cfg.Name, cfg.Stage, cfg.Round, cfg.Model)
	fmt.Printf("工具: %s；用例: %d 个（version=%s, hash=%s）\n", cfg.Tool, len(set.Cases), set.Version, shortHash(set.Hash))
	fmt.Printf("矩阵: %d 个配置 × %d 个用例 × %d 次重复 = %d 次运行\n\n",
		len(exps), len(set.Cases), cfg.Repeats, len(exps)*len(set.Cases)*cfg.Repeats)
	fmt.Printf("%-4s %-10s %-10s %-10s %-16s\n", "#", "prompt", "schema", "return", "axis")
	for i, exp := range exps {
		fmt.Printf("%-4d %-10s %-10s %-10s %-16s\n", i+1, exp.Prompt, exp.Schema, exp.Return, exp.Axis)
	}
}

func runEval(cfg *evalconfig.Config, set *cases.Set, exps []matrix.Experiment, outDir string) int {
	registry, err := host.DefaultRegistry()
	if err != nil {
		fmt.Fprintf(os.Stderr, "运行错误: 装配工具失败: %v\n", err)
		return 2
	}
	if _, ok := registry.Get(cfg.Tool); !ok {
		fmt.Fprintf(os.Stderr, "运行错误: 工具 %q 未注册（评测宿主当前提供: %s）\n", cfg.Tool, toolNames(registry))
		return 2
	}

	apiKey := os.Getenv("GQY_API_KEY")
	if strings.TrimSpace(apiKey) == "" {
		fmt.Fprintln(os.Stderr, "运行错误: 未配置 GQY_API_KEY，无法真实调用模型；可先用 -dry-run 检查矩阵")
		return 2
	}
	apiBase := os.Getenv("GQY_API_BASE")
	if strings.TrimSpace(apiBase) == "" {
		apiBase = "https://api.deepseek.com/v1"
	}
	apiBase = strings.TrimRight(apiBase, "/")
	upstream := &provider.Upstream{
		APIBase: apiBase,
		APIKey:  apiKey,
		APIUA:   os.Getenv("GQY_API_UA"),
		Model:   cfg.Model,
		Wire:    "chat",
		Client:  &http.Client{},
	}

	rec, err := recorder.New(outDir, cfg.Round, time.Now())
	if err != nil {
		fmt.Fprintf(os.Stderr, "运行错误: %v\n", err)
		return 2
	}
	defer rec.Close()

	fmt.Printf("输出目录: %s\n", rec.RunDir())
	totalRuns := len(exps) * len(set.Cases) * cfg.Repeats
	traces, runErr := engine.Run(context.Background(), engine.Options{
		Config:      cfg,
		Cases:       set,
		Experiments: exps,
		Registry:    registry,
		Completer:   upstream,
		Recorder:    rec,
		ModelVer:    os.Getenv("GQY_MODEL_VER"),
		Progress: func(done, total int, tr *result.Trace) {
			pass := tr.Judge != nil && tr.Judge.Pass
			fmt.Fprintf(os.Stderr, "[%d/%d] %-28s %-12s pass=%v status=%s\n",
				done, total, tr.RunID, tr.CaseID, pass, tr.Status)
		},
	})
	if runErr != nil {
		fmt.Fprintf(os.Stderr, "运行错误: %v\n", runErr)
		return 2
	}
	if len(traces) != totalRuns {
		fmt.Fprintf(os.Stderr, "运行错误: 期望 %d 条结果，实际 %d 条\n", totalRuns, len(traces))
		return 2
	}

	reportText := report.Build(cfg, set, traces, filepath.ToSlash(filepath.Join("..", rec.TraceRelDir())))
	if err := rec.WriteReport(reportText); err != nil {
		fmt.Fprintf(os.Stderr, "运行错误: 写报告失败: %v\n", err)
		return 2
	}
	passed := 0
	for _, tr := range traces {
		if tr.Judge != nil && tr.Judge.Pass {
			passed++
		}
	}
	fmt.Printf("\n完成: %d/%d 通过（%.1f%%）\n", passed, len(traces), 100*float64(passed)/float64(len(traces)))
	fmt.Printf("报告: %s\n", rec.ReportPath())
	fmt.Printf("逐次记录: %s\n", rec.RunsPath())
	if passed != len(traces) {
		return 1
	}
	return 0
}

func runReplay(dir string, filters []matrix.Filter) int {
	traces, err := replay.LoadDir(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "回放错误: %v\n", err)
		return 2
	}
	filtered := traces[:0]
	for _, tr := range traces {
		if traceMatches(tr, filters) {
			filtered = append(filtered, tr)
		}
	}
	if len(filtered) == 0 {
		fmt.Fprintln(os.Stderr, "回放错误: 过滤后没有 trace")
		return 2
	}
	fmt.Printf("回放 %d 条 trace（不请求模型）\n\n", len(filtered))
	fmt.Printf("%-4s %-32s %-14s %-8s %-8s %-8s\n", "#", "run_id", "case", "pass", "render", "judge")
	allOK := true
	for i, tr := range filtered {
		res, err := replay.Run(tr, tr.Config.StrictSchema)
		if err != nil {
			fmt.Fprintf(os.Stderr, "回放错误: %v\n", err)
			return 2
		}
		ok := res.Matches() && res.Verdict.Pass
		if !ok {
			allOK = false
		}
		fmt.Printf("%-4d %-32s %-14s %-8v %-8v %-8v\n", i+1, tr.RunID, tr.CaseID, res.Verdict.Pass, res.RenderMatch, res.JudgeMatch)
	}
	if allOK {
		return 0
	}
	return 1
}

func traceMatches(tr *result.Trace, filters []matrix.Filter) bool {
	for _, f := range filters {
		var actual string
		switch f.Key {
		case "prompt":
			actual = tr.Config.Prompt
		case "schema":
			actual = tr.Config.Schema
		case "return":
			actual = tr.Config.Return
		case "round":
			actual = tr.Round
		case "axis":
			actual = tr.Axis
		case "case":
			actual = tr.CaseID
		default:
			return false
		}
		found := false
		for _, v := range f.Values {
			if v == actual {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func toolNames(reg *tool.Registry) string {
	list := reg.List()
	names := make([]string, 0, len(list))
	for _, t := range list {
		names = append(names, t.Name())
	}
	return strings.Join(names, ", ")
}

func shortHash(h string) string {
	if len(h) > 12 {
		return h[:12] + "…"
	}
	return h
}
