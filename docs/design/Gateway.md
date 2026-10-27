# 网关设计

目标：让顾清影的后端从“单上游”变成“本地多 provider 网关”，同时服务内部 agent loop 和外部 OpenAI 兼容客户端。

调研依据见 `docs/research/MagpieGateway.md`；插件协议见 `docs/design/PluginRuntime.md`。

## 范围

**做：**

- 从 `$GQY_HOME/providers.json` 读取多个 OpenAI 兼容 provider；环境变量 `GQY_API_BASE/KEY/MODEL` 作为兜底 provider。
- 模型解析：`provider/model`、路由组 `group/id`、裸模型名、默认模型。
- 路由组策略：`order`、`rotate`、`usage`（按最近使用计数）。
- 响应开始前的失败转移；429 / 5xx / 连接错误可重试，4xx 不重试。
- 每 provider 并发上限与有界等待；429/5xx 进入冷却。
- 会话亲和：同一会话 id 固定 provider，内存态。
- 请求日志 `$GQY_HOME/gateway/requests.jsonl`。
- `GET /api/gateway/status`、`GET /v1/models`、`POST /v1/chat/completions`。
- 内部 `/api/chat` 走同一个网关。

**不做（后置）：**

- Anthropic Messages / Gemini 协议翻译。
- 订阅账号 OAuth、配额窗口、每日唤醒。
- 远程 magpie、SSRF 防护之外的网络策略。
- 图片、embedding、视频端点。
- 通用中间件链（只预留接口）。

## 数据

### providers.json

```json
{
  "providers": [
    {
      "id": "deepseek",
      "display_name": "DeepSeek",
      "base_url": "https://api.deepseek.com/v1",
      "protocol": "openai-chat",
      "default_model": "deepseek-chat",
      "models": ["deepseek-chat", "deepseek-reasoner"],
      "api_key": "sk-...",
      "user_agent": "",
      "max_concurrency": 2
    }
  ]
}
```

- `api_key` 允许为空；为空时回退到环境变量 `GQY_PROVIDER_<ID>_API_KEY`（ID 转大写、非字母数字转 `_`）。
- `protocol` 只支持 `openai-chat`；空值等价。
- 文件不存在时只用环境变量 provider。
- 读取时对前端返回脱敏版本（`api_key_set: true`），不返回明文。

### routing.json

```json
{
  "default": {"provider": "deepseek", "model": "deepseek-chat"},
  "groups": [
    {
      "id": "daily",
      "name": "日常",
      "strategy": "order",
      "members": ["deepseek/deepseek-chat", "moonshot/kimi-k2"]
    }
  ],
  "max_attempts": 3,
  "cooldown_seconds": 30,
  "queue_timeout_ms": 1500
}
```

- `groups[].id` 唯一；重复注册报错。
- `members` 支持 `provider/model`；裸模型名按 provider 默认模型解析。
- `strategy`：`order`（默认）/ `rotate` / `usage`。
- `max_attempts` 上限 5；`cooldown_seconds` 上限 600。

### 运行态

内存：

```go
type providerState struct {
    inflight chan struct{}   // 信号量
    cooldownUntil time.Time
    lastErr string
    calls int64
    failures int64
}
type groupState struct { cursor uint64 }
```

落盘：`$GQY_HOME/gateway/requests.jsonl`，每行：

```json
{"ts":"...","request_id":"...","session":"...","provider":"deepseek","model":"deepseek-chat","status":"ok|error|cooldown|no_candidate","http_status":200,"duration_ms":123,"attempt":1,"error_code":""}
```

不记录 prompt / 回复正文。

## 模型解析

输入：`model` 字符串、请求上下文（session id）、可选 `preferred provider`。

1. `group/<id>` 或裸 `<id>` 命中路由组 → 该组候选。
2. `provider/model` 且 provider 存在 → 单候选。
3. 裸模型名：遍历 provider 的 `models` / `default_model`，命中即候选；多个命中按 provider 文件顺序。
4. 空模型：`routing.default`；没有则环境变量 provider 的默认模型。
5. 都没有 → `ErrNoCandidate`。

