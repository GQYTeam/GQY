# 数据模型

## localStorage

- 键：`claude-webchat:v6`（结构变更时递增版本号，旧的演示数据直接作废）。
- 值：整个 `state` 的 JSON。
- 另有 `gqy:files-width` 保存文件面板宽度。
- 所有读写都包在 try/catch 里；配额失败静默忽略（演示可接受，真实产品应提示）。

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
  sidebarCollapsed, reduceMotion
}
```

`loadState()` 对每个字段做白名单校验，非法值回落到默认。

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
