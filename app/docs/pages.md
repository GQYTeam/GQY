# 页面与组件地图

| 区域 | 标记 | 样式 | 逻辑 |
| --- | --- | --- | --- |
| 侧栏 | `aside.sidebar` | `styles/shell.css` | `renderSidebar()`、搜索、工作区 |
| 顶栏 | `header.topbar` | `styles/shell.css` | `renderThread()` 设置标题 |
| 对话 | `#body` / `#thread` | `styles/chat.css` | `renderThread()`、`messageHTML()` |
| 输入框 | `#composerForm` | `styles/chat.css` | `handleSend()`、菜单、附件、用量 |
| 看板 | `#boardView` | `pages.css` + `board.css` | `renderBoard()`、`setView('board')` |
| 设置 | `#settingsLayer` | `settings.css` | `openSettings()`、`refreshUsage()` |
| 文件面板 | `#filesPanel` | `panels.css` | `openFilesPanel()`、树/代码/拖宽 |
| 代码灯箱 | `#lightbox` | `panels.css` | `openViewer()` |
| 图片灯箱 | `#imgbox` | `media.css` | `openImgbox()` |
| 消息菜单/推理滑块 | `.menu` / `.effort` | `ui.css` | `openMenu()`、`mountEffort()` |
| 回到最新 | `#scrollBtn` | `ui.css` | `updateScrollBtn()` |
| 无障碍播报 | `#liveStatus` | — | `announce()` |

## 对话消息的形状

```js
{
  id, role: 'user' | 'assistant', ts,
  text,                 // Markdown 正文
  thinking, thinkMs,    // 思考块
  tools: [{ name, detail, icon, args, output, ms, status }],
  artifact: { title, path, lang, content, lines },
  question: { header, text, multiSelect, options:[{label,desc}], answer:{picks,custom} },
  attachments: [{ name, type, size, url }],
  error: { code, message },     // 服务端流式失败时出现，渲染错误卡片 + 重试
  usage: { prompt_tokens, completion_tokens }  // 真实后端返回时记录
}
```

`messageHTML(m, live)` 按顺序渲染：思考 → 工具轨迹 → Markdown → 错误卡片 → 产物 → 提问 → 操作行。
`liveMsg()` 在流式期间只暴露已生成的部分，并把 `stage` 传给渲染器（光标位置、工具 running 态）。

## 新增页面的检查清单

1. `index.html`：`<section class="view" id="fooView" hidden aria-labelledby="fooTitle">`，内部放 `.view-head` + `.view-scroll`。
2. `styles/foo.css`：在 `index.html` 里放在 `responsive.css` 之前；窄屏规则写进 `responsive.css`。
3. `js/foo.js`：`(() => { const G = window.GQY; G.foo = { init(el) {...} }; })();`，在 `app.js` 之前引入。
4. `app.js`：`setView` 增加分支（隐藏 `.main-row`、显示 `#fooView`、设置顶栏标题），boot 里 `G.foo.init(...)`。
5. 侧栏加入口（`.side-action`），选中态 `.is-active`。
6. `npm run check` + 浏览器冒烟：进入/返回/键盘/Esc。
