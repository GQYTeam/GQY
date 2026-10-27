# 测试与验收

## 必跑

```bash
npm run check      # node --check 所有 JS + go vet
go test ./...      # 网关 / 插件 / 评测 / 工具沙箱
go test -race ./internal/eval/... ./internal/plugin/...
npm run smoke      # 39 项端到端
```

启动本地服务后（`npm run dev`）手动走一遍：

1. 首屏：空会话 + 输入框；首次引导可跳过；模型选择器能打开并搜索 models.dev 目录。
2. 发送消息：Enter 发送、Shift+Enter 换行；停止按钮可中断；停止后内容保留。
3. 提问卡片：点选项 / 键盘 1–3 / 「其他…」自定义；已回答态禁用，回答会继续发起请求。
4. 产物：点击打开右侧文件面板；对话变窄；树可展开、键盘导航、点文件看到代码；拖宽、关闭恢复。
5. 代码灯箱：面板「全屏」打开；预览 / 源码切换；Esc 关闭；复制可用。
6. 看板：侧栏「看板」打开；加卡、左右移动、删除、关联会话；Esc 返回对话。
7. 术语：正文 `RAG` / `token` 等点击出现浮窗；代码块内不装饰；Esc 关闭。
8. 设置：10 个 tab 全部可切换；搜索框能过滤 tab；人设/记忆/知识库/工具/软件包/用量/账户都渲染真实数据；导入/导出/清空可用。
9. 用量与扩展：顶栏 ⚡ 打开用量浮层；侧栏「扩展」可开可关；GitHub 未配置时连接按钮给出提示；插件可加载/卸载。
10. 响应式：390 / 600 / 1100 无横向滚动；面板与看板在窄屏堆叠。
11. 减少动效：无打字光标、无循环动画。

## 工具与 agent loop

需要真实模型或假上游；`GQY_MOCK=1` 只验证 UI 流式，不执行工具。

1. `read` / `list_dir`：让模型读一个文件、列一个目录，确认工具轨迹与结果正确。
2. `edit` / `shell`：在临时目录写文件、跑 `echo`；确认落盘与输出截断（>16 KB）符合预期。
3. `ask_user`：提一个需要选择的问题，确认出现提问卡片；点选项后新一轮请求带回答案。
4. 外部工具：默认不出现 `provider_usage`；设 `GQY_ENABLE_EXTERNAL=1` 后模型可见并可调用。
5. 余额提醒：配置真实 `GQY_API_KEY`，设一个高于余额的 `GQY_LOW_BALANCE`，确认结果带“尽快收尾”提示。
6. 错误路径：让工具参数非法（如 `read` 缺 `path`），确认错误回填后模型仍能继续给出答案。
7. 轮次上限：用假上游持续返回工具调用，确认 20 轮后返回错误而不是死循环。
8. 路径沙箱：让 `read` 读 `/etc/passwd`、`GQY_HOME/github_token.json`，确认被拒绝；设 `GQY_TOOL_ROOTS` 后放行追加根。
9. shell 白名单：设 `GQY_SHELL_ALLOWLIST=echo`，确认 `echo ok` 可执行、`echo x; rm` 被拒绝。
10. 插件：复制 `plugins/echo` 到 `$GQY_HOME/plugins/echo`，扩展页加载后 `/api/tools` 出现 `echo__echo`，调用返回 `hello`，卸载后消失。

## 网关

1. 两个假上游 A/B，A 返回 500、B 返回 200：`/api/chat` 最终来自 B，`gateway/requests.jsonl` 记录两次 attempt，A 进入冷却。
2. `/v1/models` 列出 `provider/model` 与 `group/<id>`；`/v1/chat/completions` 非流式与 `stream:true` 都能被 OpenAI 客户端解析。
3. 路由组策略：`order` 按配置顺序，`rotate` 每轮换，`usage` 优先调用少的成员。
4. 会话亲和：同一 `session` 第二次请求优先落到上次成功的 provider。
5. 并发：`max_concurrency` 填满后，超出请求按 `queue_timeout_ms` 返回排队超时。

## 评测

```bash
go run ./cmd/gqy-eval -config evals/echo/config.json -dry-run
go run ./cmd/gqy-eval -config evals/echo/config.json -out ../docs/test_call_echo
go run ./cmd/gqy-eval -config ... -replay /path/to/trace
```

