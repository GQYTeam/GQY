// gqy-server 是常驻服务：HTTP API、网关、agent loop、插件运行时。
package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"gqy/internal/agent"
	"gqy/internal/buildinfo"
	"gqy/internal/config"
	"gqy/internal/gateway"
	"gqy/internal/goal"
	"gqy/internal/httpapi"
	"gqy/internal/plugin"
	"gqy/internal/provider"
	"gqy/internal/provider/anthropic"
	"gqy/internal/provider/gemini"
	"gqy/internal/provider/openai"
	"gqy/internal/storage"
	"gqy/internal/tool"
)

func main() {
	os.Exit(run())
}

func run() int {
	config.LoadDotEnv(".env")
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	startup := config.LoadStartup()
	if err := storage.EnsureRoot(startup.Home); err != nil {
		log.Error("数据根初始化失败", "home", startup.Home, "error", err)
		return 1
	}

	mgr, err := config.NewManager(startup, os.Getenv)
	if err != nil {
		log.Error("配置加载失败", "error", err)
		return 1
	}
	snap := mgr.Current()

	if snap.AuthToken == "" && !isLoopbackHost(startup.Host) {
		log.Warn("未设置 GQY_AUTH_TOKEN 且监听非回环地址，非本机请求将被拒绝",
			"host", startup.Host,
			"hint", "设置 GQY_AUTH_TOKEN 或把 HOST 改为 127.0.0.1",
		)
	}

	gw := newGateway(snap.Home, log)
	api := httpapi.New(mgr, gw, log)
	if webDir := resolveWebDir(); webDir != "" {
		api.SetWebDir(webDir)
		log.Info("Web UI 已挂载", "dir", webDir)
	} else {
		log.Warn("未找到 Web UI 静态目录", "hint", "设置 GQY_WEB_DIR，或在 app/ 下启动")
	}
	pm := plugin.NewManager(snap.Home)
	if err := pm.Scan(); err != nil {
		log.Warn("插件扫描失败", "error", err)
	}
	api.SetPlugins(pm)
	api.SetGoals(goal.NewManager())
	api.SetAgent(newAgentLoop(snap, gw, pm, startup.EnableExternal))
	srv := &http.Server{
		Addr:              net.JoinHostPort(startup.Host, startup.Port),
		Handler:           api.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	hup := make(chan os.Signal, 1)
	signal.Notify(hup, syscall.SIGHUP)
	defer signal.Stop(hup)

	errCh := make(chan error, 1)
	go func() {
		log.Info("gqy-server 启动",
			"version", buildinfo.String(),
			"addr", srv.Addr,
			"home", snap.Home,
			"config_version", snap.Version,
			"providers", len(snap.Providers),
			"auth_enabled", snap.AuthToken != "",
			"rate_limit", snap.RateLimit,
		)
		errCh <- srv.ListenAndServe()
	}()

	for {
		select {
		case err := <-errCh:
			closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = pm.Close(closeCtx)
			cancel()
			if err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Error("HTTP 服务退出", "error", err)
				return 1
			}
			return 0

		case <-hup:
			if next, err := mgr.Reload(); err != nil {
				log.Warn("SIGHUP 配置重载失败", "error", err)
			} else {
				log.Info("SIGHUP 配置重载成功", "version", next.Version)
			}

		case <-ctx.Done():
			log.Info("收到退出信号，等待在途请求结束")
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			if err := srv.Shutdown(shutdownCtx); err != nil {
				log.Error("优雅关闭失败", "error", err)
			}
			if err := pm.Close(shutdownCtx); err != nil {
				log.Warn("插件关闭有失败", "error", err)
			}
			cancel()
			return 0
		}
	}
}

func isLoopbackHost(host string) bool {
	if host == "" {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func resolveWebDir() string {
	if v := strings.TrimSpace(os.Getenv("GQY_WEB_DIR")); v != "" {
		if abs, err := filepath.Abs(v); err == nil {
			return abs
		}
		return v
	}
	exe, err := os.Executable()
	exeDir := "."
	if err == nil {
		exeDir = filepath.Dir(exe)
	}
	candidates := []string{".", "app", exeDir, filepath.Join(exeDir, "..")}
	for _, dir := range candidates {
		if info, err := os.Stat(filepath.Join(dir, "index.html")); err == nil && !info.IsDir() {
			if abs, err := filepath.Abs(dir); err == nil {
				return abs
			}
			return dir
		}
	}
	return ""
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

func newAgentLoop(snap *config.Snapshot, gw *gateway.Router, pm *plugin.Manager, externals bool) *agent.Loop {
	policy := tool.Policy{
		Workspace:      snap.Workspace,
		Roots:          append([]string(nil), snap.ToolRoots...),
		Denied:         []string{filepath.Join(snap.Home, "github_token.json")},
		ShellTimeout:   snap.Settings.ShellTimeoutS,
		MaxOutput:      16 * 1024,
		ShellAllowlist: append([]string(nil), snap.Settings.ShellAllowlist...),
	}
	reg := tool.NewRegistry()
	for _, t := range []tool.Tool{
		tool.ReadTool{Policy: policy},
		tool.EditTool{Policy: policy},
		tool.ShellTool{Policy: policy},
		tool.ListDirTool{Policy: policy},
		tool.WebSearchTool{Policy: policy},
		tool.AskUserTool{},
	} {
		if err := reg.Register(t); err != nil {
			panic(err)
		}
	}
	pm.RegisterTools(reg, externals)
	return agent.NewLoop(gw, reg)
}
