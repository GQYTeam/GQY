# 工具与 agent loop

## 现状

服务端已实现最小工具系统：统一 `Tool` 接口 + 线程安全注册表 + agent loop。工具全部进程内执行，来源只有内置（核心）和 `provider_usage`（外部）。
`docs/AboutPlugin.md` 里的适配器、注册中心、子进程/远程运行时、契约测试尚未实现，详见文末「与插件设计的关系」。

## 契约

`internal/tool/tool.go`：

```go
type Tool interface {
    Name() string
    Description() string
    Schema() map[string]any                 // JSON Schema 的 parameters 部分
    Execute(ctx context.Context, args map[string]any) (string, error)
    Kind() Kind                             // KindCore / KindExternal
}
```

- `Description` 与 `Schema` 会原样进入请求的 `tools[].function`，同时核心工具名会列进系统提示。
- `Execute` 返回的字符串回填给模型；错误会被包成 `错误: ...` 回填，不中断 loop（`ask_user` 除外）。
- `Registry.Register` 重名返回错误，不覆盖；表级 `sync.RWMutex`，加载/调用并发安全。
- 注册位置：`internal/httpapi.New()`。

## 核心工具

默认注入。系统提示会列出这些工具，避免模型声称自己“没有工具”。

| 工具 | Schema | 行为 | 限制 |
| --- | --- | --- | --- |
| `read` | `path` | 读文件；图片（png/jpg/jpeg/gif/webp）返回 base64 data URI | 仅允许根内；单文件 ≤ 10 MB |
| `edit` | `path`, `content` | 覆盖写入，自动建父目录；同目录临时文件 + rename 原子替换 | 仅允许根内；无 diff、无追加模式 |
| `shell` | `command` | 在 workspace 以 `sh -c` 执行，合并 stdout+stderr | 默认 60s 超时并杀进程组；输出 ≤ 16 KB；可选白名单 |
| `list_dir` | `path`（默认 `.`） | 列目录，目录名以 `/` 结尾 | 仅允许根内；不递归 |
| `websearch` | `query` | 占位：直接返回“尚未接入搜索后端” | 未实现 |
| `ask_user` | `question`, `options?` | 中断 loop，向前端发提问卡片 | 不受单工具超时约束 |

## 权限、超时与配置

文件类工具（`read` / `edit` / `list_dir`）统一走 `internal/tool/sandbox.go` 的 `Policy`：

- 相对路径按 `GQY_WORKSPACE`（默认启动目录）解析，绝对路径也会做同样校验。
- 默认允许根 = workspace + `$GQY_HOME/sessions`；`GQY_TOOL_ROOTS`（冒号或逗号分隔）可追加。
- 已存在前缀做 `EvalSymlinks`，新建文件解析最近的已存在祖先；`..`、绝对越界、符号链接逃逸都会被拒绝。
- `GQY_HOME/github_token.json` 即使落在允许根内也被 `Policy.Denied` 显式拒绝。
- `edit` 用同目录临时文件 + `rename`，保留既有文件权限，不跟随最终符号链接写到根外。

`shell` 额外受：

- `GQY_SHELL_TIMEOUT`（默认 `60s`）：超时后 SIGKILL 整个进程组；`GQY_TOOL_MAX_OUTPUT`（默认 16384）限制合并输出。
- `GQY_SHELL_ALLOWLIST` 非空时，按首 token basename 匹配；并拒绝 `; | & > < $ ( )`、反引号与换行等控制字符，避免 `echo x; rm ...` 绕过。白名单模式只接受单条简单命令。
- 空 allowlist 表示不限制命令（兼容默认行为），此时 shell 仍能访问任意路径，不能当作沙箱。

单工具超时由 `agent.Loop.ToolTimeout`（`GQY_TOOL_TIMEOUT`，默认 `120s`）控制，每次工具调用单独计时；超时错误回填后 loop 继续。

## 外部工具

默认不注入；`GQY_ENABLE_EXTERNAL=1` 时随核心工具一起下发。用于“依赖具体供应商/社区、不该算核心能力”的工具。

`provider_usage`：

- 查 OpenAI 兼容供应商余额，默认走 DeepSeek 的 `GET {API_BASE 去掉 /v1}/user/balance`。
- 复用全局 `http.Client`；未注入时用 10s 超时。
- 未配置 API key 直接返回错误。
- 返回可用状态、币种、总余额、赠送/充值余额。
- `GQY_LOW_BALANCE > 0` 且总余额低于阈值时，在结果末尾追加“尽快收尾并汇报额度”。

