# eval · 评测

## 1. 状态

待施工（2026-11-02）。

## 2. 对应设计决定

兑现 `designs/07-评测.md` 的 E1–E10：

- E1 评测是独立 CLI `gqy-eval`，一次性任务，复用 gateway / tool / plugin，不占 `gqy-server` 并发额度。
- E2 三维矩阵 prompt / schema / return；single / pair / full 三阶段；round / axis 标签；`-filter` / `-dry-run` 筛选。
- E3 用例集是「输入 + 期望行为」，不逐字相等；用例集与判定器带版本、一轮内冻结。
- E4 单次对话循环：渲染 → 请求 → 工具执行 → 回填 → 最终答案；超时 / 超轮记 `not_converged`。
- E5 判定规则优先、模型裁判补充；运行时错误与模型决策错误分开（复用 03 C9）。
- E6 落盘全量录制、带版本；脱敏；禁止跨轮比较；`best/current.json` 只由完整轮次写入。
- E7 回放从 trace 不发真实请求，标记 `replay`。
- E8 报告固定七项；指标含调用决策、工具选择、参数合规、返回值利用、端到端成功率、成本、稳定性。
- E9 契约测试管链路正确，评测管能力正确；共享同一套 tool / plugin 运行时。
- E10 成本与并发：组间小并发、组内串行、限流退避重试落盘、工具超时不重试、增量模式。

## 3. 具体形状

### 3.1 CLI 子命令与参数

`gqy-eval` 是独立子命令（01 D4），不是服务端点：

| 参数 | 必填 | 说明 |
| --- | --- | --- |
| `-config <path>` | 是 | 评测配置 JSON 路径 |
| `-out <dir>` | 是（dry-run / replay 除外） | 输出目录 |
| `-dry-run` | 否 | 只打印矩阵与用例数，不发请求 |
| `-replay <trace dir>` | 否 | 从 trace 目录回放 |
| `-filter <expr>` | 否 | 按维度取值筛选，如 `prompt=basic` |

