# 工具调用评测网关

依据 `docs/AboutAitestCallToolOrPlugin.md` 与 `docs/design/EvalGateway.md`，用独立 CLI 复现「提示词 / 工具说明 / 返回值渲染」三个维度的实验矩阵。

代码在 `internal/eval/`，入口 `cmd/gqy-eval`。零第三方依赖，配置和用例都用标准库 `encoding/json`。

## 命令

```bash
# 只看矩阵，不执行、不落盘
go run ./cmd/gqy-eval -config internal/eval/testdata/sample.json -dry-run

# 真实运行：需要可用 provider（GQY_API_KEY 或 $GQY_HOME/providers.json）和输出目录
export GQY_API_KEY=sk-...
export GQY_API_BASE=https://api.deepseek.com/v1   # 可选，单 provider 时使用
go run ./cmd/gqy-eval -config internal/eval/testdata/sample.json -out /tmp/gqy-eval

# 过滤配置；-filter 可重复，值用逗号分隔
go run ./cmd/gqy-eval -config ... -out /tmp/eval \
  -filter prompt=basic -filter return=raw,summary

# 从 trace 回放，不请求模型
go run ./cmd/gqy-eval -config ... -replay /tmp/gqy-eval/runs/r1-20260101-120000/trace

# 回放也支持 -filter（prompt/schema/return/round/axis/case）
```

退出码：

| 码 | 含义 |
| --- | --- |
| 0 | 全部通过（dry-run 正常打印；回放全部 verdict 通过且渲染/判定一致） |
| 1 | 有失败样本（或回放出现 verdict/渲染/判定不一致） |
| 2 | 配置、用例、过滤、装配、IO 等运行错误 |

`-out` 在非 dry-run、非 replay 模式必填；所有评测结果只写到 CLI 指定目录，配置里没有输出路径字段。

## 配置

```json
{
  "name": "echo-tool",
  "tool": "plugin__echo__echo",
  "model": "deepseek/deepseek-chat",
  "cases": "cases.json",
  "repeats": 1,
  "concurrency": 2,
  "max_rounds": 4,
  "strict_schema": true,
  "judge_model": "",
  "stage": "full",
  "round": "r1",
  "axes": {
    "prompt": ["none", "basic"],
    "schema": ["min", "full"],
    "return": ["raw", "summary"]
  },
  "fixed": {"prompt": "basic", "schema": "full", "return": "raw"},
  "timeouts": {
    "request_ms": 60000,
    "tool_ms": 10000,
    "total_ms": 180000,
    "retries": 1,
    "backoff_ms": 100,
    "backoff_max_ms": 1000
  }
}
```

| 字段 | 说明 |
| --- | --- |
| `name` / `tool` / `model` | 必填；`tool` 必须是评测宿主已注册的工具名 |
| `cases` | 用例 JSON 文件或目录，相对配置文件目录解析 |
| `repeats` | 每个「配置 × 用例」重复次数，默认 1 |
| `concurrency` | 并发配置组数，默认 1；组内用例与重复串行 |
| `max_rounds` | 工具调用轮次上限，默认 6；超过记为 `not_converged` |
| `strict_schema` | 参数校验失败时是否拦截执行，默认 false |
| `stage` | `single` / `pair` / `full` |
| `round` | 轮次标签，默认 `r1`；写进每条记录 |
| `axes` | 三个维度的枚举值，不能为空，不允许重复 |
| `fixed` | 单因素/两两组合时固定其余维度的默认值 |
| `timeouts` | 请求/工具/总超时、重试次数与指数退避上限；0 使用默认值 |
| `judge_model` | 可选模型裁判；规则判定仍是主判定，模型裁判单独记录 |

校验错误带字段路径，例如 `axes.prompt[1]: 未知变体 "nope"`、`fixed.schema: 未知变体 "wat"`；JSON 语法错误带文件名和行列号。

### 维度变体

| 维度 | 取值 |
| --- | --- |
| prompt | `none` `basic` `when` `when-how` `fewshot` `strict` |
| schema | `min` `full` `example` `negative` `alias` |
| return | `raw` `trim` `summary` `typed` `error-rich` `error-min` |

### 矩阵语义

