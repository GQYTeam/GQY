# 架构

## 总览

```
index.html
  ├─ styles/*.css                设计令牌与各区域样式（固定加载顺序）
  ├─ js/core.js                  纯工具，无状态
  ├─ js/markdown.js              纯函数：文本 → HTML
  ├─ js/replies.js               纯函数：prompt → { thinking, tools, text, artifact }
  ├─ js/samples.js               常量：演示项目文件
  ├─ js/clawd.js                 自包含：精灵播放 + 状态机
  └─ app.js                      控制器（唯一持有状态的地方）
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

```
handleSend()
  → 推入 user 消息 → respond(conv, prompt)
  → generateReply() 返回 { thinking, tools, text, artifact, question }
  → startStream()：stage = thinking | tool | text
  → runStream() 递归 setTimeout，按阶段推进：
       thinking 逐字 → 逐个工具 running → done → 正文逐字
    每次状态变化 paintStream()（整体重绘该条消息）
  → finishStream()：写回消息、持久化、更新用量、Clawd 进入 happy
```

- 速度受 `state.streamSpeed` 缩放；减少动效直接 `completeStream()`。
- 停止/切换会话会先 `finishStream()`，把已输出内容落库。
- 演示回放 `replayDemo()` 复用同一管线，`stream.demo = true`。

## 文件面板

- `buildFiles()` 从当前会话收集产物（按标题去重），补上 `FILES_MOCK`。
- `buildTree()` 生成静态目录树 + 文件节点；`filesState.open` 记录展开状态。
- 树是 `role="tree"`，键盘 ↑↓←→/Home/End/Enter 导航；选中文件打开代码视图（面包屑 + 行号 + 高亮）。
- 左缘 5px 分隔条可拖动调宽，宽度持久化到 `localStorage['gqy:files-width']`。

## 设计约束

- 无构建：所有脚本必须能按顺序在 file:// 下执行。
- 无依赖：高亮、图表（SVG）、精灵播放、看板都是手写。
- 可访问性：浮层用 `inert` + 焦点回收；菜单/树有 aria；减少动效全局生效。
