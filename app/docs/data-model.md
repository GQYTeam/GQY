# 数据模型

## localStorage

- 键：`claude-webchat:v6`（结构变更时递增版本号，旧的演示数据直接作废）。
- 值：整个 `state` 的 JSON。
- 另有 `gqy:files-width` 保存文件面板宽度。
- 所有读写都包在 try/catch 里；配额失败会 toast 提示（“改动只留在内存里”），不再静默丢弃。
- 保存成功会触发一次会话镜像（服务端可用时）：`PUT /api/sessions/:id`，写入 `$GQY_HOME`。

## state

```js
{
  workspaces: [{
    id, name,
    convs: [{ id, title, updatedAt, messages: [Message] }]
  }],
  activeWorkspaceId, activeConvId,
  model, effort,                 // 'DeepSeek-V4.1-Flash' | '...Pro' / 'Low' | 'Medium' | 'High'
  fontSize,                      // 'small' | 'normal' | 'large'
  streamSpeed,                   // 'slow' | 'normal' | 'fast'
  thinkOpen,                     // 回复结束后是否保持思考展开
  sidebarCollapsed, reduceMotion,
  drafts: { [convId]: string }   // 每个会话的未发送草稿
}
```

`loadState()` 对每个字段做白名单校验，非法值回落到默认。
`reduceMotion` 没有存过时取系统 `prefers-reduced-motion`；`drafts` 只保留字符串值。

## 会话镜像（可选）

服务端可用时，`save()` 会把**当前会话**镜像到：

```
$GQY_HOME/sessions/<convId>/
  manifest.json                  session_id / workspace / artifacts 清单
  io/context/context.md          可读摘要
  io/context/messages.jsonl      逐条消息
  io/output/artifacts/<title>    产物正文
  logs/tools/YYYY-MM-DD.jsonl    工具调用（参数脱敏）
```

镜像不参与前端恢复：刷新后仍以 localStorage 为准。删除会话不会删服务端目录（按 `docs/AboutProjectFile.md` 的 TTL 清理）。

## Message

见 `docs/pages.md` 的字段表。注意：

- `tools[].ms` 是演示用的执行时长；`status` 只在流式期间是 `running`。
- `artifact.path` 决定文件面板里的目录根（取 dirname）。
- `question.answer` 为 `{ picks: number[], custom: string }`；一旦存在即视为已回答。
- 附件的 `url` 可能是 `blob:`，序列化前会被剔除（设置页的导出逻辑）。

## 演示数据

- 种子会话：`seedState()`（Python 数据集问答 + 产物 + 提问）。
- 项目文件样例：`js/samples.js` 的 `FILES_MOCK`（train.py / README.md）。
- 看板、天气、用量、会话统计：`app.js` 内的常量与 `usageSnapshot` / `ctxUsage`，全部为模拟值。
