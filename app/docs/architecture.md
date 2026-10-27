# 架构

## 总览

```
index.html
  ├─ styles/*.css                设计令牌与各区域样式（固定加载顺序）
  ├─ js/core.js                  纯工具，无状态
  ├─ js/markdown.js              纯函数：文本 → HTML（含 URL 协议白名单）
  ├─ js/clawd.js                 自包含：精灵播放 + 状态机
  ├─ js/api.js                   后端适配：探测 / 模型 / 用量 / SSE / 镜像 / GitHub
  ├─ js/store.js                 共享 store：state、el、常量、save
  ├─ js/render.js                渲染层：消息、侧栏、线程、上下文浮层
  ├─ js/chat.js                  对话 service：发送、流式、会话、镜像
  ├─ js/files.js                 文件/灯箱/提问卡片 service
  ├─ js/menu.js                  菜单/模型/附件/语音 service
  ├─ js/settings.js              设置/用量/数据/扩展 service
  └─ app.js                      事件接线 + boot

cmd/gqy-server/                  服务端入口
internal/                        config / catalog / provider / agent / tool / session / github / httpapi
```

JS 都是经典脚本 IIFE，按固定顺序加载。`store.js` 建立 `window.GQY.app`，后续 service 把函数挂到 `app` 上，`app.js` 只做 DOM 事件绑定和启动。模块之间不互相 import。

## 状态与持久化

- `app.state` 由 `store.js` 的 `loadState()` 从 `localStorage['claude-webchat:v7']` 读取，失败则 `seedState()`。
- 结构：`workspaces[] → convs[] → messages[]`，外加 `activeWorkspaceId / activeConvId / model / effort / fontSize / thinkOpen / sidebarCollapsed / reduceMotion / drafts`。
- 任何写操作后调用 `app.save()`：写 localStorage → `refreshUsage()` → `mirrorSession()`（服务端可用时）。
- 视图状态（`view`、`stream`、`filesState`、`menu`、`imgboxItems`、`usageData` 等）只在内存中，挂在 `app` 上。
- `clearEverything` / `importData` 走 `resetState()` / `reloadState()` 重新建立 `app.state`。

## 视图路由

`setView('chat' | 'settings')`：

- `chat`：显示 `.main-row`（对话 + 文件面板），刷新线程；
- `settings`：打开浮层 `#settingsLayer`，对话仍在后面。

设置、用量、扩展、代码灯箱、图片灯箱、文件面板都是浮层/兄弟列，不切换顶层 view。层叠顺序与 Esc 关闭顺序写在 `app.js` 全局 keydown 里：图片灯箱 → 代码灯箱 → 扩展 → 用量 → 设置 → 上下文浮层 → 文件面板 → 菜单 → 搜索 → 移动端抽屉。

文件面板是 `.main-row` 的兄弟列，展开时通过 `flex-basis` 挤压对话区；窄屏改为覆盖。

## 流式管线

```
handleSend()
  → 推入 user 消息 → respond(conv, prompt)
  → startApiStream() → POST /api/chat（SSE）
  → 服务端 agent loop：模型 → 工具 → 回填 → 模型……
  → event: thinking | tool | text | question | done | error | end
  → 累加进 app.stream → scheduleStreamPaint()
  → finishStream()：写回消息、持久化、镜像、更新用量、Clawd 状态
```

- `scheduleStreamPaint()` 节流到约 18fps：打字观感不变，避免每帧重解析整段 Markdown。
- `tool` 事件按 `name|detail` 去重合并，流式期间 `status` 为 `running`，`finishStream` 统一改成 `done` 并补时长。
- `question` 事件先存进 `app.stream.question`；`finishStream` 生成 `msg.question`，复用提问卡片渲染。
- `stopStreaming()` 会 `AbortController.abort()` 并落库已生成内容；中断的请求不额外写内容。
- 失败时消息挂 `error: { code, message }`，`messageHTML` 渲染错误卡片 + 重试（`retryMessage` 复用同一管线）。
- `answerQuestion()` 把选择写进 `msg.question.answer`，再追加一条 user 消息并调用 `respond()` 发起新请求。

## 后端

Go module（`gqy`，标准库，零第三方依赖），按 `cmd` / `internal` 分层：

- `cmd/gqy-server`：入口，读 `.env`、定位前端目录、装配 `httpapi`、信号处理 + 优雅关闭。
- `internal/config`：环境变量解析，密钥只进服务端。
- `internal/catalog`：models.dev 拉取、缓存（6h）、过滤专用类型、元数据。
- `internal/provider`：SSE 转发，chat / responses 两种协议 → 内部事件。
- `internal/agent`：agent loop，工具调用、回填、usage 累加、20 轮上限。
- `internal/tool`：`Tool` 契约、线程安全注册表、核心工具、外部工具。
- `internal/session`：会话落盘 + 用量聚合。
- `internal/github`：GitHub OAuth 只读账户，token 落服务端。
- `internal/httpapi`：路由、中间件、JSON、静态白名单。

### agent loop

`agent.Loop.Run()`：

