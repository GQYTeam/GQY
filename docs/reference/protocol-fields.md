# protocol-fields · 协议字段手册

本页是 `blueprint/protocol.md` 的对外快照，给外部客户端与 provider 适配器实现者查字段。字段不一致时以 blueprint 为准。错误分类见 `http-api.md` 第 6 节，本页只给协议层视角的表。

## 1. 统一内部请求模型

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `model` | string | 是 | `provider/model`、`group/id` 或裸模型名 |
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

## 2. 统一响应事件流

| 事件 | 字段 | 说明 |
| --- | --- | --- |
| `thinking` | `delta` | 思考增量，可折叠 |
| `text` | `delta` | 正文增量 |
| `tool_call` | `index`、`id`、`name`、`arguments_delta` | 工具调用；参数分片累积 |
| `usage` | `prompt_tokens`、`completion_tokens` | 流式独立发一次；非流式并入 done |
| `done` | `stop_reason` | 值域：`end_turn` / `tool_use` / `max_tokens` / `stop_sequence` / `safety` / `other` |

- 对外 OpenAI 兼容流式与内部事件流的关系见 `http-api.md` 第 7 节。

## 3. OpenAI Chat Completions 映射

provider 协议为 `openai-chat` 时原生透传：客户端字段与上游字段同名直通；不一致时经内部模型翻译。

### 3.1 请求：客户端 → 内部 → 上游

| 客户端字段 | 内部字段 | 上游字段 |
| --- | --- | --- |
| `model` | `model` | `model` |
| `messages[].role` | `messages[].role` | `messages[].role` |
| `messages[].content`（文本） | `messages[].content` | `messages[].content` |
| `messages[].content`（image 部分） | `messages[].images` | `messages[].content`（`image_url`） |
| `messages[].tool_calls[].id` | `tool_calls[].id` | `tool_calls[].id` |
| `messages[].tool_calls[].function.name` | `tool_calls[].name` | `tool_calls[].function.name` |
| `messages[].tool_calls[].function.arguments`（字符串） | `tool_calls[].arguments`（对象） | `tool_calls[].function.arguments`（字符串） |
| `messages[].tool_call_id` + `content`（tool 消息） | tool 消息 | 同名直通 |
| `tools` | `tools` | `tools` |
| `tool_choice` | `tool_choice` | `tool_choice` |
| `stream` | `stream` | `stream` |
| `max_tokens` | `max_tokens` | `max_tokens` |
| `temperature` | `temperature` | `temperature` |
| `top_p` | `top_p` | `top_p` |
| `stop` | `stop` | `stop` |

### 3.2 响应：上游 → 内部 → 客户端

| 上游字段 | 内部事件 | 客户端字段 |
| --- | --- | --- |
| `choices[].message.content` / `delta.content` | `text.delta` | `choices[].message.content` / `delta.content` |
| `message.reasoning_content` / `delta.reasoning_content` | `thinking.delta` | `reasoning_content` |
| `message.tool_calls` / `delta.tool_calls` | `tool_call` | `tool_calls`（arguments 序列化为字符串） |
| `choices[].finish_reason` | `done.stop_reason` | `finish_reason` |
| `usage.prompt_tokens` / `completion_tokens` | `usage` | `usage` |

`finish_reason` 映射：`stop`→`end_turn`；`length`→`max_tokens`；`tool_calls`→`tool_use`；`content_filter`→`safety`；其余→`other`。

## 4. Anthropic Messages 映射

### 4.1 请求：客户端 → 内部 → 上游

| 客户端字段 | 内部字段 | 上游字段 |
| --- | --- | --- |
| `system`（string 或 blocks） | system 消息（text 拼接；image 并入 user） | `system` |
| `messages[].role` | `messages[].role`（user / assistant） | `messages[].role` |
| `content[].text` | `messages[].content` | `content[].text` |
| `content[].image` | `messages[].images` | `content[].image` |
| `content[].tool_use` | `tool_calls[]`（input 反序列化为对象） | `content[].tool_use` |
| `content[].tool_result` | tool 消息（`tool_use_id` 对齐 `tool_call_id`） | `content[].tool_result` |
| `tools[].input_schema` | `tools[].function.parameters` | `tools[].input_schema` |
| `max_tokens` | `max_tokens` | `max_tokens`（缺省填默认） |
| `stream` | `stream` | `stream` |
| `temperature` | `temperature` | `temperature` |

### 4.2 响应：上游 → 内部 → 客户端

| 上游字段 | 内部事件 | 客户端字段 |
| --- | --- | --- |
| `content[].text` | `text.delta` | text 块 |
| `content[].thinking` | `thinking.delta`（`signature` 透传） | thinking 块（含 `signature`） |
| `content[].tool_use` | `tool_call` | tool_use 块（`input` 对象） |
| `stop_reason` | `done.stop_reason` | `stop_reason` |
| `usage.input_tokens` / `output_tokens` | `usage` | `usage` |

`stop_reason` 映射：`end_turn` / `tool_use` / `max_tokens` / `stop_sequence` 直通内部。

