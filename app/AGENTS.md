# AGENTS.md

给在仓库里工作的 AI / 人类协作者的说明。先读这一页，再动代码。

## 这是什么

顾清影：AI 对话工作台。前端零构建、零依赖；后端是 Go module（标准库，零第三方依赖），按 `cmd` / `internal` 分包，负责静态托管、模型目录（models.dev）、SSE 转发、agent loop（模型 ↔ 工具多轮调用）、会话落盘、用量聚合和 GitHub 只读账户 OAuth。
模型列表由服务端向 models.dev 拉取并缓存，前端只消费精简结果；聊天请求经 SSE 转发到 OpenAI 兼容网关，工具由服务端执行后回填。

关键约束：

- **不新增第三方依赖**。前端不加 npm 包，后端不加第三方 Go 模块；图表、高亮、精灵图、SSE 解析、冒烟测试全部手写。`go.mod` 只声明 module 与 Go 版本，无 require。
- 后端必须能 `go build ./cmd/gqy-server` 直接编译，且只依赖标准库。
- 视觉基准：暖纸底 + 赤陶强调 + 少即是多；颜色/字号优先用 `styles/base.css` 的令牌。
- 所有用户/模型内容进 DOM 前必须 `esc()`；Markdown 链接必须过 `GQY.safeUrl()`，只放行 http(s)/mailto/相对路径。
- 密钥只允许出现在服务端环境变量；前端不得读取、不得写入 localStorage。
- 工具只实现 `internal/tool.Tool` 契约，核心工具与外部工具靠 `Kind()` 区分；默认只注入核心工具。

## 命令

```bash
npm start        # http://127.0.0.1:5173 ，含 /api/*（go run ./cmd/gqy-server）
npm run dev:watch # air 监听 Go 变更自动重启（需自行安装 air）
npm run build    # go build -trimpath -ldflags "-s -w" -o bin/gqy-server ./cmd/gqy-server
npm run check    # node --check 所有 JS + go vet ./...
npm run smoke    # 编译后端 + 无头 Chrome 端到端
npm run files    # 列出项目文件
go test ./...    # 网关 / 插件 / 评测 / 工具沙箱单测
go run ./cmd/gqy-eval -config evals/echo/config.json -dry-run
go run ./cmd/gqy-import -src ... [-dry-run]  # 导入旧备份（需系统 sqlite3）
go run ./cmd/gqy-eval -config internal/eval/testdata/sample.json -dry-run  # 评测网关矩阵
```

## 目录与加载顺序

