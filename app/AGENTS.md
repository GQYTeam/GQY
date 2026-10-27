# AGENTS.md

给在仓库里工作的 AI / 人类协作者的说明。先读这一页，再动代码。

## 这是什么

顾清影：AI 对话工作台。前端零构建、零依赖；后端是 Go module（标准库，零第三方依赖），按 `cmd` / `internal` 分包，负责静态托管、模型目录（models.dev）、SSE 转发、会话落盘和用量聚合。
模型列表由服务端向 models.dev 拉取并缓存，前端只消费精简结果；聊天请求经 SSE 转发到 OpenAI 兼容网关。

关键约束：

- **不新增第三方依赖**。前端不加 npm 包，后端不加第三方 Go 模块；图表、高亮、精灵图、SSE 解析、冒烟测试全部手写。`go.mod` 只声明 module 与 Go 版本，无 require。
- 后端必须能 `go build ./cmd/gqy-server` 直接编译，且只依赖标准库。
- 视觉基准：暖纸底 + 赤陶强调 + 少即是多；颜色/字号优先用 `styles/base.css` 的令牌。
- 所有用户/模型内容进 DOM 前必须 `esc()`；Markdown 链接必须过 `GQY.safeUrl()`，只放行 http(s)/mailto/相对路径。
- 密钥只允许出现在服务端环境变量；前端不得读取、不得写入 localStorage。

## 命令

```bash
npm start       # http://127.0.0.1:5173 ，含 /api/*（go run ./cmd/gqy-server）
npm run build   # go build -o bin/gqy-server ./cmd/gqy-server
npm run check   # node --check 所有 JS + go vet ./...
npm run smoke   # 编译后端 + 无头 Chrome 端到端
npm run files   # 列出项目文件
```

## 目录与加载顺序

```
index.html          应用外壳 + 各页面 <section class="view">
app.js              控制器：状态、视图、流式输出、事件接线
go.mod              module 声明（gqy）
cmd/gqy-server/     服务端入口：装配 config/httpapi，处理信号与优雅关闭
internal/
  config/           环境变量解析（.env / 密钥只进服务端）
  catalog/          models.dev 拉取、缓存、过滤、元数据（context/模态/成本）
  provider/         SSE 转发：chat / responses 两种协议 → 内部事件
  session/          会话落盘（AboutProjectFile.md 目录规范）+ 用量聚合
  httpapi/          路由、中间件、JSON、静态白名单
js/core.js          工具与常量（esc / uid / 时间 / 格式化 / token 估算）
js/markdown.js      Markdown、代码高亮、链接卡片、safeUrl
js/clawd.js         Clawd 精灵播放 + 活动状态机
js/api.js           后端探测、模型目录、用量、SSE 聊天、会话镜像
scripts/smoke.mjs   端到端冒烟（go build + CDP，无依赖）
styles/             base → shell → chat → ui → pages → panels → media → settings → responsive
assets/             头像、字标、favicon、封面、Clawd 精灵图
docs/               架构、页面、数据、测试、路线图
```

JS 全是经典脚本，顺序固定：`core → markdown → clawd → api → app`。
共享能力挂在 `window.GQY`，模块之间不互相 import；`app.js` 顶部一次性解构。
CSS 按 `index.html` 的顺序级联，新样式放在 `responsive.css` 之前。

## 改动指南

- **接入/调整模型**：只改 `internal/provider`（chat / responses 两种协议）与 `js/api.js` 的接口形状；渲染层（`messageHTML` / `liveMsg` / `paintStream`）不要动。
- **模型目录**：`internal/catalog` 负责拉取 models.dev 并缓存（TTL 6h）、过滤专用类型、补全 context / 模态 / 成本等元数据；前端 `openModelMenu` 消费 `/api/models` 做可搜索选择器。
- **用量**：`internal/session` 聚合落盘会话里的真实 token 用量与消息量，`GET /api/usage` 暴露；前端 `refreshUsage` 消费。
- **新增接口**：`internal/httpapi` 里注册 `METHOD /api/xxx` 路由，返回 JSON 或 SSE；静态文件只允许 `mimeTypes` 表里的扩展名，并记得给不支持的方法留 405。
- **改 Go 代码后**：`gofmt -w ./cmd ./internal && go vet ./...`，再跑 `npm run smoke`。
- **协议差异**：两种上游都只往前端吐 `thinking` / `text` / `done`；新增协议时保持这个内部事件名不变，前端就不用改。