- `single`：基线（全 fixed）+ 每次只变一个维度。
- `pair`：基线 + 每次同时变两个维度（两个维度都取非 fixed 值），第三个固定。
- `full`：三维全组合。
- 组合按 `(prompt, schema, return)` 去重，顺序固定为 prompt → schema → return。
- 每个实验带 `round` 和 `axis` 标签：`baseline`、`prompt`、`schema`、`return`、`prompt+schema`、`prompt+return`、`schema+return`、`full`。
- `-filter` 支持 `prompt` `schema` `return` `round` `axis` `case`；多个同键过滤取交集，不同键也取交集。

## 用例

文件或目录（目录按文件名排序合并所有 `*.json`）：

```json
{
  "version": "1",
  "cases": [
    {
      "id": "echo-basic",
      "input": "把我说的这句话原样回显：hello",
      "expect": {
        "should_call": true,
        "tool": "plugin__echo__echo",
        "arg_contains": {"text": "hello"},
        "final_contains": ["hello"]
      },
      "inject": {"mode": "error", "tool": "plugin__echo__echo", "times": 1}
    }
  ]
}
```

- `input` 与 `messages` 二选一；`messages` 支持 `user` / `system` / `assistant`，至少一条 user。
- `expect.should_call` 用指针区分「未设置」和「显式 false」；`must_call` 是同义别名（两者同时出现且冲突会校验报错）。显式 false 或 `must_not_call` 都要求零调用。
- `arg_contains` 支持点号路径（`nested.id`）、字符串包含、数组包含子集、对象递归匹配。
- `final_contains` 是关键事实包含，大小写不敏感；不做答案完全相等比较。
- `inject.mode`：`empty`（空结果）/ `error`（错误）/ `timeout`（超时）/ `slow`（慢响应）；`times` 默认 1，`slow` 默认 100ms。
- 用例集版本哈希覆盖 `version`、来源文件列表、过滤子集和全部用例内容；任何变化都会改变哈希。

## 单次运行

```
messages = [system(按 prompt 变体)] + 用例消息
tools    = [按 schema 变体渲染的工具]
for round in 0..max_rounds:
  resp = completer.Complete(messages, tools)      # 请求超时 + 指数退避重试
  if no tool_calls: 最终答案，结束
  for call:
    解析参数 + 本地 schema 校验
    strict_schema 且不合法 → 不执行，记录错误，回填
    故障注入匹配 → 返回注入结果
    否则 tool.Registry 执行工具（工具超时）
    按 return 变体渲染，回填 role=tool
超过轮次 → not_converged
```

- 重试只针对可重试错误（请求超时、网络错误、429、5xx）；工具超时/错误不重试。
- 每次重试写入 `trace.retries[]`，`retry_of` 指向被重试的尝试 ID（`{run_id}#r{round}-a{attempt}`），运行行顶层也带 `retry_of`。
- 并发按配置组合分组，组内串行；不同配置组最多 `concurrency` 个并行。
- `strict_schema=false` 时，参数不合法仍执行，让模型看到真实工具结果；`strict_schema=true` 时拦截并回填参数错误。

## 判定与状态

规则（每条都会写进 `judge.rule.rules`）：

| 规则 | 含义 |
| --- | --- |
| `status_ok` | 运行状态必须为 `ok` |
| `should_call` | 显式要求调用 / 不调用 |
| `must_not_call` | 禁止调用 |
| `expected_tool` | 至少一次调用命中期望工具（按真实工具名） |
| `arg_contains` | 至少一次调用同时满足全部参数约束 |
| `final_contains` | 最终答案包含全部关键事实 |
| `schema_valid` | 所有工具调用参数合法；只有 `strict_schema=true` 时影响通过 |

状态语义：

| 状态 | 含义 |
| --- | --- |
| `ok` | 正常结束（可能仍然判定失败，那是模型决策问题） |
| `not_converged` | 达到最大轮次仍在调用工具 |
| `timeout` | 模型请求或工具执行超时 |
| `gateway_error` | 模型上游失败（重试耗尽后） |
| `exec_error` | 工具执行失败、未知工具、严格校验拦截 |

运行时错误与模型决策错误分开：`judge.runtime=true` 表示运行期异常；`pass=false` 且 `runtime=false` 才是模型调用决策/参数/答案问题。

## 落盘

```
<out>/
  configs/{round}-{ts}.json          # 配置快照（含用例哈希、网关/判定器版本）
  runs/{round}-{ts}/
    runs.jsonl                       # 每次运行一行
    trace/{run_id}.json              # 完整轨迹（多轮请求/响应、工具调用、重试）
    cases/cases.json                 # 用例快照
    errors.log                       # 网关/工具异常，脱敏
  report/{round}-{ts}.md             # 汇总报告
```

