# 评测网关设计

依据：`docs/AboutAitestCallToolOrPlugin.md`。目标是用一个独立 CLI 复现“提示词 / 工具说明 / 返回值渲染”三个维度的实验，结果落到 `docs/test_call_<tool>/`。

## 范围

**做：**

- JSON 配置（不引入 YAML 依赖）：用例、三个维度、重复次数、并发与超时。输出目录由 CLI `-out` 指定。
- 矩阵生成：单因素、两两组合、全组合；每轮打 `round` / `axis` 标签。
- 单次对话循环：渲染 prompt / tools / 返回值 → 请求模型 → 执行工具 → 回填 → 直到最终答案或轮次上限。
- 规则判定：是否调用、调用哪个、参数包含、最终答案关键事实、禁止调用。
- 落盘：配置快照、`runs.jsonl`、完整 trace、用例快照、`report.md`、`best/current.json`。
- 重放：从 trace 回放模型响应，验证判定器和渲染改动。
- 故障注入：空结果、错误、超时、慢响应。

**不做：**

- 模型裁判（后续可接）。
- 跨轮比较、自动写回线上配置。
- 并行压测；先按配置串行，组间小并发。

## 配置

`evals/<tool>/config.json`：

```json
{
  "name": "echo-tool",
  "model": "deepseek/deepseek-chat",
  "tool": "echo__echo",
  "cases": "cases.json",
  "repeats": 1,
  "concurrency": 1,
  "max_rounds": 4,
  "strict_schema": true,
  "stage": "single",
  "round": "r1",
  "axes": {
    "prompt": ["basic", "when-how", "strict"],
    "schema": ["min", "full", "example"],
    "return": ["raw", "summary"]
  },
  "fixed": {"prompt": "basic", "schema": "full", "return": "raw"},
  "timeouts": {"request_ms": 60000, "tool_ms": 10000, "total_ms": 180000, "retries": 1}
}
```

- `tool`：评测宿主已注册的工具名；真实插件工具是 `<pluginID>__<action>`，宿主持有 `$GQY_HOME/plugins` 下对应插件。
- `stage`：`single`（固定两维，扫第三维）、`pair`（两两组合）、`full`。
- `fixed`：单因素/两两组合时的默认值。
- `round`：轮次标签，写进记录与目录。
- `max_rounds`：单次对话最多工具轮次。
- `strict_schema`：参数校验失败时是否拦截执行。
- `timeouts`：请求 / 工具 / 总超时与重试退避。
- 输出目录不在配置里，由 CLI `-out` 必填（dry-run/replay 除外）。完整字段见 `docs/eval.md`。

`cases.json`：

```json
{
  "version": "1",
  "cases": [
    {
      "id": "echo-basic",
      "input": "把我说的这句话原样回显：hello",
      "expect": {
        "should_call": true,
        "tool": "echo__echo",
        "arg_contains": {"text": "hello"},
        "final_contains": ["hello"],
        "must_not_call": false
      }
    }
  ]
}
```

故障注入写成对象：

```json
{"inject": {"mode": "error", "tool": "echo__echo", "times": 1, "message": "注入故障"}}
```

`mode`：`empty` / `error` / `timeout` / `slow`；`slow` 用 `delay_ms`。

## 维度渲染

### Prompt

| 变体 | 渲染 |
| --- | --- |
| `none` | 不加工具说明 |
| `basic` | “可用工具如下，需要时调用。” |
| `when` | 加“什么时候该调 / 不该调” |
| `when-how` | 再加参数填写要求 |
| `fewshot` | 加 1 正例 + 1 反例 |
| `strict` | 强约束：不确定不调；需要真实信息必须先调 |

### Schema

| 变体 | 渲染 |
| --- | --- |
| `min` | name + required 字段，description 截断到一句话 |
| `full` | 原 schema |
| `example` | full + 每个必填参数给示例 |
| `negative` | full + “不要用它做什么” |
| `alias` | 工具名换成短别名（验证命名影响） |

### Return

