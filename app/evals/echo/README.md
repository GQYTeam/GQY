# Echo 评测示例

前置：

1. 配好 `GQY_API_KEY` 或 `$GQY_HOME/providers.json`。
2. 把仓库 `plugins/echo` 复制到 `$GQY_HOME/plugins/echo`。评测宿主会按 `config.json` 的 `tool`（`echo__echo`）只加载这个插件。

只生成矩阵：

```bash
cd app
go run ./cmd/gqy-eval -config evals/echo/config.json -dry-run
```

真实运行：

```bash
cd app
go run ./cmd/gqy-eval -config evals/echo/config.json -out ../docs/test_call_echo
```

结果：

- `configs/`：配置快照
- `runs/<round>-<ts>/runs.jsonl`：逐次执行
- `runs/<round>-<ts>/trace/`：完整轨迹
- `report/`：汇总报告
- `best/current.json`：完整一轮的胜出配置

`model` 改成你要评测的 `provider/model`；`tool` 是插件动作注册后的工具名（`<pluginID>__<action>`）。详细字段见 `docs/eval.md`。
