# http-api · 对外 HTTP 接口参考

本页是 `blueprint/auth.md`、`blueprint/gateway.md`、`blueprint/protocol.md`、`blueprint/config.md` 的对外快照。字段不一致时以 blueprint 为准。

## 1. 鉴权

- 未设置 `GQY_AUTH_TOKEN`：鉴权关闭，服务只在回环地址监听并放行本机请求；绑定非回环且无令牌时拒绝启动（A3）。
- 设置后：覆盖 `/api`、`/v1`、`/v1beta` 三组前缀；`/api/auth` 与静态文件豁免（A1）。

携带方式二选一（A2）：

| 方式 | 形状 | 说明 |
| --- | --- | --- |
| Bearer | `Authorization: Bearer <token>` | 外部客户端与 OpenAI 兼容调用方首选 |
| cookie | `gqy_token=<token>` | `HttpOnly`、`SameSite=Lax`、`Path=/`、`MaxAge=7d`；TLS 或 `X-Forwarded-Proto: https` 时加 `Secure` |

401 响应体按前缀区分：

| 前缀 | 响应体 |
| --- | --- |
| `/v1/` | `{"error":{"message":"缺少或错误的访问令牌","type":"gqy_error","code":"auth_required"}}` |
| `/v1beta/` | Gemini 错误形状，同 `code` |
| `/api/` | `{"error":"auth_required","message":"缺少或错误的访问令牌"}` |

## 2. 限流

- `rate_limit`（`settings.json` 的 `rate_limit`，环境变量 `GQY_RATE_LIMIT` 非空时覆盖）按客户端 IP 做每分钟固定窗口计数；0 表示不限。
- 限流在鉴权外层执行：未认证请求同样计数（A4）。
- 命中返回 429，带 `Retry-After: 60`。

429 响应体：

| 前缀 | 响应体 |
| --- | --- |
| `/v1/` | `{"error":{"message":"请求过于频繁","type":"gqy_error","code":"rate_limited"}}` |
| `/v1beta/` | Gemini 错误形状，同 `code` |
| `/api/` | `{"error":"rate_limited","message":"请求过于频繁，请稍后再试"}` |

## 3. 端点总表

| 方法 + 路径 | 鉴权 | 用途 | 成功 | 主要失败 |
| --- | --- | --- | --- | --- |
| `GET /healthz` | 否 | 存活检查 | `200 {"status":"ok"}` | — |
| `POST /api/auth` | 否 | 校验令牌，种登录 cookie | `200 {"ok":true}` | `401 {"error":"auth_required","message":"…"}` |
| `POST /v1/chat/completions` | 是 | OpenAI 兼容对话（非流式 + 兼容流式） | `200` JSON 或 SSE | 400 / 401 / 404 / 429 / 502 / 503 / 504，OpenAI 错误形状 |
| `GET /v1/models` | 是 | 模型列表（provider 模型 + 路由组） | `200 {"object":"list","data":[…]}` | 401 |
| `POST /v1/messages` | 是 | Anthropic Messages（非流式 + 流式） | `200` JSON 或 SSE 事件族 | 400 / 401 / 429 / 502 / 503 / 504，Anthropic 错误形状 |
| `POST /v1beta/models/{model}:generateContent` | 是 | Gemini 非流式生成 | `200` candidates + usageMetadata | 400 / 401 / 429 / 502 / 503 / 504，Gemini 错误形状 |
| `POST /v1beta/models/{model}:streamGenerateContent?alt=sse` | 是 | Gemini 流式生成 | `200` SSE `data:` 帧 | 同上 |
| `GET /api/gateway/status` | 是 | 健康 / 冷却 / 并发 / 配额状态 | `200` JSON | 401 |
| `GET /api/config` | 是 | 当前配置安全视图（密钥只回 `api_key_set`） | `200` JSON | 401 |
| `POST /api/config/reload` | 是 | 手动重载配置 | `200 {"ok":true,"version":N}` | `400` 校验失败，保留旧快照 |

