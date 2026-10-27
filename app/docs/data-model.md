# 数据模型

## localStorage

- 主键：`claude-webchat:v7`（结构变更时递增版本号，旧数据直接作废）。
- 值：整个 `state` 的 JSON；序列化时剔除 `url` 字段里的 `blob:` 地址。
- `gqy:files-width`：文件面板宽度。
- 所有读写都包在 try/catch 里；配额失败会 toast 提示（“改动只留在内存里”），不再静默丢弃。
- 保存成功会触发一次会话镜像（服务端可用时）：`PUT /api/sessions/:id`，写入 `$GQY_HOME`。

## state

```js
{
  workspaces: [{
    id, name,
    convs: [{ id, title, pinned, updatedAt, messages: [Message] }]
  }],
  activeWorkspaceId, activeConvId,
  model, effort,                 // 模型 id（空=服务端默认）/ 'Low' | 'Medium' | 'High'
  fontSize,                      // 'small' | 'normal' | 'large'
  thinkOpen,                     // 回复结束后是否保持思考展开
  sidebarCollapsed, reduceMotion,
  drafts: { [convId]: string }   // 每个会话的未发送草稿
}
```

`loadState()` 对每个字段做白名单校验，非法值回落到默认。
`reduceMotion` 没有存过时取系统 `prefers-reduced-motion`；`drafts` 只保留字符串值。

会话字段的迁移规则：

- `title`：trim 后非空、最长 80；旧数据缺失或非法时回落为「新会话」。
- `pinned`：只接受布尔值，缺省 `false`；`sortConvs()` 把置顶会话稳定排在同工作区最前。
- `updatedAt`：缺失或非有限数字时补当前时间。
- 单会话删除会同时清理 `drafts[id]`，撤销删除时恢复草稿；批量删除直接清理，不可撤销。
- 重命名 / 置顶 / 批量删除 / 导出都在前端完成；导出 JSON 包含 `id/title/pinned/updatedAt/messages/workspace/exportedAt`。

## 会话镜像（可选）

服务端可用时，`save()` 会把**当前会话**镜像到：

```
$GQY_HOME/sessions/<convId>/
  manifest.json                  session_id / workspace / title / 时间 / 消息数 / 产物清单 / 清理策略
  io/context/context.md          可读摘要（每条消息截断到 8000 字）
  io/context/messages.jsonl      逐条消息（客户端发来的原始 JSON）
  io/output/artifacts/<title>    产物正文（单文件上限 2 MB）
  io/output/index.json           产物文件名清单
  logs/tools/YYYY-MM-DD.jsonl    工具调用（参数脱敏，按消息日期分文件）
```

镜像字段只包含 `mirrorSession()` 挑出来的 `id / role / ts / text / thinking / tools / artifact / usage`。`question`、`attachments`、`pinned` 和草稿不落服务端。
镜像不参与前端恢复：刷新后仍以 localStorage 为准。删除会话不会删服务端目录（按 `docs/AboutProjectFile.md` 的 TTL 清理）。

## Message

见 `docs/pages.md` 的字段表。注意：

- `tools[].ms` 是工具执行时长；`status` 流式期间是 `running`，结束后统一为 `done`。
- `tools[].args` 是 `[key, value]` 对的数组，用于侧栏轨迹展示和落盘脱敏。
- `artifact.path` 决定文件面板里的目录根（取 dirname）。
- `question.answer` 为 `{ picks: number[], custom: string }`；一旦存在即视为已回答。回答后还会追加一条 user 消息并触发新请求。
- `usage` 来自服务端 `done` 事件，字段是 snake_case：`prompt_tokens` / `completion_tokens` / `total_tokens`。
- 附件的 `url` 可能是 `blob:`，序列化前会被剔除（导出与 localStorage 保存都走同一剔除函数）。

## 服务端持久化

- `github_token.json`：`$GQY_HOME/github_token.json`，0600，含 access token 与账户快照；`GET /api/github/me` 不返回 token。
- `providers.json`：`$GQY_HOME/providers.json`，0600，由 `cmd/gqy-import` 从旧 `config.jsonc` 提取；`GET /api/providers` 只读返回。
- 写入统一走 `writeFileAtomic`：建父目录 → 写临时文件（0600）→ rename。
- 会话 id 只接受 `^[A-Za-z0-9_-]{1,64}$`；产物文件名会过滤非 `A-Za-z0-9_.-` 字符。
- `messages.jsonl` 最多保留 500 条；产物单文件超过 2 MB 跳过。
