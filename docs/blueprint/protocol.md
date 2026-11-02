# protocol · 协议翻译

## 1. 状态

待施工（2026-11-02）。

## 2. 对应设计决定

兑现 `designs/03-协议翻译.md` 的 C1–C11：

- C1 对内只有一种请求模型：messages / tools / stream / max_tokens / temperature 等；`tool_calls[].arguments` 内部是对象。
- C2 对内只有一种响应事件流：thinking / text / tool_call / usage / done；stop_reason 值域六种。
- C3 thinking 独立于 text；Anthropic thinking `signature` 原样透传、不落日志。
- C4 OpenAI Chat Completions 原生透传 + 兼容流式；`protocol` 取值 openai-chat / anthropic-messages / gemini。
- C5 Anthropic Messages 编码与解码：thinking 块、tool_use / tool_result、stop_reason、流式事件族。
- C6 Gemini generateContent 编码与解码：generationConfig、functionCall / functionResponse、finishReason / usageMetadata。
- C7 非流式与流式等价：以事件流为唯一真相，双向可转。
- C8 流式边界样本放 `docs/designs/samples/03-协议翻译/`，验收看分片合法、usage 恰一次、终止帧。
- C9 上游错误归一成 `{code, retryable, http_status}`，喂给 02 的 R3。
- C10 错误码与 HTTP 状态码映射表。
- C11 `/v1/models` 输出形状。

## 3. 具体形状

### 3.1 对外端点路径

| 端点 | 方法 | 协议 | 模式 |
| --- | --- | --- | --- |
| `/v1/chat/completions` | POST | OpenAI Chat Completions | 非流式 + 兼容流式 |
| `/v1/models` | GET | OpenAI | 模型列表 |
| `/v1/messages` | POST | Anthropic Messages | 非流式 + 流式 |
| `/v1beta/models/{model}:generateContent` | POST | Gemini | 非流式 |
| `/v1beta/models/{model}:streamGenerateContent?alt=sse` | POST | Gemini | 流式 |

- `/v1/messages`、`/v1beta/*` 与 `/v1/*` 同受 02 R8 鉴权覆盖（Bearer / cookie）。
- Gemini 路径中的 `{model}` 允许带或不带 `models/` 前缀；处理器去掉前缀后按 02 R6 解析。

### 3.2 统一内部请求模型

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `model` | string | 是 | `provider/model`、`group/id` 或裸模型名（02 R6） |
| `messages` | array | 是 | 至少一条 |
| `messages[].role` | string | 是 | `system` / `user` / `assistant` / `tool` |
| `messages[].content` | string | 否 | 文本正文 |
| `messages[].images` | string[] | 否 | data URL；仅 user 消息 |
| `messages[].tool_calls` | array | 否 | 仅 assistant 消息 |
| `messages[].tool_calls[].id` | string | 是 | 工具调用 id |
| `messages[].tool_calls[].name` | string | 是 | 工具名 |
| `messages[].tool_calls[].arguments` | object | 是 | 已解析的参数对象 |
| `messages[].tool_call_id` | string | 否 | 仅 tool 消息，对齐调用 id |
| `messages[].name` | string | 否 | 仅 tool 消息，工具名 |
| `tools` | array | 否 | `{type:"function", function:{name, description, parameters}}` |
| `tool_choice` | string / object | 否 | 默认 `auto` |
| `stream` | bool | 否 | 默认 false |
| `max_tokens` | int | 否 | 输出上限 |
| `temperature` | number | 否 | 采样温度 |
| `top_p` | number | 否 | 核采样 |
| `stop` | string / string[] | 否 | 停止序列 |

### 3.3 统一内部响应事件流

五种事件，字段固定：

| 事件 | 字段 | 说明 |
| --- | --- | --- |
| `thinking` | `delta` | 思考增量 |
| `text` | `delta` | 正文增量 |
| `tool_call` | `index`、`id`、`name`、`arguments_delta` | 工具调用；参数分片累积 |
| `usage` | `prompt_tokens`、`completion_tokens` | 流式独立发一次；非流式并入 done |
| `done` | `stop_reason` | 值域：`end_turn` / `tool_use` / `max_tokens` / `stop_sequence` / `safety` / `other` |

一次流式的 JSONL 序列样例：

```jsonl
{"type":"thinking","delta":"让我想想"}
{"type":"text","delta":"你好"}
{"type":"tool_call","index":0,"id":"call_1","name":"read","arguments_delta":"{\"path\":"}
{"type":"tool_call","index":0,"id":"call_1","name":"read","arguments_delta":"\"a.md\"}"}
{"type":"usage","prompt_tokens":10,"completion_tokens":8}
{"type":"done","stop_reason":"tool_use"}
```

### 3.4 三条协议字段双向映射

#### OpenAI Chat Completions

内部 → OpenAI（请求编码）：