| 变体 | 渲染 |
| --- | --- |
| `raw` | 原样字符串 |
| `trim` | 若为 JSON，只保留 key，截断长值 |
| `summary` | “工具返回：<首 500 字>” |
| `typed` | 加类型标注（string/number/bool/array/object） |
| `error-rich` | 错误时加 `code` / `hint` / `retryable` |
| `error-min` | 错误时只给“失败” |

## 单次运行

```
for case, config, repeat:
  messages = [system(prompt), user(case.input)]
  for round in 0..max_rounds:
    tools = schemaVariant(tool)
    resp = complete(messages, tools)
    record request/response
    if no tool_calls: break
    for call:
      validate name/args (record, optionally block)
      result, err = registry.Execute(...)
      content = renderReturn(result, err, variant)
      messages += assistant(tool_calls), tool(content)
  judge(case, trace)
```

- 工具执行复用 `tool.Registry` / 插件运行时。
- 故障注入按 case 的 `inject` 字段：`empty` / `error` / `timeout` / `slow`。
- 超时与轮次上限记为 `not_converged`，仍进入判定。

## 判定

```go
type Verdict struct {
    Pass bool
    Reasons []string
    Rules map[string]bool
}
```

规则：

- `should_call`：至少一次工具调用；`false` 时断言零调用。
- `tool`：调用名匹配。
- `arg_contains`：参数 JSON 包含指定子串/值。
- `final_contains`：最终答案包含全部关键事实（不要求完全相等）。
- `must_not_call`：禁止调用。
- 运行时错误与模型错误分开：`status` 为 `gateway_error` / `exec_error` / `not_converged` / `timeout`。

## 落盘

```
<out>/
  configs/{round}-{ts}.json
  runs/{round}-{ts}/
    runs.jsonl
    trace/{run_id}.json
    cases/cases.json
    errors.log
  report/{round}-{ts}.md
  best/current.json
```

`runs.jsonl` 每行：

```json
{
  "run_id":"...","case_id":"echo-basic","repeat_idx":0,
  "config":{"prompt":"basic","schema":"full","return":"raw","model":"...","gateway_ver":"1"},
  "round":"single","axis":"return",
  "messages_masked":[...],
  "tool_calls":[{"name":"...","args_raw":"...","schema_valid":true,"exec_ok":true,"ret_rendered":"...","latency_ms":12}],
  "final_answer":"...","rounds_used":1,"tokens":{"in":10,"out":8},"total_ms":120,
  "judge":{"rule":{"pass":true,"reasons":[]}},
  "status":"ok"
}
```

报告固定包含：配置摘要、矩阵表、单因素对比、反例清单、结论、成本。`best/current.json` 只在完整一轮跑完后由 CLI 写入。

## 防漂移

- 用例集与配置带版本号；跑时快照。
- 判定器版本写进配置；改动判定器 = 新轮次。
- `runs.jsonl` 写入前对 `api_key/token/secret/authorization` 字段脱敏。
- 禁止跨轮比较：报告只引用同轮快照。

## CLI

```bash
go run ./cmd/gqy-eval -config evals/echo/config.json -dry-run
go run ./cmd/gqy-eval -config evals/echo/config.json -out ../docs/test_call_echo
go run ./cmd/gqy-eval -config ... -replay /tmp/eval/runs/r1-.../trace
go run ./cmd/gqy-eval -config ... -out /tmp/eval -filter prompt=basic
```

实现包：`internal/eval/{config,cases,matrix,runner,judge,result,recorder,report,replay,engine,host}`；`host` 注册核心工具与工具名对应的真实插件。

`-dry-run` 只打印矩阵与用例数，不发请求。

## 测试

- 假上游：第一次返回工具调用，第二次返回最终答案；断言 2 次请求、判定通过、文件齐全。
- 矩阵生成：单因素数量 = 变体数；两两组合去重正确。
- 返回值渲染：raw/trim/summary/typed 的输出符合预期。
- 重放：从 trace 回放不发起网络请求。
