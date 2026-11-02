# 006 · 核心工具与 agent loop

日期：2026-11-02
状态：完成（2026-11-02）

## 目标

把 agent loop 与核心工具接起来：agent 组装请求 → 消费 002 的 provider 事件流 → 流式累积回复 → 有工具调用就执行工具并回填 → 再请求，直到 `end_turn` 或 `max_rounds`。核心工具（read / edit / shell / list_dir / websearch / ask_user）按 06 T4–T7 的边界与上限实现；注册表按 T3；工具轨迹按 T9 进事件流；路径沙箱按 11 SEC2–SEC4 落地。

## 范围

**做：**

- 工具契约：名称 / 描述 / 参数 JSON Schema / 执行 / 来源标记；一次调用六要素记录（06 T1）。
- 工具注册表：重名报错、注销幂等、核心 / 外部两份列表（06 T3）。
- 核心工具六件：read / edit / shell / list_dir / websearch / ask_user（06 T4 / T5 / T6 / T7 的边界与上限）。
- 沙盒策略：允许根 / 拒绝路径 / 符号链接解析 / 写目标符号链接拒绝、shell 白名单与元字符拒绝（11 SEC2–SEC4）。
- agent loop：组装请求 → 消费 provider 事件流（thinking / text / tool_call / usage / done）→ 流式累积 → 工具调用 → 回填 → 再请求（01 agent 层、03 C2）。
- ask_user 暂停 loop：`question` 事件、数字键 / 自定义答案回填（06 T7、05 S2）。
- 工具轨迹进事件流与日志（06 T9、05 S5）。

**不做（后续施工单）：**

- 插件运行时与外部工具注入（007；04 P11、06 T8）。
- 评测（008）。
- 知识库 / 记忆。
- `write_artifact` 产物工具（归 005 产物域，06 T4）。
- 工具级配额 / 限流、流式工具输出（06 已后置）。

## 关键决定

- **工具契约六要素照 T1**：`name` / `schema` / `arguments` / `result` / `duration_ms` / `status`；`status` 四取值 `ok` / `error` / `timeout` / `canceled`；模型只看到 `result` 或 `error`。
- **核心自动注入、外部默认关（T2）**：本单只实现并注入核心列表；外部列表留空，注入开关逻辑给 007。
- **agent loop 每轮工具调用是 provider 事件流的 consumer**：把 provider 的 `tool_call` 事件累积成一次调用，执行后把结果写成 tool 消息回填，重新组装请求；不解析供应商协议（01 agent 层、03 C2）。
- **loop 结束条件**：provider 事件流 `done` 且 `stop_reason=end_turn`，或工具轮数达到 `max_rounds`；`tool_use` 且轮数未满则继续。
- **ask_user 数字键回填**：`options` 渲染成 1..n，用户选数字键或输入自定义答案；答案回填进下一轮上下文，不重新走工具调用（T7、S2 `question`）。
- **沙盒照 SEC2–SEC4 落地，不自行发明**：最小权限（read / list_dir 只读允许根、edit 只写允许根、shell 只有 workspace）；路径解析「绝对化 → Clean → EvalSymlinks → 先拒绝后允许」；写目标为符号链接时拒绝；shell 白名单按首 token 的 basename，白名单模式下拒绝 `; | & > <` 等元字符。

## 落点

```
app/internal/tool/{tool.go, registry.go, builtin.go, ask.go, sandbox.go}
app/internal/agent/{loop.go, tools.go}
app/internal/httpapi/chat.go   # 增量：/api/chat 挂上 agent loop
app/cmd/gqy-server/main.go     # 增量：装配 tool.Registry + agent.Loop
```

## 验收

```bash
make check
cd app && go test -race ./...
```

行为验收：

1. 工具契约：每个核心工具返回文本或错误；一次调用的六字段记录齐全，`status` 只取四取值；重名注册报错、重复注销幂等（T1 / T3）。
2. read：大文件超限报错、图片返回 data URI、`../` / 符号链接逃逸 / 拒绝路径命中三种越界被拒（T5、SEC3）。
3. edit：原子写不产生半截文件、保留原权限、自动建父目录；目标为符号链接时拒绝（T5、SEC3）。
4. shell：工作目录固定 workspace；超时杀整个进程组无孤儿；输出截断带标记；白名单拒绝元字符与未列入命令（T6、SEC4）。
5. list_dir：目录项以 `/` 结尾、越界被拒；websearch 后端未配置时返回明确错误不编造（T4）。
6. ask_user：暂停 loop，`question` 事件带 `options`；数字键与自定义答案都能回填后继续（T7、S2）。
7. agent loop：`tool_use` 时执行工具并回填再请求，直到 `end_turn`；`max_rounds` 到顶强制结束且标注原因。
8. 工具轨迹：一次调用对应一个 `tool` 事件与一条 jsonl 日志；敏感参数脱敏（T9、05 S5）。

## 留底

完成（2026-11-02）。`make check` 与 `go test -race ./...` 全部通过；tool 包 8 个测试、agent 包 5 个测试。

实际落点与偏差：

- 落点文件全部建成：`internal/tool/{tool,registry,builtin,ask,sandbox}.go`、`internal/agent/{loop,tools}.go`；`internal/httpapi/chat.go` 增量（`/api/chat` 挂 agent loop，五事件投影）、`cmd/gqy-server/main.go` 增量（装配 tool.Registry + agent.Loop）；另有必要接线 `internal/httpapi/server.go`（Server 加 `agentLoop` 字段与 `SetAgent`）。
- 行为验收 1–8 由 `tool_test.go`、`agent/loop_test.go` 覆盖；路径沙箱按 SEC2–SEC4 落地（绝对化→Clean→EvalSymlinks→先拒绝后允许、写目标符号链接拒绝、shell 白名单+元字符拒绝）。
- 偏差：`ask_user` 的 HTTP 级暂停/续跑实现为「question 事件后本轮结束，下一请求带 `answer` 字段回填续跑」；agent 层数字键/自定义答案回填已测。`websearch` 本单只实现「未配置报错」，真实后端调用留给后续；工具轨迹 jsonl 落盘接口已预留（Sink.Tool → 会话 tool 事件），日志文件写入随 007 接插件时一并启用。
