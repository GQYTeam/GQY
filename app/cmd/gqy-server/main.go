// 顾清影 · 服务端入口。
//
// 结构：cmd/gqy-server（入口）→ internal/httpapi（路由）→ internal/{catalog,provider,session,config}。
// 只用 Go 标准库，零第三方依赖；密钥只在服务端环境变量。
package main

import (
	"context"
	"errors"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"gqy/internal/config"
	"gqy/internal/httpapi"
)

func main() {
	// 生产环境二进制可能与源码分离：先读可执行文件旁的 .env，再读当前目录的 .env。
	if exe, err := os.Executable(); err == nil {
		config.LoadDotEnv(filepath.Join(filepath.Dir(exe), ".env"))
	}
	config.LoadDotEnv(".env")

	root := findStaticRoot()
	if root == "" {
		log.Fatalf("找不到静态前端：请在 app/ 里运行，或把二进制和 index.html 放在一起")
	}

	cfg := config.Load()

	client := &http.Client{
		// 不设整体超时：SSE 是长连接。只在等响应头时兜底。
		Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			ResponseHeaderTimeout: 30 * time.Second,
			MaxIdleConnsPerHost:   4,
		},
	}

	api := httpapi.New(root, cfg, client)
	defer api.Close()

	srv := &http.Server{
		Addr:              net.JoinHostPort(cfg.Host, cfg.Port),
		Handler:           api.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		log.Println("关闭中")
		_ = srv.Shutdown(shutdown)
	}()

	log.Printf("顾清影 · http://%s", srv.Addr)
	log.Printf("  provider : %s", cfg.Provider())
	if cfg.Provider() == "openai-compatible" {
		log.Printf("  model    : %s", cfg.Model)
		log.Printf("  wire     : %s", cfg.Wire)
	}
	log.Printf("  sessions : %s", filepath.Join(cfg.Home, "sessions"))
	log.Printf("  workspace: %s", cfg.Workspace)
	log.Printf("  toolroots: %s", strings.Join(cfg.ToolRoots, string(os.PathListSeparator)))
	if len(cfg.ShellAllowlist) == 0 {
		log.Printf("  shell    : 未限制命令（GQY_SHELL_ALLOWLIST 为空），超时 %s", cfg.ShellTimeout)
	} else {
		log.Printf("  shell    : 仅允许 %s，超时 %s", strings.Join(cfg.ShellAllowlist, ","), cfg.ShellTimeout)
	}
	if cfg.Provider() == "none" {
		log.Printf("  提示     : 未配置 GQY_API_KEY")
	}

	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("启动失败: %v", err)
	}
}

// findStaticRoot 定位前端静态目录：优先当前目录，其次可执行文件所在目录。
func findStaticRoot() string {
	if cwd, err := os.Getwd(); err == nil {
		if _, err := os.Stat(filepath.Join(cwd, "index.html")); err == nil {
			return cwd
		}
	}
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		if _, err := os.Stat(filepath.Join(dir, "index.html")); err == nil {
			return dir
		}
	}
	return ""
}
