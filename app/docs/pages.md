# 页面与组件地图

| 区域 | 标记 | 样式 | 逻辑 |
| --- | --- | --- | --- |
| 侧栏 | `aside.sidebar` | `styles/shell.css` | `renderSidebar()`、搜索、工作区、会话管理、扩展入口 |
| 顶栏 | `header.topbar` | `styles/shell.css` | `renderThread()` 标题、用量按钮、全屏 |
| 对话 | `#body` / `#thread` | `styles/chat.css` | `renderThread()`、`messageHTML()` |
| 输入框 | `#composerForm` | `styles/chat.css` | `handleSend()`、模型菜单、附件、语音、上下文用量 |
| 设置 | `#settingsLayer` | `styles/settings.css` | `openSettings()`、10 个 tab、`filterSettings()` |
| 用量浮层 | `#usageLayer` | `styles/settings.css` | `openUsage()`、`refreshUsage()` |
| 扩展浮层 | `#extensionsLayer` | `styles/settings.css` | `openExtensions()`、GitHub 连接/断开 |
| 文件面板 | `#filesPanel` | `styles/panels.css` | `openFilesPanel()`、树/代码/拖宽 |
| 代码灯箱 | `#lightbox` | `styles/panels.css` | `openViewer()`、预览/源码切换、`openArtifact()` |
| 图片灯箱 | `#imgbox` | `styles/media.css` | `openImgbox()` |
| 消息菜单/推理滑块 | `.menu` / `.effort` | `styles/ui.css` | `openMenu()`、`mountEffort()` |
| 上下文用量浮层 | `.usage-pop` | `styles/ui.css` | `openContextPanel()` |
| 回到最新 | `#scrollBtn` | `styles/ui.css` | `updateScrollBtn()` |
| 无障碍播报 | `#liveStatus` | — | `announce()` |

服务归属：`store.js`（状态/DOM）、`render.js`（消息/侧栏/线程/上下文浮层）、`chat.js`（发送/流式/会话）、`files.js`（文件/灯箱/提问卡片）、`menu.js`（菜单/模型/附件/语音）、`settings.js`（设置/用量/导入导出/GitHub）。

## 会话管理（侧栏）

- 入口：会话行右侧「更多」按钮或右键菜单；双击标题直接行内重命名。
- 重命名：Enter 保存、Esc 取消、失焦保存；trim 非空、上限 80。
- 置顶：菜单「置顶 / 取消置顶」；置顶项稳定排在同工作区最前，`pinned` 持久化。
- 批量删除：侧栏「管理」进入多选模式，支持全选 / 清空 / 计数 / 二次确认；流式中的会话禁止删除；当前会话被删时切到剩余首个或新建空会话。
- 导出：会话菜单导出 JSON 或 Markdown，文件名会过滤路径分隔符与保留字符。

## 产物预览（灯箱）

- 可预览类型：Markdown、HTML、SVG；其他代码只显示源码。
- 灯箱顶部「预览 / 源码」分段按钮，`aria-pressed` + 左右方向键切换；打开新文件重置状态。
- HTML 用 `iframe sandbox="allow-scripts"`（绝不带 `allow-same-origin`）+ `srcdoc`，并注入 CSP；SVG 用无脚本 sandbox；Markdown 复用 `GQY.renderMarkdown` 与 `safeUrl`。
- 超过 2 MB、空内容、二进制/非 UTF-8 自动降级为源码并在灯箱条显示原因；预览失败可随时切回源码。
- 关闭灯箱会清空 `srcdoc` / Markdown DOM，避免残留执行环境。

## 设置 tab

`settings.js` 的 `openSettingsTab()` 白名单与 `index.html` 的 `data-settings-tab` 必须一致。

| tab | 内容 | 状态 |
| --- | --- | --- |
| `general` | 字号、减少动效、侧栏、思考展开、导入/导出、清空、关于 | 可用 |
| `models` | 当前模型 / 推理强度；供应商池 | 只读；打开 tab 时从 `/api/providers` 拉取并列出 id / 名称 / base_url，编辑待接入 |
| `personas` | 人设与预设 | 占位 |
| `memory` | 记忆查看/搜索/删除 | 占位 |
| `knowledge` | 知识库文档与索引 | 占位 |
| `voice` | 语音输入开关；声音/唤醒词 | 开关未接线，麦克风在输入框菜单 |
| `packages` | 软件包与预设启用情况 | 占位 |
| `tools` | 核心工具 / 外部工具静态清单；加载模式与预算 | 只读 |
| `usage` | 用量详情入口 | 占位，实时用量在顶栏 ⚡ 浮层 |
| `accounts` | 成员、分组、权限 | 占位（单用户） |

设置搜索 `#settingsSearch` 按 tab 标题 + 面板文本做子串过滤，只隐藏不匹配的 tab；清空恢复全部。

## 对话消息的形状

```js
{
  id, role: 'user' | 'assistant', ts,
  text,                 // Markdown 正文
  thinking, thinkMs,    // 思考块
  tools: [{ name, detail, icon, args, output, ms, status }],
  toolsTotal,           // 流式期间用于“还有 N 个工具”计数
  artifact: { title, path, lang, content, lines },
  question: {
    header, text, multiSelect,
    options: [{ label, desc }],
    answer: { picks: number[], custom: string }
  },
  attachments: [{ name, type, size, url }],
  error: { code, message },     // 服务端流式失败时出现，渲染错误卡片 + 重试
  usage: { prompt_tokens, completion_tokens, total_tokens }  // 真实后端返回时记录
}
```

`messageHTML(m, live)` 按顺序渲染：思考 → 工具轨迹 → Markdown → 错误卡片 → 产物 → 提问 → 操作行（复制 / 重新生成 / token / 时间）。
`liveMsg()` 在流式期间只暴露已生成的部分，并把 `stage` 传给渲染器（光标位置、工具 running 态）。

`ask_user` 生成的卡片复用同一结构：`header` 固定为「需要确认」，`options` 来自服务端返回的字符串数组；回答后追加一条 user 消息并重新请求。

## 新增页面 / 浮层

当前只有 `chat` 与 `settings` 两个 `setView` 分支；其余都是浮层或兄弟面板。

1. `index.html` 加结构。浮层用 `hidden` + `role="dialog"` + `aria-modal="true"` + `tabindex="-1"`。
2. `styles/*.css` 写样式，放在 `responsive.css` 之前；窄屏规则进 `responsive.css`。
3. 逻辑挂到对应 service（`chat/files/menu/settings`），用 `Object.assign(app, {...})` 暴露给 `app.js`。
4. `app.js`：绑定打开/关闭按钮；`syncInert()` 纳入层叠；Esc 关闭顺序插到正确位置；Tab 焦点陷阱用 `trapTab()`。
5. 同步更新 `scripts/smoke.mjs` 的可见性断言，跑 `npm run check` + `npm run smoke`。
