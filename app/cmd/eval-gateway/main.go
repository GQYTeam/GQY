// 顾清影 · 工具调用评测网关。
//
// 用法：
//
//	go run ./cmd/eval-gateway -config evals/echo/config.json
//	go run ./cmd/eval-gateway -config ... -dry-run
//	go run ./cmd/eval-gateway -replay docs/test_call_echo/runs/.../trace/xxx.json
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"gqy/internal/config"
	"gqy/internal/eval"
	"gqy/internal/gateway"
	"gqy/internal/plugin"
	"gqy/internal/tool"
)

func main() {
	configPath := flag.String("config", "", "评测配置 JSON")
	out := flag.String("out", "", "覆盖输出目录")
	stage := flag.String("stage", "", "覆盖 stage：single / pair / full")
	dryRun := flag.Bool("dry-run", false, "只打印矩阵，不发请求")
	replay := flag.String("replay", "", "回放一个 trace JSON，只重新判定")
	withPlugins := flag.Bool("plugins", false, "加载 $GQY_HOME/plugins 下的全部插件")
	flag.Parse()

	if *replay != "" {
		replayTrace(*replay)
		return
	}
	if *configPath == "" {
		log.Fatal("需要 -config")
	}
	cfg, err := eval.LoadConfig(*configPath)
	if err != nil {
		log.Fatalf("读取配置失败：%v", err)
	}
	if *stage != "" {
		cfg.Stage = *stage
	}
	if *out != "" {
		cfg.Out = *out
	}
	set, err := eval.LoadCases(cfg.Cases)
	if err != nil {
		log.Fatalf("读取用例失败：%v", err)
	}
	variants := eval.GenerateMatrix(cfg)
	log.Printf("模型 %s · 工具 %s · 用例 %d · 变体 %d · 重复 %d",
		cfg.Model, cfg.Tool, len(set.Cases), len(variants), cfg.Repeats)
	if *dryRun {
		for _, v := range variants {
			fmt.Printf("  %-8s %-16s %s\n", v.Round, v.Axis, v.Label())
		}
		return
	}

	appCfg := config.Load()
	client := &http.Client{Timeout: 120 * time.Second}
	router := gateway.New(appCfg, client)
	defer router.Close()
	if !router.Available() {
		log.Fatal("没有可用 provider：配置 GQY_API_KEY 或 $GQY_HOME/providers.json")
	}

	registry := tool.NewRegistry()
	_ = registry.Register(tool.Read{})
	_ = registry.Register(tool.Edit{})
	_ = registry.Register(tool.Shell{})
	_ = registry.Register(tool.ListDir{})
	_ = registry.Register(tool.AskUser{})

	var pm *plugin.Manager
	if *withPlugins {
		pm = plugin.NewManager(appCfg.Home, registry)
		_ = pm.Discover()
		for _, st := range pm.List() {
			if err := pm.Load(context.Background(), st.ID); err != nil {
				log.Printf("插件 %s 加载失败：%v", st.ID, err)
			}
		}
		defer pm.Close()
	}

	label := cfg.Stage + "-" + time.Now().Format("20060102-150405")
	recorder, err := eval.NewRecorder(cfg.Out, label, cfg, set, variants)
	if err != nil {
		log.Fatalf("创建结果目录失败：%v", err)
	}
	defer recorder.Close()

	runner := &eval.Runner{Completer: router, Registry: registry}
	records, err := runner.Run(context.Background(), cfg, set, variants)
	if err != nil {
		log.Fatalf("评测失败：%v", err)
	}
	for _, rec := range records {
		if err := recorder.Add(rec); err != nil {
			log.Printf("写入结果失败：%v", err)
		}
	}
	best, complete, err := eval.Report(cfg.Out, label, cfg, variants, records)
	if err != nil {
		log.Fatalf("生成报告失败：%v", err)
	}
	passed := 0
	for _, rec := range records {
		if rec.Judge.Pass {
			passed++
		}
	}
	fmt.Printf("完成 %d 次执行，通过 %d 次\n", len(records), passed)
	fmt.Printf("报告：%s\n", filepath.Join(cfg.Out, "report", label+".md"))
	fmt.Printf("胜出：%s（%v）\n", best.Label(), complete)
}

func replayTrace(path string) {
	raw, err := os.ReadFile(path)
	if err != nil {
		log.Fatalf("读取 trace 失败：%v", err)
	}
	var rec eval.RunRecord
	if err := json.Unmarshal(raw, &rec); err != nil {
		log.Fatalf("解析 trace 失败：%v", err)
	}
	judge := eval.Judge(rec.Case, rec.ToolCalls, rec.FinalAnswer, rec.Status)
	fmt.Printf("case=%s pass=%v reasons=%v\n", rec.CaseID, judge.Pass, judge.Reasons)
}