```
index.html          应用外壳 + 各页面 / 浮层
app.js              事件接线 + boot；不持有业务逻辑
go.mod              module 声明（gqy）
cmd/gqy-server/     服务端入口：装配 config/httpapi，处理信号与优雅关闭
cmd/gqy-import/     旧备份导入器：sqlite3 导出 → session.Store / $GQY_HOME
cmd/gqy-eval/       评测网关 CLI：矩阵执行、报告、trace 回放
internal/
  config/           环境变量解析（.env / 密钥 / workspace / 工具沙箱）
  catalog/          models.dev 拉取、缓存、过滤、元数据（context/模态/成本）
  provider/         SSE 转发：chat / responses 两种协议 → 内部事件
  gateway/          本地多 provider 网关：路由、失败转移、健康/冷却、会话亲和
  agent/            agent loop：工具调用、回填、usage 累加、轮次上限、单工具超时
  tool/             工具契约 / 注册表 / 路径沙箱 / 核心工具 read·edit·shell·list_dir·websearch·ask_user / 外部工具 provider_usage
  plugin/           插件运行时：原生 stdio JSON-RPC + MCP stdio、状态机、进程组回收
  eval/             评测网关：config/cases/matrix/runner/judge/recorder/report/replay
  session/          会话落盘（AboutProjectFile.md 目录规范）+ 用量聚合
  github/           GitHub OAuth 只读账户（token 只落服务端）
  httpapi/          路由、中间件、JSON、静态白名单、插件与工具接口
js/core.js          工具与常量（esc / uid / 时间 / 格式化 / token 估算）
js/markdown.js      Markdown、代码高亮、链接卡片、safeUrl
js/clawd.js         Clawd 精灵播放 + 活动状态机
js/api.js           后端探测、模型目录、用量、SSE 聊天、会话镜像、GitHub、插件、网关
js/store.js         共享 store：state、el、常量、save/镜像入口
js/render.js        消息 HTML、侧栏、线程、上下文用量浮层
js/chat.js          发送、流式管线、会话切换与镜像、人设/记忆/知识 system 上下文
js/files.js         文件面板、代码/图片灯箱、提问卡片、预览
js/menu.js          浮层菜单、推理滑块、模型选择、附件、语音输入
js/settings.js      设置窗口、用量、导入导出、扩展、GitHub、人设/记忆/知识库/软件包/工具
js/glossary.js      关键词浮窗（本地术语表）
js/board.js         看板
js/onboarding.js    首次引导
scripts/smoke.mjs   端到端冒烟（go build + CDP，无依赖）
plugins/echo/       示例插件（原生 stdio JSON-RPC）
evals/echo/         评测示例配置与用例
skills/gh.md         agent 的 gh 命令参考（通过 shell 工具使用）
plugins/            示例插件（echo：native stdio 协议）
styles/             base → shell → chat → ui → pages → panels → media → settings → features → responsive
assets/             头像、字标、favicon、封面、Clawd 精灵图
docs/               架构、页面、数据、工具、测试、路线图
```

JS 全是经典脚本，顺序固定：`core → markdown → clawd → api → store → render → chat → files → menu → settings → glossary → board → onboarding → app`。
共享能力挂在 `window.GQY`，跨模块状态与服务挂在 `window.GQY.app`（`js/store.js` 建立，其余 service 用 `Object.assign` 挂上去），模块之间不互相 import。
CSS 按 `index.html` 的顺序级联，新样式放在 `responsive.css` 之前。

## 改动指南