- 同一秒重名时目录追加 `-2`、`-3`，不覆盖历史。
- 配置、trace、用例快照、报告用临时文件 + rename 原子写；`runs.jsonl` 每行写入后 sync。
- 所有落盘内容先脱敏：字段名 `api_key/token/secret/authorization/password/...` 递归替换为 `***`，自由文本里的 `sk-...`、`Bearer ...`、`ghp_...` 也会被替换。
- `runs.jsonl` 每行包含设计文档约定的 `run_id`、`case_id`、`repeat_idx`、`config`、`round`、`axis`、`messages`、`tool_calls`、`final_answer`、`rounds_used`、`tokens{in,out,total}`、`judge.rule{pass,reasons,rules}`、`status`，另加 `retry_of` / `retries` / `started_at`。

报告包含：配置摘要、按端到端成功率排序的矩阵表、三个维度的单因素对比、交互效应说明、组合观察、失败反例 Top 10（带 trace 链接）、成本汇总、结论。没有样本时明确写「不生成结论」；有样本时只引用本轮快照，不编造跨轮结论。

## 回放

`-replay <dir>` 接受 `trace/` 目录，也接受包含 `trace/` 的 run 目录。回放：

1. 不实例化 Completer，不发任何模型请求；
2. 使用 trace 里记录的模型响应、工具原始返回值和错误；
3. 用当前 `return` 渲染器重新渲染，用当前判定器重新判定；
4. 输出 `render` / `judge` 是否与录制一致。

渲染或判定不一致、或 verdict 失败都会让退出码为 1，适合做渲染器/判定器改动的回归。

## 测试

```bash
cd app
gofmt -w ./cmd ./internal
go vet ./...
go test ./...
npm run check
go run ./cmd/gqy-eval -config internal/eval/testdata/sample.json -dry-run
```

评测相关测试不联网：使用假 Completer 和假工具，落盘全部用 `t.TempDir()`。覆盖矩阵生成/去重、配置校验、用例哈希、Runner 多轮/超时/重试/严格校验/故障注入、Judge 各规则与状态、Recorder 结构与脱敏、Replay 一致性、报告排序。

## 与设计文档的差异

- 配置用 JSON（标准库），不引入 YAML 依赖；不使用 `docs/test_call_{tool}/` 的固定目录，结果目录完全由 `-out` 指定。
- 入口是 `cmd/gqy-eval`，不是设计里的 `cmd/eval-gateway`；包结构按实现拆成 config/cases/matrix/runner/judge/recorder/report/replay/engine。
- 评测宿主注册核心工具（`read/edit/shell/list_dir/websearch`）和一个确定性的 `plugin__echo__echo` 占位适配器；`-config` 指定 `<pluginID>__<action>` 时，宿主按前缀从 `$GQY_HOME/plugins` 加载真实插件并注册其动作。远程插件（unix/tcp）与服务依赖已在插件运行时实现，评测宿主按工具名加载；评测结论只代表本次加载的工具实现。
- 只做规则判定；模型裁判、人工抽检未接。
- 没有 `best/current.*` 自动写回；CLI 不会修改线上配置。
- `stage=replay` 不写在配置里，改用 CLI `-replay`。
- 没有令牌桶限流；只有请求级超时、指数退避重试和并发组上限。
- 温度由 `internal/provider` 决定：Runner 传 `Effort=Low`（当前实现为 0.2），不是严格的 0。
- `models`/`model_ver` 记录来自配置和 `GQY_MODEL_VER` 环境变量；没有自动探测模型版本。

## 剩余风险

- 真实模型调用走 `internal/gateway`：`GQY_API_KEY` 或 `$GQY_HOME/providers.json` 任一可用即可；都没有只能 dry-run/replay。
- 评测宿主的 `read/edit/shell/list_dir` 复用服务端的路径沙箱配置（`GQY_WORKSPACE`、`GQY_TOOL_ROOTS`、`GQY_SHELL_ALLOWLIST`、超时与输出上限），并拒绝 `$GQY_HOME/github_token.json`。当前没有交互式审批，评测用例不应让模型执行破坏性命令。
- trace 脱敏基于字段名和常见密钥前缀，非常规密钥格式可能漏网；新增密钥形态时要补规则。
- 并发下 Completer 必须线程安全；`gateway.Router` 与 `provider.Upstream` 均满足；自定义 Completer 需自行保证。
- 回放只重新执行渲染与判定，不会重放工具副作用；工具原始结果以录制的 `ret_raw` 为准。
