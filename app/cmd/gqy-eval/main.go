// gqy-eval 是评测子命令：矩阵、单次对话循环、判定、落盘与回放（E1）。
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"gqy/internal/config"
	"gqy/internal/eval"
	"gqy/internal/gateway"
	"gqy/internal/plugin"
	"gqy/internal/provider"
	"gqy/internal/provider/anthropic"
	"gqy/internal/provider/gemini"
	"gqy/internal/provider/openai"
	"gqy/internal/tool"
)

func main() {
	configPath := flag.String("config", "", "评测配置 JSON 路径")
	outDir := flag.String("out", "", "输出目录")
	dryRun := flag.Bool("dry-run", false, "只打印矩阵与用例数，不发请求")
	replayDir := flag.String("replay", "", "从 trace 目录回放")
	filter := flag.String("filter", "", "按维度取值筛选，如 prompt=basic")
	flag.Parse()

	if *configPath == "" {
		fmt.Fprintln(os.Stderr, "用法: gqy-eval -config <path> -out <dir> [-dry-run|-replay <dir>] [-filter expr]")
		os.Exit(2)
	}
	if *outDir == "" && !*dryRun && *replayDir == "" {
		fmt.Fprintln(os.Stderr, "-out 必填（dry-run 除外）")
		os.Exit(2)
	}

	cfg, err := eval.LoadConfig(*configPath)
	if err != nil {
		fatal(err)
	}
	cases, err := eval.LoadCases(casesPath(*configPath))
	if err != nil {
		fatal(err)
	}

	if *replayDir == "" && !*dryRun {
		engine := buildEngine()
		runner := &eval.Runner{Cfg: cfg, Cases: cases, Engine: engine, Recorder: eval.NewRecorder(*outDir)}
		_, err = runner.Run(context.Background(), eval.Options{DryRun: *dryRun, ReplayDir: *replayDir, Filter: *filter})
	} else {
		runner := &eval.Runner{Cfg: cfg, Cases: cases, Engine: nil, Recorder: eval.NewRecorder(*outDir)}
		_, err = runner.Run(context.Background(), eval.Options{DryRun: *dryRun, ReplayDir: *replayDir, Filter: *filter})
	}
	if err != nil {
		fatal(err)
	}
}

func casesPath(configPath string) string {
	dir := filepath.Dir(configPath)
	return filepath.Join(dir, "cases.json")
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "gqy-eval:", err)
	os.Exit(1)
}

// gatewayRounder 把 gateway.Router.Complete 适配成 eval.Rounder。
type gatewayRounder struct {
	gw   *gateway.Router
	snap *config.Snapshot
}

func (g *gatewayRounder) Round(ctx context.Context, model string, messages []provider.Message, tools []provider.Tool) (provider.Response, error) {
	req := &provider.Request{Model: model, Messages: messages, Tools: tools}
	res, err := g.gw.Complete(ctx, g.snap, req, gateway.Meta{})
	if err != nil {
		return provider.Response{}, err
	}
	if res == nil || res.Response == nil {
		return provider.Response{}, nil
	}
	return *res.Response, nil
}

// buildEngine 装配工具注册表、插件运行时与网关。
func buildEngine() *eval.Engine {
	config.LoadDotEnv(".env")
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	startup := config.LoadStartup()
	mgr, err := config.NewManager(startup, os.Getenv)
	if err != nil {
		fatal(err)
	}
	snap := mgr.Current()

	reg := tool.NewRegistry()
	policy := tool.Policy{
		Workspace:      snap.Workspace,
		Roots:          append([]string(nil), snap.ToolRoots...),
		Denied:         []string{filepath.Join(snap.Home, "github_token.json")},
		ShellTimeout:   snap.Settings.ShellTimeoutS,
		MaxOutput:      16 * 1024,
		ShellAllowlist: append([]string(nil), snap.Settings.ShellAllowlist...),
	}
	for _, t := range []tool.Tool{
		tool.ReadTool{Policy: policy},
		tool.EditTool{Policy: policy},
		tool.ShellTool{Policy: policy},
		tool.ListDirTool{Policy: policy},
		tool.WebSearchTool{Policy: policy},
		tool.AskUserTool{},
	} {
		_ = reg.Register(t)
	}
	pm := plugin.NewManager(snap.Home)
	_ = pm.Scan()
	pm.RegisterTools(reg, true)

	gw := newGateway(snap.Home, log)
	decls := make([]eval.ToolDecl, 0)
	for _, t := range append(reg.Core(), reg.External()...) {
		decls = append(decls, eval.ToolDecl{Name: t.Name(), Schema: t.Schema()})
	}
	host := eval.NewHost(eval.RegistryExecutor{Reg: reg}, decls)
	return &eval.Engine{
		Rounder:   &gatewayRounder{gw: gw, snap: snap},
		Host:      host,
		MaxRounds: 5,
		TimeoutMS: 60000,
	}
}

func newGateway(home string, log *slog.Logger) *gateway.Router {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConns = 100
	transport.MaxIdleConnsPerHost = 20
	transport.IdleConnTimeout = 90 * time.Second
	transport.TLSHandshakeTimeout = 10 * time.Second
	client := &http.Client{Transport: transport}

	registry := provider.NewRegistry(
		openai.NewAdapter(client),
		anthropic.NewAdapter(client),
		gemini.NewAdapter(client),
	)
	return gateway.New(registry, log, home)
}
