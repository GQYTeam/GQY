# 前端能力设计

依据：`docs/AboutUiPages.md`、现有 `js/store.js` 的 state，以及设置页 10 个 tab。

目标：把占位页补成可用功能，并新增引导、看板与关键词浮窗；所有新数据仍以 localStorage 为准，不新增后端依赖。

## 能力分类

### A. 引导

- 首次打开（`state.onboarded !== true`）显示全屏引导。
- 三步：工作区名称 → 模型（来自 `/api/models`，可为空）→ 动效偏好。
- 完成后写入 `state.workspaces[0].name`、`state.model`、`state.reduceMotion`、`state.onboarded = true`。
- 可跳过；跳过也写 `onboarded = true`，设置里可重新打开。

### B. 看板

- 新视图 `#boardView`，侧栏入口「看板」。
- 每张卡：`{id, title, note, status, convId?, createdAt}`；状态 `todo / doing / done`。
- 卡片可新增、编辑标题、删除、左右移动；点击关联会话时跳回对话。
- 数据存 `state.board.cards`；`setView('board')` 显示；Esc/返回回到对话。
- 窄屏三列改为纵向堆叠。

### C. 关键词浮窗

- 术语表 `state.glossary`：`[{term, aliases[], definition, source?}]`，内置 20 个 AI/工程词。
- Markdown 渲染后，对纯文本节点匹配术语（大小写不敏感、按长度倒序），包成 `<button class="term" data-term="...">`。
- 点击/回车显示浮层：术语、定义、来源；Esc 关闭。
- 设置「知识库」tab 管理自定义术语。
- 不联网、不调用模型。

### D. 人设

- `state.personas`：`[{id, name, system, enabled}]`；内置「默认」不可删。
- `state.activePersonaId`；设置页 CRUD，对话页顶部显示当前人设。
- 发送时把启用的人设 `system` 作为第一条 system 消息传给后端。

### E. 记忆

- `state.memories`：`[{id, text, enabled, createdAt}]`。
- 设置页增删改、启停；发送时把启用的记忆拼成 system 上下文（总长上限 4000 字）。
- 不自动抽取记忆，避免模型偷偷写入。

### F. 知识库

- `state.knowledge`：`[{id, title, content, enabled, updatedAt}]`。
- 设置页粘贴文本；发送时按输入关键词做简单匹配，取最多 3 条、总长 6000 字注入 system。
- 不做向量检索；这是本机小知识库。

### G. 用量

- 设置「用量」tab 显示真实数据：本地存储占用、工作区/会话/消息/产物、token、7 天柱状图、各 provider 调用统计。
- 复用 `refreshUsage()` 与 `/api/usage`、`/api/gateway/status`。
- 顶栏 ⚡ 浮层保留。

### H. 工具

- 核心工具、外部工具、插件工具列表；来源 `/api/tools`。
- 外部工具总开关：`POST /api/tools/external {enabled}`，运行时改 `agent.Loop.IncludeExternal`。
- 插件工具显示所属插件与状态；未加载的置灰。

### I. 软件包

- 显示已发现插件、版本、协议、状态、actions 数。
- 操作：加载 / 卸载 / 重载扫描；与扩展页共用接口。
- 不显示 npm 包（当前没有包管理器）。

### J. 语音

- `state.voiceEnabled`；设置开关持久化。
- 关闭时隐藏/禁用麦克风按钮；开启时使用浏览器 `SpeechRecognition`（已有 `toggleMic`）。
- 不支持时显示原因，不报错。

### K. 账户

- 显示本机服务端状态、GitHub 连接、数据目录、版本。
- 单用户模式，不做成员/权限。
- 提供「导出数据」「清空」快捷入口。

### L. 扩展 / 插件

- `GET /api/plugins` 列表：状态、协议、actions、调用次数、错误。
- 加载 / 卸载按钮；示例插件安装按钮（复制 `plugins/echo` 到 `$GQY_HOME/plugins`）。
- 插件调用调试：选择 action、填 JSON、显示结果。

## 状态扩展

```js
{
  onboarded: false,
  voiceEnabled: false,
  activePersonaId: 'default',
  personas: [{id, name, system, enabled}],
  memories: [{id, text, enabled, createdAt}],
  knowledge: [{id, title, content, enabled, updatedAt}],
  glossary: [{term, aliases, definition}],
  board: {cards: [{id, title, note, status, convId, createdAt}]}
}
```

`loadState` 对每个新字段做类型校验；缺失时补默认；旧数据不丢。

## 路由与层叠

- `setView('chat' | 'board')`；设置/用量/扩展/灯箱仍是浮层。
- 看板加入 Esc 关闭顺序：先浮层，再看板 → 对话。
- 侧栏加入口：对话、看板、扩展、设置。

## 验收

1. 首次打开出现引导，完成后刷新不再出现。
2. 看板增删改、移动、关联会话可用；窄屏不溢出。
3. 关键词浮窗在正文中可打开/关闭，键盘可用。
4. 人设/记忆/知识库启用后，发送请求包含对应 system 内容（用假上游断言）。
5. 用量、工具、软件包、账户页无占位文案。
6. 语音开关持久化；不支持时不报错。
7. `npm run smoke` 增加看板、引导、浮窗、设置 CRUD 断言。