## agent loop

`internal/agent/agent.go`，最多 20 轮：

```
messages（客户端历史）
  → 无 system 时插入系统提示（工具清单 + 使用规则）
  → Complete(带 tools schema)
  → 无 tool_calls：返回最终答案
  → 有 tool_calls：逐个 Execute，OnTool 回调发 SSE tool 事件，
     结果以 role=tool + tool_call_id 回填
  → 回到 Complete
```

- usage 每轮累加，最终随 `done` 事件返回。
- 参数不是合法 JSON 时用空 map 执行，让工具自己报错。
- 未知工具返回“未知工具”，同样回填后继续。
- 每次工具调用受 `ToolTimeout` 限制；工具不响应 context 也会按上限返回（后台 goroutine 自行结束）。
- `ask_user` 返回 `ErrAskUser`：立即结束本轮，HTTP 层发 `question` + `end`。

## SSE 映射

`POST /api/chat` 对前端只暴露这些事件：

| 事件 | 来源 | 前端处理 |
| --- | --- | --- |
| `thinking` | 上游 reasoning 增量 | 思考块 |
| `tool` | 每次工具执行后 | 工具轨迹；按 `name|detail` 合并 |
| `text` | 最终答案增量 | Markdown 正文 |
| `question` | `ask_user` | 提问卡片 |
| `done` | loop 结束 | 记录 usage |
| `error` | 上游 / loop 失败 | 错误卡片 + 重试 |
| `end` | 流结束 | 关闭流 |

`tool` 载荷：`{ tool: { name, detail, status, ms, output } }`。`detail` 是原始参数 JSON，`status` 为 `done` / `error`。

## ask_user 流程

1. 模型调用 `ask_user`（问题 + 可选选项）。
2. loop 捕获 `ErrAskUser`，不再请求模型。
3. HTTP 层发 `question`，前端渲染单选卡片；用户可点选项、按数字键，或选「其他…」输入自定义答案。
4. 前端把答案作为一条新的 user 消息写入会话，并重新 `POST /api/chat`。

当前是无状态续跑：提问前的工具调用和模型上下文不会随回答回传，新一轮请求只看得到可见文本历史。

## 新增工具

1. 在 `internal/tool/` 实现 `Tool` 五个方法。
2. 在 `internal/httpapi.New()` 里 `registry.Register(...)`。
3. 决定类别：默认给模型用 → `KindCore`；需要显式开启 → `KindExternal`。
4. 参数在 `Execute` 里自行校验，返回给模型的文本要能让模型继续推理。
5. 补测试：`scripts/smoke.mjs` 的 agent loop 用假上游返回工具调用；新工具至少手动跑一次真实调用。

## 与插件设计的关系

| `docs/AboutPlugin.md` | 当前实现 |
| --- | --- |
| 统一调用入口 / 统一模型 | `Tool` 接口 + `Registry` |
| 注册中心、适配器 | 无；只有进程内注册 |
| 生命周期状态机、卸载、进程组回收 | 无；工具是长驻进程内对象 |
| 并发模型声明、远程传输、熔断 | 无；表级读写锁 + 请求 context |
| 契约测试套件 | 无；只有冒烟里的单工具链路 |
| 重复注册报错 | 已对齐 |
| 社区专有类型不出适配器 | 不适用（尚无社区接入） |

## 已知缺口

- 没有交互式审批：`shell` 仍以服务端进程权限执行；非白名单模式可访问任意路径。需要强约束时设置 `GQY_SHELL_ALLOWLIST`。
- 硬链接可以指向允许根外的文件内容；路径沙箱不覆盖硬链接。`Resolve` 与 `open` 之间仍有理论 TOCTOU（本机单用户场景风险低）。
- 不响应 context 且永不返回的外部工具在超时后会泄漏一个 goroutine。
- 工具执行是同步阻塞的，执行期间没有进度事件；结束后才发 `tool`。
- 客户端只保存工具轨迹用于展示，`apiMessages()` 只发送文本，模型在下一轮看不到上一轮的工具调用记录（除非服务端回灌）。
- `websearch` 未实现；`GQY_MOCK=1` 不经过 agent loop，工具不会执行。
- `ask_user` 不是授权闸门，回答不会回传工具上下文。
