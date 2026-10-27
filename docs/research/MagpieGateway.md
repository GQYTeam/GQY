# Magpie 网关调研

调研对象：[`yetone/magpie`](https://github.com/yetone/magpie)（MIT，23k+ star）。
定位：本机 AI 网关，把多个 agent 的请求统一到一个端点，再按 provider / 账号 / 路由组转发；同时管理插件、用量、会话和隐私。

本文只记录对顾清影有参考价值的部分，不复制其实现。

## 它解决的问题

- 每个 agent 各有一套模型配置、密钥、base URL；magpie 把它们收拢到一个本地端点。
- 一个请求按 `provider/model` 或路由组选候选，失败时在**响应开始前**换下一个候选。
- 同一会话尽量落在同一个账号上，复用上游 prompt cache。
- 账号/密钥有并发上限、队列、429 冷却和配额窗口。
- 插件可以在网关的多个扩展点介入：路由策略、请求/响应中间件、工具、预算、页面。
- 请求、路由决策、用量、会话都留可观测记录。

## 关键设计

### 1. 候选与失败转移

- 一个请求先解析模型：普通 `provider/model`、路由组、别名或层级映射。
- `plan` 生成候选列表：同一 provider 的每个 key/account，然后是 fallback 模型；处于冷却的候选排到最后。
- 只在**还没有向客户端写出任何响应**时切换候选；一旦开始流式输出，错误直接返回给客户端。
- 路由组可以嵌套；组内按策略排序。

参考：`docs/subsystems/gateway-routing.md`。

### 2. 路由策略

magpie 提供 `smart`、`order`、`rotate`、`usage`、`pace`、`weight`、`free`、`fast` 等模式：

- `order`：按配置顺序，失败再下一个。
- `rotate`：每轮换一个成员。
- `usage`：优先用得最少的成员（带半衰期）。
- `pace`：按距离配额重置的时间摊平使用。
- `smart`：在还有额度的订阅里，选最早重置的。

顾清影当前不需要订阅配额，只保留 `order` / `rotate` / `usage` 三种，外加 fallback。

### 3. 健康、冷却与并发

- 每个 key/account 有最大并发；超出的请求进有界队列。
- 429 且仍有额度时进入“休息”，排到所有未限流候选后面；可手动解除。
- 连接错误、超时、5xx 视为可重试；4xx（除 429）不重试。
- 插件卡住有超时，不能拖垮整个网关。

### 4. 会话亲和

- 同一会话固定在回答过它的 key/account 上，避免 prompt cache 失效。
- 亲和模式：`auto` / `session` / `turn` / `off` / `strict`；状态落 `affinity.json`。
- 会话身份优先取客户端原生 header，其次才是首条消息摘要。

顾清影当前是单机单用户，先做“同一会话固定 provider”，状态放内存并在响应里带 `X-GQY-Provider`。

### 5. 可观测

- Routing 页记录每次决策：候选、冷却、哪个候选最终回答。
- Usage 记录每个请求的 provider、model、token、耗时、状态。
- 会话聚合把同一会话的请求归并。

顾清影先落 `$GQY_HOME/gateway/requests.jsonl`，并提供 `/api/gateway/status`。

### 6. 插件运行时

- 插件在 `init` 里调用 registry 注册 manifest；重复 id 直接 panic。
- manifest 声明宿主 API 版本区间、`Requires` / `Provides` 服务、是否 essential、设置 schema。
- 运行时 `Reconcile(on, config)`：先按依赖逆序关闭不再需要的插件，再按依赖顺序启动；`Apply` 拿到 `OnDispose` / `Provide` / `Service` / `Config`。
- 关闭器逆序执行，单个 panic 不影响其余。
- 插件工具通过 MCP 暴露：名字做白名单、参数转 JSON Schema、调用有 55s 超时。

参考：`docs/subsystems/plugin-runtime.md`、`plugin-tools.md`。

### 7. 中间件与请求钩子

- 网关中间件可以改写请求、观察响应、按超时放弃。
- 异步钩子有明确的请求/完成契约，慢钩子不能阻塞主链路。

顾清影先不做通用中间件，只在网关层留 `Middleware` 接口，后续接脱敏、预算、日志。

### 8. 协议与端点

- 同时提供 OpenAI Chat、OpenAI Responses、Anthropic Messages、Gemini 四套协议，内部翻译。
- `/v1/models` 列出 provider、模型和路由组。

顾清影当前上游本身是 OpenAI 兼容的，先提供 `/v1/chat/completions`（含流式的兼容实现）和 `/v1/models`；Anthropic/Gemini 翻译后置。

### 9. 隐私

- 请求离开本机前替换密钥、密码、邮箱等，回复里再还原；值只在内存。
- 不能脱敏时直接 503，不降级发送。

顾清影目前不传输对话到第三方以外的服务；工具日志已脱敏。完整 masking 后置。

## 顾清影采用 / 不采用

| 能力 | 现在采用 | 说明 |
| --- | --- | --- |
| 多 provider / 模型目录 | 是 | `providers.json` + 环境变量兜底 |
| 路由组与 fallback | 是 | `routing.json`，支持 order / rotate / usage |
| 失败转移 | 是 | 响应开始前，最多 N 次 |
| 健康与冷却 | 是 | 429 / 5xx / 连接错误进入冷却 |
| 并发限制 | 是 | 每 provider 信号量 + 有界等待 |
| 会话亲和 | 是 | 同会话固定 provider，内存态 |
| 请求日志 / 状态接口 | 是 | `gateway/requests.jsonl`、`/api/gateway/status` |
| 外部 OpenAI 兼容端点 | 是 | `/v1/chat/completions`、`/v1/models` |
| 插件运行时 / 插件工具 | 是（最小闭环） | 原生 stdio + MCP stdio |
| 插件服务依赖 / 热重配 | 否 | 后续 |
| 账号订阅、OAuth、配额窗口 | 否 | 后续 |
| Anthropic / Gemini 协议翻译 | 否 | 后续 |
| 隐私 masking、远程 magpie | 否 | 后续 |
| 图片 / embedding / 视频端点 | 否 | 后续 |

## 来源

- <https://github.com/yetone/magpie>
- <https://github.com/yetone/magpie/blob/main/docs/subsystems/gateway-routing.md>
- <https://github.com/yetone/magpie/blob/main/docs/subsystems/gateway-middleware.md>
- <https://github.com/yetone/magpie/blob/main/docs/subsystems/plugin-runtime.md>
- <https://github.com/yetone/magpie/blob/main/docs/subsystems/plugin-tools.md>
- <https://github.com/yetone/magpie/blob/main/docs/subsystems/providers-accounts.md>
