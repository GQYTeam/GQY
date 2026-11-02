# 03-协议翻译 · 规范样本

这些文件是**规范样例**（canonical fixtures）：手工书写，用于验收「分片拼接、usage、终止帧、thinking 与 text 分界」等边界，不是真实抓包。

## 维护规则

- 形状权威是 `blueprint/protocol.md`；本目录与它不一致时，以 blueprint 为准。
- 决定与映射规则见 `designs/03-协议翻译.md`（C1–C11）。
- 改协议映射时，先改 blueprint，再更新这里的样本。

## 文件清单

| 文件 | 内容 |
| --- | --- |
| `openai/chat.stream.txt` | OpenAI 兼容流式 SSE：role delta → reasoning_content → content → tool_calls 分片 → finish_reason → usage chunk → `data: [DONE]` |
| `openai/chat.nonstream.json` | OpenAI 非流式响应（message + reasoning_content + tool_calls + usage） |
| `openai/error.429.json` | OpenAI 429 错误体（归一为 `rate_limited` / retryable） |
| `anthropic/messages.stream.txt` | Anthropic 流式事件族：message_start → thinking → text → tool_use input_json_delta → message_delta → message_stop |
| `gemini/generateContent.sse.txt` | Gemini `alt=sse` 流式：parts 增量 → functionCall → finishReason + usageMetadata |

目录用协议短名：`openai/`（openai-chat）、`anthropic/`（anthropic-messages）、`gemini/`。

## 验收怎么对照

- 注释行以 `#` 开头，不是协议字节；去掉 `#` 行并保留空行，可还原协议帧序列。
- **分片拼接合法**：tool_calls 的 `arguments`、`input_json_delta` 累积后必须能解析成合法 JSON 对象。
- **usage 恰一次**：每条流里 usage 只出现一次——OpenAI 末 chunk；Anthropic `message_start` 的 `input_tokens` + `message_delta` 的 `output_tokens` 合并发一次；Gemini 末帧。
- **终止帧存在**：OpenAI `data: [DONE]`；Anthropic `message_stop`；Gemini 带 `finishReason` 的末帧。
- **thinking 与 text 分界**：thinking 事件绝不并入 text。
