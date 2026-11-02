// gqy-run 是一次性插件执行子命令（15 NX1–NX7、009 施工单）。
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"gqy/internal/config"
	"gqy/internal/oneshot"
)

func main() {
	keep := flag.Bool("keep", false, "保留工作目录")
	noCache := flag.Bool("no-cache", false, "跳过缓存")
	id := flag.String("id", "", "覆盖插件 id（用于目录名与缓存键）")
	argsJSON := flag.String("args", "{}", "一次性参数 JSON")
	flag.Parse()
	if flag.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "用法: gqy run <来源> [--args '<json>'] [--keep] [--no-cache] [--id <id>]")
		os.Exit(2)
	}

	config.LoadDotEnv(".env")
	startup := config.LoadStartup()

	var args map[string]any
	if err := json.Unmarshal([]byte(*argsJSON), &args); err != nil {
		fmt.Fprintln(os.Stderr, "参数 JSON 解析失败:", err)
		os.Exit(2)
	}

	res := oneshot.Run(context.Background(), oneshot.Request{
		Home:    startup.Home,
		Source:  flag.Arg(0),
		Args:    args,
		Keep:    *keep,
		NoCache: *noCache,
		ID:      *id,
	})

	out, err := json.MarshalIndent(res, "", "  ")
	if err != nil {
		fmt.Fprintln(os.Stderr, "结果序列化失败:", err)
		os.Exit(1)
	}
	fmt.Println(string(out))
	if !res.OK {
		os.Exit(1)
	}
}