| 内部 | OpenAI 字段 | 说明 |
| --- | --- | --- |
| `messages[].role` | `role` | 四角色直通 |
| `messages[].content` | `content` | 文本直通 |
| `messages[].images` | `content` 数组 + `image_url` | 多模态 data URL |
| `tool_calls[].id` | `tool_calls[].id` | 直通 |
| `tool_calls[].name` | `tool_calls[].function.name` | 直通 |
| `tool_calls[].arguments`（对象） | `tool_calls[].function.arguments` | 序列化成 JSON 字符串 |
| `tools` | `tools` | `type:function` 直通 |
| `tool_choice` | `tool_choice` | 直通 |
| `stream` | `stream` | 直通 |
| `max_tokens` | `max_tokens` | 直通 |
| `temperature` / `top_p` / `stop` | 同名 | 直通 |

OpenAI → 内部（响应解码）：

| OpenAI 字段 | 内部事件 | 说明 |
| --- | --- | --- |
| `choices[].message.content` / `delta.content` | `text.delta` | 正文 |
| `message.reasoning_content` / `delta.reasoning_content` | `thinking.delta` | 思考 |
| `message.tool_calls` / `delta.tool_calls` | `tool_call` | `function.arguments` 反序列化为对象 |
| `choices[].finish_reason` | `done.stop_reason` | `stop`→end_turn；`length`→max_tokens；`tool_calls`→tool_use；`content_filter`→safety；其余→other |
| `usage.prompt_tokens` / `completion_tokens` | `usage` | 直通 |

流式帧：`data: {…}` 帧，末尾 `data: [DONE]`；`stream_options.include_usage` 时 usage 放最后一个 chunk。

#### Anthropic Messages

内部 → Anthropic（请求编码）：

| 内部 | Anthropic 字段 | 说明 |
| --- | --- | --- |
| system 消息 | `system` | string 或 blocks（text 拼接、image 并入 user） |
| user 文本 | `messages[].content[].text` | text 块 |
| user 图片 | `content[].image` | `{type:"base64", media_type, data}` |
| assistant `tool_calls` | `content[].tool_use` | `{id, name, input}`，input 为对象 |
| tool 消息 | `content[].tool_result` | `{tool_use_id, content}` |
| `tools` | `tools[].input_schema` | parameters 直通 |
| `max_tokens` | `max_tokens` | 必填；缺省填默认值再转发 |
| `stream` / `temperature` | 同名 | 直通 |

Anthropic → 内部（响应解码）：

| Anthropic 字段 | 内部事件 | 说明 |
| --- | --- | --- |
| `content[].text` | `text.delta` | 正文 |
| `content[].thinking` | `thinking.delta` | `signature` 保留透传 |
| `content[].tool_use` | `tool_call` | `{id, name, input}`，input 为对象 |
| `stop_reason` | `done.stop_reason` | `end_turn` / `tool_use` / `max_tokens` / `stop_sequence` |
| `usage.input_tokens` / `output_tokens` | `usage` | 直通 |

流式事件族：`message_start` → `content_block_start` → `content_block_delta`（`text_delta` / `input_json_delta` / `thinking_delta`）→ `content_block_stop` → `message_delta` → `message_stop`；`index` 连续，`tool_use.input` 以 `input_json_delta` 分片。

#### Gemini generateContent

内部 → Gemini（请求编码）：

| 内部 | Gemini 字段 | 说明 |
| --- | --- | --- |
| system 消息 | `systemInstruction.parts[].text` | 拼接 |
| user / assistant 文本 | `contents[].parts[].text` | role 映射 user / model |
| user 图片 | `parts[].inlineData` | `{mimeType, data}` |
| assistant `tool_calls` | `parts[].functionCall` | `{name, args}` |
| tool 消息 | `parts[].functionResponse` | `{name, response}` |
| `tools` | `tools[].functionDeclarations` | `{name, description, parameters}` |
| `temperature` | `generationConfig.temperature` | 直通 |
| `top_p` | `generationConfig.topP` | 直通 |
| `max_tokens` | `generationConfig.maxOutputTokens` | 直通 |
| `stop` | `generationConfig.stopSequences` | 直通 |

Gemini → 内部（响应解码）：

| Gemini 字段 | 内部事件 | 说明 |
| --- | --- | --- |
| `candidates[0].content.parts[].text` | `text.delta` | 正文 |
| `parts[].thought` | `thinking.delta` | 思考 |
| `parts[].functionCall` | `tool_call` | `{name, args}` |
| `finishReason` | `done.stop_reason` | `STOP`→end_turn；`MAX_TOKENS`→max_tokens；`SAFETY`→safety；其余→other |
| `usageMetadata.promptTokenCount` / `candidatesTokenCount` | `usage` | 直通 |

流式：`data: {…}` 帧，每帧带 `candidates[0].content.parts` 增量；末帧带 `finishReason` 与 `usageMetadata`。

### 3.5 错误归一取值表（C10 展开）

统一三元组 `{code, retryable, http_status}`：