- **接入/调整模型**：只改 `internal/provider`（chat / responses 两种协议）与 `js/api.js` 的接口形状；渲染层（`render.js` 的 `messageHTML`、`chat.js` 的 `liveMsg` / `paintStream`）不要动。
- **模型目录**：`internal/catalog` 负责拉取 models.dev 并缓存（TTL 6h）、过滤专用类型、补全 context / 模态 / 成本等元数据；前端 `openModelMenu` 消费 `/api/models` 做可搜索选择器。
- **用量**：`internal/session` 聚合落盘会话里的真实 token 用量与消息量，`GET /api/usage` 暴露；前端 `refreshUsage` 消费。
- **新增工具**：实现 `tool.Tool`（`Name/Description/Schema/Execute/Kind`）→ 在 `internal/httpapi.New` 注册；重名 `Register` 会报错，不要静默覆盖。核心工具默认注入，外部工具只在 `GQY_ENABLE_EXTERNAL=1` 时注入。文件类工具必须复用 `tool.Policy.Resolve`，不要直接拼路径；shell 类工具必须带超时与输出上限。详见 `docs/tools.md`。
- **网关**：provider 来自 `$GQY_HOME/providers.json` + 环境变量兜底；路由组在 `$GQY_HOME/routing.json`。改解析/失败转移只动 `internal/gateway`，不要让 agent 感知 provider 细节。`/api/chat` 和 `/v1/*` 共用 `Router.Complete`。
- **插件**：manifest 放 `$GQY_HOME/plugins/<id>/`；协议只加适配器（`plugin.Adapter`），生命周期、超时、进程回收留在 `Manager`。插件动作注册成 `KindExternal` 工具，默认不注入。新增协议必须补 `internal/plugin` 单测。
- **评测**：配置 JSON 在 `evals/<tool>/`；包结构 `internal/eval/{config,cases,matrix,runner,judge,result,recorder,report,replay,engine,host}`。宿主按 `config.tool` 前缀加载真实插件；改判定器要新开一轮，不能和旧 `runs.jsonl` 混比。命令见 `docs/eval.md`。
- **新增设置页功能**：占位容器换成 `#xxxPanel` → `settings.js` 加 `renderXxxPanel()` → `openSettingsTab` 分发 → 表单用 `data-*` 事件委托。新字段同步 `store.js` 的 `seedState` / `loadState`。
- **前端新视图**：`index.html` 加 `<section class="view">`，`setView` 加分派，Esc 关闭顺序与 `syncInert` 同步；看板是第二个视图。
- **新增接口**：`internal/httpapi` 里注册 `METHOD /api/xxx` 路由，返回 JSON 或 SSE；静态文件只允许 `mimeTypes` 表里的扩展名，并记得给不支持的方法留 405。
- **新增 SSE 事件**：`provider.Event` 加字段，`/api/chat` 里 `out.Send`，前端 `chat.js` 的 `startApiStream` 加分支，`finishStream` 决定是否落进消息。
- **改 Go 代码后**：`gofmt -w ./cmd ./internal && go vet ./...`，再跑 `npm run smoke`。
- **协议差异**：两种上游都只往前端吐 `thinking` / `text` / `done`；新增协议时保持这个内部事件名不变，前端就不用改。
- **新增页面/浮层**：页面结构放 `index.html`，逻辑挂到对应 service（`settings.js` / `files.js` / `menu.js`），在 `app.js` 里接线；浮层用 `hidden` + `inert`，Esc 关闭顺序在 `app.js` 的全局 keydown 里。详见 `docs/pages.md`。
- **新增消息组件**：数据结构挂在消息对象上（仿 `artifact` / `question` / `tools`），渲染放 `render.js` 的 `messageHTML`，流式阶段由 `liveMsg()` 决定是否展示。
- **新增设置项**：`state` 加字段 → `store.js` 的 `loadState` 校验 → `seedState` 默认值 → 设置面板加行 → 事件里 `save()`。见 `docs/data-model.md`。
- **新增设置 tab**：`index.html` 加 `.settings-tab` + `.settings-panel`，`settings.js` 的 `openSettingsTab` 白名单同步，`scripts/smoke.mjs` 的 tab 数量断言也要改。

## 质量门槛（提交前）

1. `npm run check` 全绿；`go test ./...` 全绿。
2. `npm run smoke` 全绿（39 项：Go 编译、agent loop、网关、插件、模型目录、用量、流式、落盘、静态白名单、405、十个设置 tab、设置搜索、快捷键、导入、XSS、看板、引导、术语、人设/记忆/知识库、工具/插件页）。
3. 浏览器控制台无报错；`js/api.js` 探测失败时优雅降级（不报错、不阻塞）。
4. 键盘可用：Tab 能到所有控件，Esc 能关浮层（含搜索框），焦点不丢。
5. 减少动效模式（设置 → 外观）下无闪烁、无无限动画；系统 `prefers-reduced-motion` 首次访问即生效。
6. 390 / 600 / 1100 三个宽度不溢出；文件面板在窄屏改为覆盖式。

## 已知边界（改动前先读）