流式事件族：`message_start` → `content_block_start` → `content_block_delta`（`text_delta` / `input_json_delta` / `thinking_delta`）→ `content_block_stop` → `message_delta` → `message_stop`。

## 5. Gemini generateContent 映射

### 5.1 请求：客户端 → 内部 → 上游

| 客户端字段 | 内部字段 | 上游字段 |
| --- | --- | --- |
| `systemInstruction.parts[].text` | system 消息（拼接） | `systemInstruction.parts[].text` |
| `contents[].role`（user / model） | `messages[].role` | `contents[].role` |
| `parts[].text` | `messages[].content` | `parts[].text` |
| `parts[].inlineData` | `messages[].images` | `parts[].inlineData` |
| `parts[].functionCall` | `tool_calls[]` | `parts[].functionCall` |
| `parts[].functionResponse` | tool 消息（按 `name` 对齐） | `parts[].functionResponse` |
| `tools[].functionDeclarations` | `tools[]` | `tools[].functionDeclarations` |
| `generationConfig.temperature` | `temperature` | `generationConfig.temperature` |
| `generationConfig.topP` | `top_p` | `generationConfig.topP` |
| `generationConfig.maxOutputTokens` | `max_tokens` | `generationConfig.maxOutputTokens` |
| `generationConfig.stopSequences` | `stop` | `generationConfig.stopSequences` |

### 5.2 响应：上游 → 内部 → 客户端

| 上游字段 | 内部事件 | 客户端字段 |
| --- | --- | --- |
| `candidates[0].content.parts[].text` | `text.delta` | `parts[].text` |
| `parts[].thought` | `thinking.delta` | `parts[].thought` |
| `parts[].functionCall` | `tool_call` | `parts[].functionCall` |
| `finishReason` | `done.stop_reason` | `finishReason` |
| `usageMetadata.promptTokenCount` / `candidatesTokenCount` | `usage` | `usageMetadata` |

`finishReason` 映射：`STOP`→`end_turn`；`MAX_TOKENS`→`max_tokens`；`SAFETY`→`safety`；其余→`other`。

## 6. 关键字段逐项说明

| 字段（协议） | 形状 | 内部表示 | 说明 |
| --- | --- | --- | --- |
| thinking（OpenAI `reasoning_content`） | string | `thinking.delta` | 思考文本；不并入正文 |
| thinking（Anthropic `content[].thinking`） | `{type:"thinking", thinking, signature}` | `thinking.delta` + signature 透传 | `signature` 原样带回同模型，不落日志 |
| thought（Gemini `parts[].thought`） | thought 部分 | `thinking.delta` | 思考过程 |
| tool_use（Anthropic） | `{type:"tool_use", id, name, input}` | `tool_call`（input 对象） | 流式时 `input` 以 `input_json_delta` 分片 |
| tool_calls（OpenAI） | `[{id, function:{name, arguments}}]` | `tool_call` | `arguments` 是 JSON 字符串，内部反序列化为对象 |
| functionCall（Gemini） | `{name, args}` | `tool_call` | `args` 是对象 |
| tool_result（Anthropic） | `{type:"tool_result", tool_use_id, content}` | tool 消息 | `tool_use_id` 对齐 `tool_call_id` |
| functionResponse（Gemini） | `{name, response}` | tool 消息 | 按 `name` 对齐调用 |
| stop_reason（Anthropic） | `end_turn` / `tool_use` / `max_tokens` / `stop_sequence` | `done.stop_reason` | 直通 |
| finish_reason（OpenAI） | `stop` / `length` / `tool_calls` / `content_filter` | `done.stop_reason` | 见 3.2 映射 |
| finishReason（Gemini） | `STOP` / `MAX_TOKENS` / `SAFETY` / … | `done.stop_reason` | 见 5.2 映射 |
| usageMetadata（Gemini） | `{promptTokenCount, candidatesTokenCount, totalTokenCount}` | `usage` | 取前两项 |

## 7. 错误归一（协议层视角）

取值表与 `http-api.md` 第 6 节一致，这里只列协议适配器视角：

| `code` | `retryable` | `http_status` | 协议层含义 |
| --- | --- | --- | --- |
| `invalid_request` | 否 | 400 | 该协议请求解码失败或参数非法 |
| `auth_required` | 否 | 401 | 缺鉴权 / 上游密钥无效 |
| `permission_denied` | 否 | 403 | 上游密钥无权限 |
| `model_not_found` | 否 | 404 | 模型未命中（02 R6） |
| `context_length_exceeded` | 否 | 400 / 413 | 上下文或请求体超限 |
| `rate_limited` | 是 | 429 | 限流 / 配额 |
| `connection_error` | 是 | 502 | 建连 / 读写失败 |
| `provider_error` | 是 | 502 | 上游 5xx |
| `upstream_timeout` | 是 | 504 | 上游超时 |
| `no_candidate` | 否（终态） | 503 | 候选耗尽 / 全部冷却 |
| `stream_interrupted` | — | 已发 200 | 流开始后中断，协议允许时补错误帧 |
