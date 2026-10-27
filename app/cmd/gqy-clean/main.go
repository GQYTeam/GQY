// 命令 gqy-clean：按 TTL 清理 $GQY_HOME/sessions。
package main

import (
	"flag"
	"fmt"
	"log"
	"path/filepath"
	"time"

	"gqy/internal/config"
	"gqy/internal/session"
)

func main() {
	home := flag.String("home", "", "GQY_HOME（默认 ~/.gqy）")
	ttl := flag.Duration("ttl", 30*24*time.Hour, "保留时长，超过则删除")
	dry := flag.Bool("dry-run", false, "只打印将删除的会话")
	flag.Parse()
	if *home == "" {
		*home = config.Env("GQY_HOME", filepath.Join(config.HomeDir(), ".gqy"))
	}
	store := session.New(*home)
	removed, err := store.CleanExpired(time.Now(), *ttl, *dry)
	if err != nil {
		log.Fatalf("清理失败: %v", err)
	}
	for _, id := range removed {
		fmt.Println(id)
	}
	if *dry {
		fmt.Printf("将删除 %d 个会话（dry-run）\n", len(removed))
	} else {
		fmt.Printf("已删除 %d 个会话\n", len(removed))
	}
}