- Gemini 路径的 `{model}` 允许带或不带 `models/` 前缀。
- `/api/chat`（内部对话，SSE 事件流）走同一网关，同样受鉴权与限流覆盖。

## 4. `GET /v1/models` 响应字段

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

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `object` | string | 固定 `"list"` |
| `data` | array | 模型条目；providers.json 的 provider 顺序在前，路由组在后 |
| `data[].id` | string | `provider/model` 或路由组 `group/<id>` |
| `data[].object` | string | 固定 `"model"`，保证 OpenAI 客户端可用 |
| `data[].owned_by` | string | provider id，或路由组的 `"group"` |
| `data[].created` | number | 固定 0（无真实时间戳） |
| `data[].display_name` | string | 展示名，可选 |
| `data[].default` | bool | 是否 providers.json 的默认模型，可选 |
| `data[].members` | string[] | 路由组成员，仅 `group/<id>` 条目携带 |

- 不返回任何密钥字段。

## 5. `GET /api/gateway/status` 响应字段

```json
{
  "auth_enabled": true,
  "rate_limit": 60,
  "default": {"provider": "deepseek", "model": "deepseek-chat"},
  "providers": [
    {
      "id": "deepseek",
      "name": "DeepSeek",
      "protocol": "openai-chat",
      "models": ["deepseek-chat", "deepseek-reasoner"],
      "accounts": [
        {
          "id": "a1",
          "inflight": 1,
          "max_concurrency": 2,
          "calls": 12,
          "failures": 1,
          "cooldown_remaining_s": 0,
          "last_error": "",
          "token_used_today": 3100000,
          "token_limit": 20000000,
          "quota_exhausted": false,
          "key_set": true
        }
      ]
    }
  ],
  "groups": [
    {"id": "daily", "strategy": "order", "cursor": 0,
     "members": ["deepseek/deepseek-chat", "moonshot/kimi-k2"]}
  ]
}
```

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `auth_enabled` | bool | 是否启用 `GQY_AUTH_TOKEN` |
| `rate_limit` | number | 每客户端每分钟限流；0 = 不限 |
| `default` | object | 默认模型 `{provider, model}` |
| `providers[].id` / `name` / `protocol` / `models` | — | provider 标识、展示名、协议、模型列表 |
| `accounts[].id` | string | 账号 id |
| `accounts[].inflight` | number | 当前在途请求数 |
| `accounts[].max_concurrency` | number | 该账号并发上限 |
| `accounts[].calls` / `failures` | number | 累计调用数 / 失败数 |
| `accounts[].cooldown_remaining_s` | number | 冷却剩余秒数；0 = 不在冷却 |
| `accounts[].last_error` | string | 最近错误码；无则空串 |
| `accounts[].token_used_today` | number | 今日（UTC 自然日）已用 token |
| `accounts[].token_limit` | number | 日配额；0 = 不限 |
| `accounts[].quota_exhausted` | bool | 今日配额是否用尽（true 则不进候选） |
| `accounts[].key_set` | bool | 是否已配密钥；永无明文 |
| `groups[].id` / `strategy` / `members` | — | 路由组标识、策略、成员 |
| `groups[].cursor` | number | `rotate` 组当前指针；`order` / `usage` 下无意义 |

## 6. 错误归一

统一三元组 `{code, retryable, http_status}`（`blueprint/protocol.md` 3.5）：

