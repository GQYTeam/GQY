# session · 会话与消息

**状态**：待施工（2026-11-02）

**对应设计决定**：S1–S7（05-会话与产物）、C2（03-协议翻译 的事件流是消息的真相源）。

## 具体形状

### 会话清单

会话目录与 manifest 形状见 `store.md`；本页只管「一次会话里的行为与消息」。

### 消息与事件流

消息不是独立存储的文档，而是事件流的投影（S2）：任何界面都能从事件流重建消息。一次回复的 SSE 事件五种：

| 事件 | 字段 | 含义 |
| --- | --- | --- |
| `thinking` | `delta`、`done` | 思考过程增量，默认折叠显示 |
| `tool` | `tool_name`、`phase`（start/result）、`arguments`、`output`、`duration_ms`、`status` | 一次工具调用的开始与结果 |
| `text` | `delta` | 正文增量 |
| `question` | `options`（2–4 个）、`allow_custom` | `ask_user` 提问，loop 暂停等回答 |
| `done` | `usage`、`stop_reason` | 回复结束 |

- 非流式请求返回同样的事件序列，一次性送达。
- `stop_reason` 取值与 `usage` 形状见 `protocol.md` C2/C5。

### 附件与产物

- 上传：文本注入上下文、图片走多模态、其他二进制落盘到 `tmp/uploads/`；同名不覆盖（O_EXCL）。
- 产物：消息关联的产物进灯箱；最终产物记录在 `manifest.json` 的 `artifacts`，中间产物在 `tmp/generated/`。
- 路径形状与权限见 `store.md` ST6。

### 草稿 / 停止 / 重试

| 操作 | 语义 |
| --- | --- |
| 草稿 | 按会话保存输入框内容，切换会话不丢 |
| 停止 | 前端发停止信号，取消在途请求；已产出增量保留，该回复标 `canceled` |
| 重试 | 以当前消息为界重新发起一轮，不覆盖历史；旧回复折叠标「已替代」 |

## 边角情况

- 事件流中断：已收到的事件保留，消息标「不完整」，不假装成功。
- 同名附件并发：O_EXCL 抢占，后到者换名或报错，不覆盖。
- 会话重建：从事件流重建到最后一个完整回合（ST5）。
- `ask_user` 超时未答：回复挂起，loop 不继续；用户可取消。
- 工具轨迹敏感参数：只记脱敏摘要（ST6、T9）。

## 计划中的测试

- `TestEventsRebuildMessage`：五事件按序重放成消息，非流式与流式一致。
- `TestAttachmentNoOverwrite`：同名并发上传不覆盖。
- `TestDraftPersistsAcrossSessions`：切换会话草稿不丢。
- `TestStopKeepsIncrements`：停止后已产出增量保留并标 canceled。
- `TestRetryKeepsHistory`：重试不覆盖历史，旧回复折叠。
- `TestAskUserPausesLoop`：question 事件后 loop 暂停等答案。

## 对应代码

待建。旧实现参考：`app/internal/session/store.go`、`app/internal/agent/agent.go`（git 历史 HEAD~2，仅作事实线索）。