- 插件运行时已落地进程内注册表 + 原生 stdio JSON-RPC + MCP stdio（加载 / 调用 / 卸载 / 超时 / 进程组回收）；远程传输、跨社区契约测试矩阵、崩溃恢复仍待补。
- 网关支持 OpenAI Chat 兼容的 provider、路由组、失败转移、冷却、并发与亲和；Anthropic / Gemini 协议翻译、订阅账号与配额窗口未实现。
- 评测网关已实现三维矩阵、用例循环、规则判定、录制、报告与回放；模型裁判、跨轮比较未实现。
- `websearch` 是占位工具，调用直接返回“尚未接入搜索后端”。
- 工具沙箱默认根 = workspace + `$GQY_HOME/sessions`，`GQY_TOOL_ROOTS` 可追加；`GQY_HOME/github_token.json` 显式拒绝。`shell` 有超时、输出上限与可选白名单，但空白名单时仍可访问任意路径；没有交互式审批。`GQY_ENABLE_EXTERNAL` 只控制注入，不是权限开关。
- `ask_user` 的续跑是无状态的：服务端返回 `question` 后结束本轮，前端把回答作为新 user 消息再请求一次；工具调用历史不随回答回传。
- `GQY_MOCK=1` 走模拟流，不经过 agent loop，工具不会执行；测工具链路要用假上游或真实模型。
- 会话统计由服务端聚合落盘数据；localStorage 占用为前端本机实时值。
- 多文件/目录能力未实现；产物仍是单个 artifact 文件。
- 附件没有上传通道：带附件的消息不含文件内容，只在界面上说明。
- 服务端没有鉴权与限流，只适合本机使用；公网部署前必须先加反向代理鉴权。
- Go 服务端定位前端目录：优先当前目录，其次可执行文件所在目录（`findStaticRoot`）。生产部署把二进制和 `index.html` 放一起即可脱离源码树运行。
- 只有当前会话会镜像到服务端；刷新后以 localStorage 为准（服务端不做回灌）。
- GitHub 集成只做只读账户信息；仓库操作走 `skills/gh.md` + 本机 `gh` CLI，不通过 OAuth token 代调。
- 设置里 10 个 tab 全部可用：人设 / 记忆 / 知识库 / 工具 / 软件包 / 用量 / 账户都接真实数据；供应商编辑、语音唤醒词、模型市场价格仍未接。

## 仓库与产品文档

仓库根的 `docs/` 是产品级约定，改 UI 或工具前先对照：

- `docs/AboutUiPages.md`：页面清单（灯箱、设置、看板、用量、初始化引导、关键词浮窗）。
- `docs/AboutProjectFile.md`：Artifact / Runtime Files / Workspace / Session 的边界，`$GQY_HOME/sessions/$SESSION_ID/{tmp,logs,io}` 的目录规范。
- `docs/AboutAiOtput.md`：输出风格红线。
- `docs/AboutPlugin.md`：插件运行时 + 多社区适配器设计（四层、并发、远程、进程回收、契约测试）。当前落地统一 `Tool` 契约、进程内注册表、原生 stdio 与 MCP stdio 适配器。
- `docs/AboutAitestCallToolOrPlugin.md`：工具/插件调用评测网关设计（三维度实验矩阵、判定、落盘）。已落地 `internal/eval` + `cmd/gqy-eval`，用法见 `docs/eval.md`。
- `docs/research/MagpieGateway.md`、`docs/design/Gateway.md`：网关调研与设计。
- `docs/design/PluginRuntime.md`、`docs/design/EvalGateway.md`、`docs/design/FrontendCapabilities.md`：实现设计。
- `docs/CapabilityMatrix.md`：能力分类与状态。

### 文案风格（来自 AboutAiOtput）

- 不要套路句式（「不是 A，而是 B」）、讨好腔、阿谀奉承、空洞大词、老师腔。
- 只写用户需要知道的：结论 → 依据 → 下一步；不堆概念、不解释人尽皆知的事。
- 不确定就问，不要编；前端改动只跑前端相关检查，不跑全量。
- 不把临时脚本散落在项目里；需要时统一放 `$GQY_HOME` 或系统的 `/tmp`，用完删。

## 文档

- `docs/architecture.md`：模块、状态流、流式管线、后端
- `docs/pages.md`：页面与组件地图、设置 tab
- `docs/data-model.md`：state / message / localStorage / 镜像
- `docs/tools.md`：工具契约、核心/外部/插件工具、沙箱与超时、agent loop、ask_user
- `docs/eval.md`：评测网关配置、命令、落盘与回放
- `docs/testing.md`：手动与 headless 验证
- `docs/roadmap.md`：未来计划与优先级