| 场景 | `code` | `retryable` | `http_status` |
| --- | --- | --- | --- |
| 请求体非法、参数错 | `invalid_request` | 否 | 400 |
| 缺鉴权 / 密钥无效 | `auth_required` | 否 | 401 |
| 密钥无权限 | `permission_denied` | 否 | 403 |
| 模型不存在 | `model_not_found` | 否 | 404 |
| 上下文 / 请求体超限 | `context_length_exceeded` | 否 | 400（上游报 413 时保留 413） |
| 限流 / 配额 | `rate_limited` | 是 | 429 |
| 建连 / 读写失败 | `connection_error` | 是 | 502 |
| 上游 5xx | `provider_error` | 是 | 502 |
| 上游超时 | `upstream_timeout` | 是 | 504 |
| 候选耗尽 / 全部冷却 | `no_candidate` | 否（本请求终态） | 503 |
| 流开始后中断 | `stream_interrupted` | —（状态已发 200） | 已发 200，协议允许时补错误帧 |

对外错误体形状：

- OpenAI：`{"error": {"message": "…", "type": "…", "code": "…"}}`
- Anthropic：`{"type": "error", "error": {"type": "…", "message": "…"}}`
- Gemini：`{"error": {"code": 400, "message": "…", "status": "…"}}`

### 3.6 `/v1/models` 输出形状

```json
{
  "object": "list",
  "data": [
    {"id": "deepseek/deepseek-chat", "object": "model", "owned_by": "deepseek", "created": 0, "display_name": "DeepSeek Chat", "default": true},
    {"id": "deepseek/deepseek-reasoner", "object": "model", "owned_by": "deepseek", "created": 0},
    {"id": "group/daily", "object": "model", "owned_by": "group", "created": 0, "display_name": "日常", "members": ["deepseek/deepseek-chat", "moonshot/kimi-k2"]}
  ]
}
```

- 顺序：providers.json 的 provider 顺序在前，路由组在后；不返回任何密钥字段。

## 4. 边角情况

- **流式中断**：HTTP 状态 200 已发出，不能再改状态。OpenAI 补 `data:{"error":…}` 后断开；Anthropic 发 `error` 事件；Gemini 发 error 块；协议不允许时直接断开。请求日志记 `stream_interrupted`。尚未写字节时按 C9 走可重试分类。
- **上游非流式拆流**：整段结果拆成一次完整事件序列：role delta → content / reasoning_content → tool_calls → finish_reason → usage。每个分片完整，顺序固定（C7 等价）。
- **流式聚合回整段**：上游只支持流式、客户端要整段时（Gemini `generateContent` 路径），由 `streamGenerateContent` 聚合；在完成事件（finishReason 帧）之前断流返回 502，禁止空 200。
- **usage 恰一次**：流式独立发一次 `usage`，非流式并入 `done`；任何路径不得重复发或漏发。OpenAI 的 `stream_options.include_usage` 放最后一个 chunk。
- **Anthropic thinking signature 透传**：解码保留 `signature`，编码回同模型时原样带回；GQY 不解读、不落日志（G6）。
- **工具调用参数 JSON 半截**：分片（`input_json_delta` / `arguments_delta` / functionCall args 增量）按字符流累积，不假设单帧是完整 JSON；全部拼完再判定合法性；拼不合法记解码错误，归 `stream_interrupted` 或协议错误帧。

## 5. 由哪些测试守着

计划中的测试（旧实现 `protocol_test.go` 只作线索，以本文为准）：

- `TestChatPassthrough`：openai-chat 上游请求 / 响应形状透传，专有字段不丢。
- `TestChatStreamCompatible`：上游非流式拆成标准 SSE 帧 + `[DONE]`；`include_usage` 时 usage 放末 chunk。
- `TestAnthropicMessagesEncode`：system / messages / tools 编码正确；`max_tokens` 缺省填默认。
- `TestAnthropicToolUse`：tool_use / tool_result 与内部 tool_call / tool 消息互转，`tool_use_id` 对齐。
- `TestAnthropicThinkingSignature`：thinking 块 `signature` 解码保留、编码带回。
- `TestAnthropicStreamEvents`：message_start → … → message_stop 事件族完整，`index` 连续。
- `TestGeminiGenerateEncode`：generationConfig、functionCall / functionResponse 映射正确。
- `TestGeminiStream`：data 帧增量 + 末帧 finishReason / usageMetadata。
- `TestStreamNonStreamEquivalence`：同请求两模式拼出 text / thinking / tool_call / usage / stop_reason 一致。
- `TestStreamInterrupted`：流已开始后上游断开，协议错误帧或断开，日志记 `stream_interrupted`。
- `TestErrorNormalization`：429 / 5xx 判 retryable、4xx 判非 retryable，并驱动 02 R3 的转移 / 冷却。
- `TestModelsShape`：`/v1/models` 形状与 C11 一致，含路由组、无密钥字段。

## 6. 对应代码

待建。旧实现参考：`app/internal/httpapi/protocol.go`（仅事实线索，不照抄）。
