# 架构

## 总览

```
index.html
  ├─ styles/*.css                设计令牌与各区域样式（固定加载顺序）
  ├─ js/core.js                  纯工具，无状态
  ├─ js/markdown.js              纯函数：文本 → HTML（含 URL 协议白名单）
  ├─ js/replies.js               纯函数：prompt → { thinking, tools, text, artifact }
  ├─ js/samples.js               常量：演示项目文件
  ├─ js/clawd.js                 自包含：精灵播放 + 状态机
  ├─ js/api.js                   后端适配：探测 / SSE 聊天 / 会话镜像
  └─ app.js                      控制器（唯一持有状态的地方）

server.go                        服务端（Go 标准库）：静态托管 + /api/chat + /api/sessions
```

`app.js` 是一个 IIFE，内部持有 `state`、`el`（DOM 引用表）、`stream`（当前流式任务）等。
外部模块只通过 `window.GQY` 暴露纯函数/常量；`app.js` 顶部解构使用。

## 状态与持久化

- `state` 由 `loadState()` 从 `localStorage['claude-webchat:v6']` 读取，失败则 `seedState()`。
- 结构：`workspaces[] → convs[] → messages[]`，外加 `activeWorkspaceId / activeConvId / model / effort / fontSize / streamSpeed / thinkOpen / sidebarCollapsed / reduceMotion`。
- 任何写操作后调用 `save()`；`resetToSeed` / `clearEverything` 会保留用户偏好。
- 视图状态（`view`、`stream`、`filesState`、`menu` 等）只在内存中。

## 视图路由

`setView('chat' | 'board' | 'settings')`：

- `chat`：显示 `.main-row`（对话 + 文件面板），刷新标题；
- `board`：隐藏 `.main-row`，显示 `#boardView`，调用 `renderBoard()`；
- `settings`：打开浮层窗口（`#settingsLayer`），对话仍在后面。

文件面板是 `.main-row` 的兄弟列，展开时通过 `flex-basis` 挤压对话区；窄屏改为覆盖。

## 流式管线

两条来源，同一个渲染状态机：

```
handleSend()
  → 推入 user 消息 → respond(conv, prompt)
  ├─ 有后端（GQY.api.available() 且无附件）
  │    startApiStream() → POST /api/chat（SSE）
  │    event: thinking | tool | text | done | error
  │    事件累加进同一个 stream 对象 → scheduleStreamPaint()
  └─ 无后端
       generateReply() → startStream()：stage = thinking | tool | text
       runStream() 递归 setTimeout 推进
  → finishStream()：写回消息、持久化、镜像会话、更新用量、Clawd 状态
```

- 重绘用 `scheduleStreamPaint()` 节流到约 18fps：打字观感不变，避免每 16ms 重解析整段 Markdown。
- `stopStreaming()` 会 `AbortController.abort()` 并落库已生成内容；中断的 API 请求不会写“已停止生成”以外的内容。
- 失败时消息挂 `error: { code, message }`，`messageHTML` 渲染错误卡片 + 重试（`retryMessage` 复用同一管线）。
- 速度受 `state.streamSpeed` 缩放（仅本地演示）；减少动效 + 本地演示直接 `completeStream()`。
- 演示回放 `replayDemo()` 复用同一管线，`stream.demo = true`。

## 后端

`server.go` 只用 Go 标准库（单文件，无 `go.mod`、无第三方模块）：

- **静态托管**：扩展名白名单 + `path.Clean` 规范化，`server.go`、`package.json`、`go.mod`、`scripts/`、`bin/`、点文件不可访问；`http.ServeContent` 处理 ETag / If-None-Match / Range。
- **`POST /api/chat`**：对内只吐 `thinking` / `text` / `tool` / `done` / `error` 五类事件，对外支持两种上游协议：
  - `wire=chat`（默认）：`/chat/completions`，`delta.content` → `text`、`delta.reasoning_content` → `thinking`（DeepSeek 等）
  - `wire=responses`：`/responses`，`response.output_text.delta` → `text`、`response.reasoning_summary_text.delta` → `thinking`（GPT-5 系列、部分中转）
  - `wire=auto`：先走 chat，上游返回「请改用 /v1/responses」时自动重试 responses
  两者都有 15s 心跳、120s 无输出超时、上游错误按状态码给中文提示；SSE 写入串行化，`http.Flusher` 每帧 flush；`GQY_API_UA` 可设置上游 UA（部分中转按 UA 拦截客户端）。
- **会话落盘**：`PUT /api/sessions/{id}` 按 `docs/AboutProjectFile.md` 写 `manifest.json` / `context.md` / `messages.jsonl` / `io/output/artifacts/*` / `logs/tools/YYYY-MM-DD.jsonl`；目录 0700、文件 0600，写入走临时文件 + rename，`created_at` 沿用首次写入。
- **边界**：默认只绑定 `127.0.0.1`，无鉴权、无限流；请求体上限 256 KB（chat）/ 8 MB（会话）；错误方法返回 405 并带 `Allow`。
- **配置**：启动时读同目录 `.env`（真实环境变量优先），无 `.env` 也能跑；缺密钥时 `provider: none`，前端回落本地演示。

## 后端探测与降级

`js/api.js` 在 http(s) 下 `GET /api/health`（1.5s 超时）；`file://` 直接禁用。
`provider: 'none'` 或探测失败 → `available() === false` → 前端走本地演示，界面上标注“本地演示”。
连上后模型按钮显示服务端的 `GQY_MODEL`，不再把演示模型名发给上游。

## 文件面板

- `buildFiles()` 从当前会话收集产物（按标题去重），补上 `FILES_MOCK`。
- `buildTree()` 生成静态目录树 + 文件节点；`filesState.open` 记录展开状态。
- 树是 `role="tree"`，键盘 ↑↓←→/Home/End/Enter 导航；选中文件打开代码视图（面包屑 + 行号 + 高亮）。
- 左缘 5px 分隔条可拖动调宽，宽度持久化到 `localStorage['gqy:files-width']`。

## 设计约束

- 无构建：所有脚本必须能按顺序在 file:// 下执行。
- 无依赖：高亮、图表（SVG）、精灵播放、SSE 解析、冒烟测试都是手写。
- 可访问性：浮层用 `inert` + 焦点回收；菜单/树有 aria；流式完成/失败通过 `#liveStatus` 播报；减少动效全局生效，并跟随系统 `prefers-reduced-motion`。
- 数据安全：`save()` 遇到配额错误会提示，不再静默丢弃；导入只接受导出结构，导入前二次确认。