- dry-run 只打印矩阵，不请求模型、不落盘。
- 真实运行需要 provider 和 `$GQY_HOME/plugins/echo`；宿主按 `tool` 前缀只加载对应插件。
- 报告、`runs.jsonl`、trace、`best/current.json` 都在 `-out` 目录。
- 单测：`go test ./internal/eval/...`；契约/宿主测试在 `internal/eval/host`。

## 自动冒烟

```bash
npm run smoke
```

`scripts/smoke.mjs` 先 `go build` 出后端二进制，再用 `GQY_MOCK=1` + 临时 `GQY_HOME` 起服务端，配合无头 Chrome（CDP，无依赖），跑端到端：

| 覆盖 | 断言 |
| --- | --- |
| 后端 | `go build` 通过；`/api/health` 返回 `provider: mock` |
| 模型目录 | `/api/models` 返回非空聊天模型列表且已过滤专用类型 |
| 用量接口 | `/api/usage` 返回聚合结构 |
| 静态边界 | `/server.go`、`/package.json`、`/scripts/*`、`/.env` 全部 404；错误方法 405 |
| 首屏 | 空会话渲染、服务端状态探测为“已连接” |
| 流式 | 模拟 SSE 链路：正文、思考块、工具轨迹、发送键复位、无错误态 |
| 落盘 | `$GQY_HOME/sessions/<id>/` 有 `manifest.json`、`messages.jsonl`、`logs/tools/` |
| 安全 | `javascript:` 链接被降级成纯文本 |
| 设置 | 10 个 tab 全部可切换；通用页有导入/导出/清空；搜索过滤生效 |
| 浮层 | 用量浮层可开可关；扩展浮层可打开 |
| 快捷键 | ⌘/Ctrl+K 打开搜索、Esc 关闭、Alt+N 新会话 |
| 草稿 | 切换会话后草稿恢复 |
| 导入 | 导出结构可原样回灌并重载状态 |
| 会话管理 | 重命名、置顶、单会话导出、批量删除 |
| 产物预览 | Markdown / HTML / SVG、iframe 沙箱 + CSP、2 MB 降级、宽度 390/600/1100 |
| 引导 | 首次弹层可跳过并写入 `onboarded` |
| 看板 | 打开、加卡、Esc 返回 |
| 术语 | 正文装饰、代码块跳过 |
| 设置 CRUD | 人设新增、记忆新增、自定义术语新增 |
| 工具页 | 列出工具、切换外部注入 |
| 软件包 | 发现示例插件、加载、卸载 |
| 用量 / 账户 | 渲染真实数据 |
| 供应商 | 新增 provider、新增路由组、删除两者 |
| 控制台 | 全程无 error |
| 真实上游 | 假 OpenAI 兼容上游：第一次返回 `list_dir` 工具调用，第二次返回最终答案；断言两次上游请求、`tool` 事件、正文含模型名、`done.usage.total_tokens === 12` |

需要本机有 Go >= 1.22 和 Chrome / Chromium；缺任意一个时脚本会打印“跳过”并以 0 退出。设置 `CHROME=/path/to/chrome` 可指定浏览器路径。

## 手动回归（改动 UI 时）

1. 首屏：空会话 + 输入框，模型选择器能搜索 models.dev 目录。
2. 后端三态：`npm start`（无密钥）、`GQY_MOCK=1 npm start`、填好 `.env` 后 `npm start`，模型按钮与关于页文案随之变化。
3. 断网/错误：把 `GQY_API_BASE` 指向不存在的地址，确认错误卡片出现且“重试”可用。
4. 停止生成：流式中点停止，内容保留、发送键复位。
5. `file://`：双击 `index.html`，确认不发起任何网络请求、状态显示“未连接服务端”。
6. 减少动效：系统开启“减弱动态效果”后首次打开即为安静模式。
7. 设置搜索：输入“人设”“记忆”“用量”，确认只留下匹配的 tab，清空后恢复。
8. 供应商：用 `gqy-import` 导入后打开「模型 & 供应商」，确认供应商数量与 id / 名称 / base_url 显示正确；没有 `providers.json` 时显示 0。
9. GitHub：未配置 `GITHUB_CLIENT_ID` 时连接按钮提示未配置；配置后走完 OAuth 回到首页，扩展浮层显示 `已连接 · @login`，断开后恢复未连接。