### 3.2 用例文件形状

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
      },
      "inject": null
    }
  ]
}
```

- `expect` 五个字段都是可选的判定条件，缺省即不判该项。
- `inject` 故障注入对象：`{"mode": "empty|error|timeout|slow", "tool": "…", "times": 1, "message": "…", "delay_ms": 500}`。
- 用例集带 `version`；跑一轮期间不改，新增进下一轮。

### 3.3 三维矩阵变体枚举

| 维度 | 变体（代号） |
| --- | --- |
| prompt | none / basic / when / when-how / fewshot / strict |
| schema | min / full / example / negative / alias |
| return | raw / trim / summary / typed / error-rich / error-min |

阶段：

| stage | 做法 | 标签 |
| --- | --- | --- |
| single | 固定两维，扫第三维 | `round`、`axis` |
| pair | 胜出变体两两组合 | `round`、`axis` |
| full | 胜出组合全组合，每组 N≥5 | `round`、`axis` |

- `axis` 记本轮扫描的维度（prompt / schema / return / pair / full）。
- 温度固定低值（0 或接近 0）。

### 3.4 单次对话循环步骤

| 步 | 动作 | 记录 |
| --- | --- | --- |
| 1 | 按配置渲染 system / user / tools schema | 请求体 |
| 2 | 发模型请求，走统一内部模型（03 C1/C2）与 gateway（02 R1–R8） | 请求 / 响应 / token |
| 3 | 响应含 tool_call：校验参数 → tool / plugin 统一入口执行 → 按 return 变体渲染回填 | 工具调用 / 返回值 / 耗时 |
| 4 | 响应是最终答案：结束，进判定 | final_answer |
| 5 | 超时或超过 `max_rounds`：记 `not_converged`，仍进判定 | status |

### 3.5 判定规则与模型裁判

规则判定五类：

| 规则 | 判定 |
| --- | --- |
| `should_call` | 至少一次调用（true）/ 零调用（false） |
| `tool` | 调用名匹配 |
| `arg_contains` | 参数 JSON 包含指定子串 / 值 |
| `final_contains` | 最终答案包含全部关键事实 |
| `must_not_call` | 禁止调用某工具 |

judge 输出形状：

```json
{
  "judge": {
    "rule": {"pass": true, "reasons": []},
    "model": {"pass": true, "reasons": ["…"], "judge_model": "…", "temperature": 0}
  }
}
```

status 取值：

| status | 含义 |
| --- | --- |
| ok | 判定通过 |
| not_converged | 超轮次 / 超时 |
| gateway_error | 模型侧失败（连接 / 5xx / 429 耗尽等） |
| exec_error | 工具 / 插件执行失败 |

### 3.6 落盘目录树与 runs.jsonl

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

`runs.jsonl` 每行一条，字段：

| 字段 | 说明 |
| --- | --- |
| `run_id` / `case_id` / `repeat_idx` | 标识 |
| `round` / `axis` | 汇总分组 |
| `config` | prompt / schema / return / model / gateway_ver / judge_ver |
| `messages_masked` | 脱敏后的请求消息 |
| `tool_calls` | name / args_raw / schema_valid / exec_ok / ret_rendered / latency_ms |
| `final_answer` / `rounds_used` | 最终答案与轮数 |
| `tokens` | in / out |
| `total_ms` | 总耗时 |
| `judge` | rule 与 model 两个来源 |
| `status` | ok / not_converged / gateway_error / exec_error |

### 3.7 报告七项

1. 配置快照摘要（三维度、模型、用例数、重复数）。
2. 矩阵汇总表：行 = 组合，列 = 指标，按端到端成功率排序。
3. 单因素对比：每个维度内变体对比，标显著差异。
4. 交互效应说明：两两组合是否非加性。
5. 反例清单：失败最多的 10 个用例 + trace 链接。
6. 结论与建议：胜出配置、为什么、适用边界。
7. 成本：总 token、总耗时、单次平均成本。

### 3.8 回放语义

- `-replay <trace 目录>`：用录制的模型响应重放，不发真实模型请求。
- 回放产出同样目录与 runs.jsonl，每条标 `replay: true`。
- 用途：验证判定器、渲染逻辑改动；失败可复现。

## 4. 边角情况

- **超时 / 超轮**：记 `not_converged`，仍进判定，单独归类，不与 ok 混计。
- **gateway_error 与 exec_error 分开**：模型侧失败与工具执行失败分开统计（复用 03 C9 错误归一），不混进模型决策错误。
- **脱敏**：`runs.jsonl` 与 trace 写入前对 `api_key/token/secret/authorization` 字段脱敏（G6）。
- **禁止跨轮比较**：模型、网关、用例集、判定器任一版本变化，历史数据只做趋势参考，不做胜出判定。
- **工具超时不重试**：工具 / 插件执行超时视为失败样本，不重试（重试污染稳定性指标）。
- **重试落盘**：对 provider 的限流退避重试落盘并标 `retry_of`，不悄悄吞。

## 5. 由哪些测试守着

计划中的测试（旧 `app/internal/eval` 测试只作线索）：

- `TestEvalDryRun`：dry-run 只打印矩阵与用例数，不发请求。
- `TestMatrixSingleAxis`：single 阶段固定两维扫第三维，round / axis 标签正确。
- `TestEvalLoopConverges`：请求 → 工具执行 → 回填 → 最终答案全链路，runs.jsonl / trace / report / cases 快照齐全。
- `TestJudgeRules`：五条规则各有一个通过用例与一个反例。
- `TestEvalReplay`：replay 不发真实请求，判定与录制一致。
- `TestFaultInjection`：empty / error / timeout / slow 各产出对应 status。
- `TestRedactRuns`：runs.jsonl 与 trace 无 api_key / token / secret 明文。

## 6. 对应代码

待建。旧实现参考：`app/internal/eval`（cases / config / engine / host / judge / matrix / recorder / replay / report / result / runner / testdata）与 `cmd/gqy-eval`，仅事实线索，不照抄。