1. 若首条消息不是 system，在最前面插入系统提示（列出当前可用工具和调用规则）。
2. 请求上游（带 `tools` schema），累加 `usage`。
3. 没有工具调用 → 返回最终答案。
4. 有工具调用 → 逐个执行，`OnTool` 回调把结果转成 SSE `tool` 事件；结果以 `role: "tool"` 回填。
5. `ask_user` 返回 `ErrAskUser` → 立即返回问题，由 HTTP 层发 `question` 并结束本轮。
6. 超过 20 轮 → 返回错误。

工具参数不是合法 JSON 时用空 map 继续，让工具自己报错；未知工具返回“未知工具”。工具错误回填为 `错误: ...`，不中断 loop（`ask_user` 除外）。

工具注入：默认 `ListCore()`；`GQY_ENABLE_EXTERNAL=1` 时 `List()`（核心 + 外部）。`GQY_MOCK=1` 时 `/api/chat` 直接走 mock 流，不经过 loop。

### 接口

- **静态托管**：扩展名白名单 + `path.Clean` 规范化，`*.go`、`package.json`、`go.mod`、`scripts/`、`bin/`、`cmd/`、`internal/`、`tmp/`、点文件不可访问；`http.ServeContent` 处理 ETag / If-None-Match / Range。
- **`GET /api/health`**：`{ ok, provider, model, home, version }`。
- **`GET /api/models`**：`catalog.Catalog` 向 models.dev 拉取 `api.json`，过滤 embedding / speech / 图像 / 视频等专用类型，返回精简条目（含 `context` / `inputModes` / `outputModes` / `cost` / `reasoning` / `toolCall` / `attachment`）；内存缓存 6 小时，刷新失败用旧缓存兜底。
- **`GET /api/usage`**：`session.Store.Stats()` 聚合落盘会话里的 token、会话 / 消息 / 产物数、近 7 天消息量。
- **`GET /api/providers`**：读 `$GQY_HOME/providers.json`，返回 `{ providers }`；文件缺失或解析失败时返回空数组。数据来自 `cmd/gqy-import`。
- **`POST /api/chat`**：请求 `{ messages, model, effort }`，返回 SSE `thinking` / `tool` / `text` / `question` / `done` / `error` / `end`。最终答案按 8 个字符一块发；`done.usage` 是 loop 累计值。上游支持两种协议：
  - `wire=chat`（默认）：`/chat/completions`，`delta.content` → `text`、`delta.reasoning_content` → `thinking`（DeepSeek 等）
  - `wire=responses`：`/responses`，`response.output_text.delta` → `text`、`response.reasoning_summary_text.delta` → `thinking`（GPT-5 系列、部分中转）
  - `wire=auto`：先走 chat，上游返回「请改用 /v1/responses」时自动重试 responses
  两者都有 15s 心跳、120s 无输出超时、上游错误按状态码给中文提示；SSE 写入串行化，`http.Flusher` 每帧 flush；`GQY_API_UA` 可设置上游 UA。
- **GitHub**：`GET /api/github/auth` 返回授权页；`GET /api/github/callback` 用 code 换 token、读账户、写 `$GQY_HOME/github_token.json`（0600），再重定向回前端；`GET /api/github/me` 只返回账户；`DELETE /api/github` 删 token。未配置 Client ID 时返回 503。
- **会话落盘**：`PUT /api/sessions/{id}` 按 `docs/AboutProjectFile.md` 写 `manifest.json` / `context.md` / `messages.jsonl` / `io/output/artifacts/*` / `logs/tools/YYYY-MM-DD.jsonl`；目录 0700、文件 0600，写入走临时文件 + rename。
- **边界**：默认只绑定 `127.0.0.1`，无鉴权、无限流；请求体上限 256 KB（chat）/ 8 MB（会话）；错误方法返回 405 并带 `Allow`。
- **配置**：启动时先读可执行文件旁 `.env`，再读当前目录 `.env`；真实环境变量优先（`LookupEnv` 区分“没设置”和“显式空值”）。

## 后端探测与降级

`js/api.js` 在 http(s) 下 `GET /api/health`（1.5s 超时）；`file://` 直接禁用。
`provider: 'none'` 或探测失败 → `available() === false` → 前端显示「未连接服务端」。
连上后模型选择器从 `/api/models` 拉取目录；未选择时用服务端的 `GQY_MODEL`。

## 文件面板

- `buildFiles()` 从当前会话收集产物（按标题去重）。
- `buildTree()` 生成目录树 + 文件节点；`filesState.open` 记录展开状态。
- 树是 `role="tree"`，键盘 ↑↓←→/Home/End/Enter 导航；选中文件打开代码视图（面包屑 + 行号 + 高亮）。
- 左缘 5px 分隔条可拖动调宽，宽度持久化到 `localStorage['gqy:files-width']`。

## 设计约束

- 无构建：所有脚本必须能按顺序在 file:// 下执行。
- 无依赖：高亮、图表（SVG）、精灵播放、SSE 解析、冒烟测试都是手写。
- 可访问性：浮层用 `inert` + 焦点回收；菜单/树有 aria；流式完成/失败通过 `#liveStatus` 播报；减少动效全局生效，并跟随系统 `prefers-reduced-motion`。
- 数据安全：`save()` 遇到配额错误会提示，不再静默丢弃；导入只接受导出结构，导入前二次确认。
- 工具安全：以服务端进程权限执行，无沙箱。定位是本机单用户，不对外暴露。
