// gqy-clean 是维护子命令：按会话或按 TTL 清理过期数据（ST7、S6）。
package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"gqy/internal/config"
	"gqy/internal/session"
	"gqy/internal/storage"
)

func main() {
	sessionID := flag.String("session", "", "清理指定会话 id")
	expired := flag.Bool("expired", false, "清理所有过期会话")
	flag.Parse()

	config.LoadDotEnv(".env")
	startup := config.LoadStartup()

	switch {
	case *sessionID != "":
		store := session.NewStore(startup.Home)
		if err := store.Delete(*sessionID); err != nil {
			fmt.Fprintln(os.Stderr, "清理失败:", err)
			os.Exit(1)
		}
		fmt.Printf("已删除会话 %s\n", *sessionID)
	case *expired:
		removed, err := storage.CleanExpired(startup.Home, time.Now())
		if err != nil {
			fmt.Fprintln(os.Stderr, "清理失败:", err)
			os.Exit(1)
		}
		// NX6：一次性插件缓存默认 TTL 7 天，与过期会话一起回收。
		oneshotRemoved, err := storage.CleanOneshotCache(startup.Home, time.Now(), 7*24*time.Hour)
		if err != nil {
			fmt.Fprintln(os.Stderr, "清理一次性插件缓存失败:", err)
			os.Exit(1)
		}
		fmt.Printf("已清理过期会话 %d 个、一次性插件缓存 %d 个\n", removed, oneshotRemoved)
	default:
		fmt.Fprintln(os.Stderr, "用法: gqy-clean --session <id> | --expired")
		os.Exit(2)
	}
}