| `code` | `http_status` | `retryable` | 含义 |
| --- | --- | --- | --- |
| `invalid_request` | 400 | 否 | 请求体非法、参数错 |
| `auth_required` | 401 | 否 | 缺鉴权 / 密钥无效 |
| `permission_denied` | 403 | 否 | 密钥无权限 |
| `model_not_found` | 404 | 否 | 模型不存在 |
| `context_length_exceeded` | 400（上游报 413 时保留 413） | 否 | 上下文 / 请求体超限 |
| `rate_limited` | 429 | 是 | 限流 / 配额 |
| `connection_error` | 502 | 是 | 建连 / 读写失败 |
| `provider_error` | 502 | 是 | 上游 5xx |
| `upstream_timeout` | 504 | 是 | 上游超时 |
| `no_candidate` | 503 | 否（本请求终态） | 候选耗尽 / 全部冷却 |
| `stream_interrupted` | 已发 200 | — | 流开始后中断，协议允许时补错误帧 |

- `retryable=true`：网关可在响应开始前换候选重试（02 R3）。
- `retryable=false`：不重试、不冷却，直接返回；`no_candidate` 是网关重试耗尽后的终态。
- 错误体按协议形状：OpenAI `{"error":{"message","type","code"}}`；Anthropic `{"type":"error","error":{"type","message"}}`；Gemini `{"error":{"code","message","status"}}`。

## 7. 流式约定

内部对话与前端使用 05 S2 的五种 SSE 事件；OpenAI 兼容流式使用标准 SSE 帧。

五种事件（`/api/chat` 与前端消息流）：

| 事件 | 字段 | 说明 |
| --- | --- | --- |
| `thinking` | `delta` | 思考增量，可折叠 |
| `tool` | `name`、`arguments`、`result`、`duration_ms`、`status` | 一次工具调用的开始 / 参数 / 输出 / 耗时 / 状态（06 T9） |
| `text` | `delta` | 正文增量 |
| `question` | `question`、`options` | ask_user 提问：选项 / 数字键 / 自定义答案（06 T7） |
| `done` | `usage`、`stop_reason` | 回复结束，带用量与停止原因 |

样例：

```jsonl
{"type":"thinking","delta":"让我想想"}
{"type":"tool","name":"edit","arguments":{"path":"a.txt","content":"hi"},"result":"已写入 a.txt","duration_ms":8,"status":"ok"}
{"type":"text","delta":"已写入"}
{"type":"question","question":"要继续吗？","options":["继续","取消"]}
{"type":"done","usage":{"prompt_tokens":10,"completion_tokens":8},"stop_reason":"end_turn"}
```

OpenAI 兼容流式（`/v1/chat/completions`，`stream:true`）：

- `data: {…}` 帧，末尾 `data: [DONE]`。
- `stream_options.include_usage` 时 usage 放在最后一个 chunk。
- Anthropic / Gemini 的流式事件族见 `blueprint/protocol.md` 3.4。

## 实现现状（2026-11-02 审计）

上表是施工 001–004 的快照。以下是 `server.go` 当前实际注册的全部路由（由代码生成，避免漂移）：

- `GET /healthz`
- `GET /v1/models`
- `POST /v1/chat/completions`
- `POST /v1/messages`
- `POST /v1beta/models/`
- `POST /api/chat`
- `GET /api/plugins`
- `POST /api/plugins/reload`
- `POST /api/plugins/{id}/load`
- `POST /api/plugins/{id}/unload`
- `POST /api/plugins/{id}/call`
- `POST /api/goal`
- `GET /api/goal`
- `POST /api/goal/{id}/pause`
- `POST /api/goal/{id}/resume`
- `POST /api/goal/{id}/queue`
- `POST /api/goal/{id}/adopt`
- `POST /api/sessions`
- `GET /api/sessions`
- `GET /api/sessions/{id}`
- `DELETE /api/sessions/{id}`
- `GET /api/sessions/{id}/draft`
- `PUT /api/sessions/{id}/draft`
- `POST /api/sessions/{id}/uploads`
- `POST /api/sessions/{id}/artifacts`
- `POST /api/auth`
- `GET /api/config`
- `POST /api/config/reload`
- `GET /api/gateway/status`
- `GET /`
- `GET /api/tools`