- **新增页面**：`<section class="view" id="fooView" hidden>` + `styles/foo.css` + `js/foo.js`（`GQY.foo = { init }`）+ `setView('foo')` + 侧栏入口。详见 `docs/pages.md`。
- **新增消息组件**：数据结构挂在消息对象上（仿 `artifact` / `question` / `tools`），渲染放 `js/markdown.js` 或 `app.js` 的 `messageHTML`，流式阶段由 `liveMsg()` 决定是否展示。
- **新增设置项**：`state` 加字段 → `loadState` 校验 → `seedState` 默认值 → 设置窗口加行 → 事件里 `save()`。见 `docs/data-model.md`。
- **新增面板**：参照 `styles/panels.css` 与 `app.js` 的 files panel；浮层用 `hidden` + `inert`，Esc 关闭顺序在 `app.js` 的全局 keydown 里。

## 质量门槛（提交前）

1. `npm run check` 全绿。
2. `npm run smoke` 全绿（覆盖 Go 编译、模型目录、用量、流式、落盘、静态白名单、405、设置、快捷键、导入、XSS）。
3. 浏览器控制台无报错；`js/api.js` 探测失败时优雅降级（不报错、不阻塞）。
4. 键盘可用：Tab 能到所有控件，Esc 能关浮层（含搜索框），焦点不丢。
5. 减少动效模式（设置 → 外观）下无闪烁、无无限动画；系统 `prefers-reduced-motion` 首次访问即生效。
6. 390 / 600 / 1100 三个宽度不溢出；文件面板在窄屏改为覆盖式。

## 已知边界（改动前先读）

- `app.js` 仍是最大的控制器；拆分需要先把 `state`/`el` 收进 store（见 `docs/roadmap.md`）。
- 会话统计由服务端聚合落盘数据；localStorage 占用为前端本机实时值。
- 多文件/目录能力未实现；产物仍是单个 artifact 文件。
- 附件没有上传通道：带附件的消息不含文件内容，只在界面上说明。
- 服务端没有鉴权与限流，只适合本机使用；公网部署前必须先加反向代理鉴权。
- Go 服务端定位前端目录：优先当前目录，其次可执行文件所在目录（`findStaticRoot`）。生产部署把二进制和 `index.html` 放一起即可脱离源码树运行。
- 只有当前会话会镜像到服务端；刷新后以 localStorage 为准（服务端不做回灌）。

## 仓库与产品文档

仓库根的 `docs/` 是产品级约定，改 UI 前先对照：

- `docs/AboutUiPages.md`：页面清单（灯箱、设置、看板、用量、初始化引导、关键词浮窗）。
- `docs/AboutProjectFile.md`：Artifact / Runtime Files / Workspace / Session 的边界，`$GQY_HOME/sessions/$SESSION_ID/{tmp,logs,io}` 的目录规范。演示站不写盘，只落地到 `localStorage`；接真实后端时按这份规范落文件。
- `docs/AboutAiOtput.md`：输出风格红线。

### 文案风格（来自 AboutAiOtput）

- 不要套路句式（「不是 A，而是 B」）、讨好腔、阿谀奉承、空洞大词、老师腔。
- 只写用户需要知道的：结论 → 依据 → 下一步；不堆概念、不解释人尽皆知的事。
- 不确定就问，不要编；前端改动只跑前端相关检查，不跑全量。
- 不把临时脚本散落在项目里；需要时统一放 `$GQY_HOME` 或系统的 `/tmp`，用完删。

## 文档

- `docs/architecture.md`：模块、状态流、流式管线
- `docs/pages.md`：页面与组件地图
- `docs/data-model.md`：state / message / localStorage
- `docs/testing.md`：手动与 headless 验证
- `docs/roadmap.md`：未来计划与优先级
