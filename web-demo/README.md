# 顾清影 · AI 对话演示

纯前端、无构建、无第三方依赖。直接双击 `index.html` 即可运行，也可以用命令起一个本地服务。

## 文档

- `AGENTS.md`：给协作者（AI / 人）的规则、改动指南与质量门槛
- `docs/architecture.md`：模块、状态流、流式管线
- `docs/pages.md`：页面与组件地图、消息数据结构、新增页面清单
- `docs/data-model.md`：state / message / localStorage
- `docs/testing.md`：验收清单与 headless 冒烟方法
- `docs/roadmap.md`：后续计划与优先级

## 命令

```bash
npm run dev     # 本地预览 http://localhost:5173（python3 -m http.server）
npm start       # 同 dev
npm run check   # 语法检查全部 JS
npm run files   # 列出项目文件
```

## 目录

```
index.html              唯一入口：应用外壳 + 各页面 <section class="view">
app.js                  应用控制器：状态、路由（视图切换）、事件接线、流式输出
js/
  core.js               共享工具与常量（esc / uid / 时间与数字格式化 / token 估算）
  markdown.js           Markdown 渲染、代码高亮、链接卡片
  replies.js            本地回复生成（演示文案）、产物提取
  samples.js            演示数据（项目文件样例）
  clawd.js              Clawd 吉祥物：精灵图逐帧播放 + 活动状态机
styles/
  base.css              设计令牌、reset、基础元素
  shell.css             应用外壳：侧栏 / 顶栏 / 主区域
  chat.css              输入框、消息、Markdown、思考与工具调用、代码块
  ui.css                菜单、推理滑块、弹层、动画
  pages.css             页面视图（看板 / 设置入口）、产物卡片、提问卡片
  panels.css            代码灯箱、上下文用量、Clawd、项目文件面板
  media.css             附件、拖拽层、图片灯箱
  settings.css          设置窗口
  board.css             看板补充样式
  responsive.css        响应式（最后加载）
assets/                 头像、字标、favicon、示例封面、Clawd 精灵图
AGENTS.md               协作规则与改动指南
docs/                   架构、页面、数据、测试、路线图
```

## 约定

**加载顺序**
- CSS 按 `index.html` 中的顺序级联；新的页面样式放在 `responsive.css` 之前。
- JS 全部是经典脚本（非 module），顺序固定：`core → markdown → replies → samples → clawd → app`。
- 共享能力挂在 `window.GQY` 上，`app.js` 顶部统一解构，模块之间不直接引用彼此的内部函数。

**页面 / 视图**
- 每个页面是 `index.html` 里的一个 `<section class="view" id="xxxView">`，由 `app.js` 的 `setView('xxx')` 切换（目前有 `chat` / `board` / `settings`）。
- 页面样式写在 `styles/<page>.css`，页面脚本可以独立成 `js/<page>.js`（IIFE + `window.GQY` 暴露入口），并在 `app.js` 的启动段调用初始化。
- 侧栏入口统一使用 `.side-action`，选中态加 `.is-active`。

**新增一个页面**
1. 在 `index.html` 的 `<main>` 里加 `<section class="view" id="fooView" hidden>`。
2. 新建 `styles/foo.css`，在 `index.html` 的 `responsive.css` 之前引入。
3. 新建 `js/foo.js`（IIFE，导出 `GQY.foo = { init, ... }`），在 `app.js` 之前引入，并在 `app.js` 启动段 `GQY.foo.init(...)`。
4. 在 `styles/shell.css` 的侧栏加导航按钮，并在 `app.js` 的 `setView()` 里加上 `foo` 分支（隐藏其它视图、设置顶栏标题）。
5. `npm run check` 后刷新验证。

**新增一个聊天内组件（例如一种新的消息卡片）**
- 渲染：`js/markdown.js`（纯函数，输入文本/数据，输出 HTML，必须 `esc`）。
- 样式：`styles/chat.css`。
- 交互：`app.js` 的 `el.thread` 事件委托（用 `data-xxx` 标记）。
- 数据结构：写在消息对象上（`artifact` / `question` / `tools` 等都是先例），流式期间由 `liveMsg()` 决定是否显示。

## 数据

- 全部状态保存在 `localStorage`（键：`claude-webchat:v6`），无网络请求。
- 回复、工具调用、思考、看板数据都是本地模拟，仅用于演示。