## 候选与重试

```
candidates := resolve(model)
sort by strategy; resting candidates last
for attempt := 0; attempt < max_attempts; attempt++ {
    c := candidates[attempt % len]
    if cooling(c) { continue }
    acquire(provider semaphore, queue timeout)
    resp, usage, err := upstream(c).Complete(ctx, req with model c.model)
    release
    log
    if err == nil { record affinity; return }
    if !retryable(err) { return err }
    mark failure / cooldown
}
return ErrAllFailed
```

- `retryable`：网络错误、context 超时（非客户端取消）、429、5xx、`need_responses`。
- 冷却：429 → `cooldown_seconds`；5xx/连接错误 → `max(5s, cooldown/2)`。
- `usage` 策略按 `calls` 升序；`rotate` 按组游标轮转；`order` 保持原序。
- 亲和：`affinity[session] = providerID`，在候选排序时把该 provider 提前；provider 失败后更新。
- 同一 provider 的多个 key 暂不支持（`providers.json` 一个 provider 一个 key）。

## 对外接口

### GET /api/gateway/status

```json
{
  "default": {"provider":"deepseek","model":"deepseek-chat"},
  "providers": [{"id":"deepseek","name":"DeepSeek","baseUrl":"...","models":[...],"keySet":true,"inflight":0,"calls":12,"failures":1,"cooldownUntil":null,"lastError":""}],
  "groups": [{"id":"daily","strategy":"order","members":[...]}],
  "affinity": {"<session>": "deepseek"}
}
```

### GET /v1/models

OpenAI 风格：

```json
{"object":"list","data":[{"id":"deepseek/deepseek-chat","object":"model","owned_by":"deepseek"},{"id":"group/daily","object":"model","owned_by":"gqy"}]}
```

### POST /v1/chat/completions

- 请求体按 OpenAI Chat 兼容解析：`model`、`messages`、`tools`、`tool_choice`、`temperature`、`stream`。
- 非流式：返回 `choices[0].message` + `usage`，附 `x_gqy_provider`。
- `stream:true`：返回 SSE。当前实现把最终文本按 16 rune 分块发送，最后发 `usage` 与 `[DONE]`；不是上游逐 token 转发，但客户端兼容。后续换成真正透传。
- 失败返回 OpenAI 风格错误：`{"error":{"message","type","code"}}`。
- 无鉴权，仅监听 `127.0.0.1`；外网部署必须加反代鉴权。

## 内部集成

- `agent.Loop.Upstream` 从 `*provider.Upstream` 改为接口：

```go
type Completer interface {
    Complete(context.Context, provider.Request) (provider.Message, *provider.Usage, error)
}
```

- `gateway.Router` 实现该接口；`httpapi.New` 用 router 构造 agent loop。
- `GQY_MOCK=1` 仍走 mock 分支，不经过网关。
- 旧的单上游行为由环境变量 provider 保留：没有 `providers.json` 时，`base_url/key/model` 自动成为一个 provider `env`。

## 安全与边界

- providers.json 0600；接口只返回 `keySet`。
- 请求日志不落正文；错误详情截断 2KB。
- 外部端点无鉴权，默认只绑定本机；文档明确公网必须反代。
- URL 只允许 http/https；禁止 file/其他 scheme。
- 上游超时沿用 `provider.Upstream`（响应头 30s、流 120s 无输出）。

## 验证

- 单元：模型解析、组策略、冷却、并发上限、重试次数。
- 集成：假上游 A 返回 500，B 返回成功 → 断言最终来自 B、日志记录两次 attempt。
- 外部端点：`/v1/chat/completions` 非流式与流式各一条。
- 冒烟：现有 `/api/chat` agent loop 仍通过。
