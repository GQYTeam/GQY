# AGENTS.md

给在仓库里工作的 AI / 人类协作者的说明。先读这一页，再动代码。

## 这是什么

顾清影：一个纯前端的 AI 对话演示。没有后端、没有构建、没有第三方依赖。
对话、思考、工具调用、产物、文件面板、看板、设置都是本地模拟数据，状态存 `localStorage`。

关键约束：

- **必须保持双击 `index.html`（file://）可用**。引入 ES module / fetch / 打包器会破坏这一点。
- **不新增依赖**。图表、高亮、精灵图播放全部手写。
- 视觉基准：暖纸底 + 赤陶强调 + 少即是多；颜色/字号优先用 `styles/base.css` 的令牌。
- 所有用户/模型内容进 DOM 前必须 `esc()`；Markdown 只允许 http(s) 链接与受控图片。

## 命令

```bash
npm run dev     # http://localhost:5173
npm run check   # node --check 所有 JS（提交前必跑）
npm run files   # 列出项目文件
```

## 目录与加载顺序

```
index.html          应用外壳 + 各页面 <section class="view">
app.js              控制器：状态、视图、流式输出、事件接线
js/core.js          工具与常量（esc / uid / 时间 / 格式化 / token 估算）
js/markdown.js      Markdown、代码高亮、链接卡片
js/replies.js       本地回复生成、产物提取
js/samples.js       演示数据（项目文件样例）
js/clawd.js         Clawd 精灵播放 + 活动状态机
styles/             base → shell → chat → ui → pages → panels → media → settings → board → responsive
assets/             头像、字标、favicon、封面、Clawd 精灵图
docs/               架构、页面、数据、测试、路线图
```

JS 全是经典脚本，顺序固定：`core → markdown → replies → samples → clawd → app`。
共享能力挂在 `window.GQY`，模块之间不互相 import；`app.js` 顶部一次性解构。
CSS 按 `index.html` 的顺序级联，新样式放在 `responsive.css` 之前。

## 改动指南

- **新增页面**：`<section class="view" id="fooView" hidden>` + `styles/foo.css` + `js/foo.js`（`GQY.foo = { init }`）+ `setView('foo')` + 侧栏入口。详见 `docs/pages.md`。
- **新增消息组件**：数据结构挂在消息对象上（仿 `artifact` / `question` / `tools`），渲染放 `js/markdown.js` 或 `app.js` 的 `messageHTML`，流式阶段由 `liveMsg()` 决定是否展示。
- **新增设置项**：`state` 加字段 → `loadState` 校验 → `seedState` 默认值 → 设置窗口加行 → 事件里 `save()`。见 `docs/data-model.md`。
- **新增面板**：参照 `styles/panels.css` 与 `app.js` 的 files panel；浮层用 `hidden` + `inert`，Esc 关闭顺序在 `app.js` 的全局 keydown 里。

## 质量门槛（提交前）

1. `npm run check` 全绿。
2. 浏览器控制台无报错；刷新后 `file://` 下同样可用。
3. 键盘可用：Tab 能到所有控件，Esc 能关浮层，焦点不丢。
4. 减少动效模式（设置 → 外观）下无闪烁、无无限动画。
5. 390 / 600 / 1100 三个宽度不溢出；文件面板在窄屏改为覆盖式。

## 已知边界（改动前先读）

- `app.js` 仍是最大的控制器；拆分需要先把 `state`/`el` 收进 store（见 `docs/roadmap.md`）。
- 看板、天气、会话统计、token 用量均为模拟值，不要当成真实数据做逻辑判断。
- 文件面板的 `data/train/*` 是演示目录，没有真实文件内容。
- 单文件产物；多文件/目录能力未实现。

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
