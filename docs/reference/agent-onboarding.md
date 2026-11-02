# 接入 GQY 网关（给非 GQY 的 agent）

GQY 网关可以当**统一上游**用：任何 agent 只要把 base_url 指过来，就自动获得协议翻译、重试与失败转移、租户配额、请求日志和密钥治理。

## 1. 选协议

| 你的 agent 讲什么 | base_url | 说明 |
| --- | --- | --- |
| OpenAI Chat Completions | `http://127.0.0.1:5173/v1` | 最常见；`/v1/models` 可列模型 |
| Anthropic Messages | `http://127.0.0.1:5173` | 请求打到 `/v1/messages` |
| Gemini generateContent | `http://127.0.0.1:5173` | 请求打到 `/v1beta/models/<model>:generateContent` |

`model` 字段用 GQY 的模型引用：`<provider>/<model>`（单账号直连）、`env/<model>`、或路由组名。

## 2. 带上租户

给每个任务/工作区一个租户名，并在请求头里带：

```http
X-GQY-Tenant: drawing
```

（也接受 `X-GQY-Task` 作为别名。）租户决定你的并发池与每分钟配额，互不干扰。租户名规则：字母、数字、`_`、`.`、`-`，1–64 字符。

管理端在 `routing.json` 登记：

```json
{"tenants": [
  {"name": "drawing", "max_concurrency": 8, "req_per_min": 600},
  {"name": "coding",  "max_concurrency": 4, "req_per_min": 300}
]}
```

未登记的租户名会被 400 拒绝（只要配置里出现过 `tenants`）。

## 3. 鉴权

- 本机访问且未设 `GQY_AUTH_TOKEN`：直接可用。
- 设了令牌：`Authorization: Bearer <token>` 或浏览器 cookie（Web UI）。
- 非本机访问必须设令牌，否则 403。

## 3.5 全局默认模型

`model` 传空时走「全局默认模型」：管理端用 `PUT /api/model/active {"model":"p/m2","persist":true}` 设一次，所有未指定 model 的请求（含非 GQY 的 agent）都走它。显式传 model 仍优先。

## 4. 错误码（统一）

| code | HTTP | 含义 | 建议动作 |
| --- | --- | --- | --- |
| `tenant_quota` | 429 | 租户每分钟请求超限 | 按 `Retry-After` 退避 |
| `tenant_queue_timeout` | 503 | 租户并发排队超时 | 降并发或加大 `max_concurrency` |
| `tenant_unknown` | 400 | 租户未登记 | 在 `routing.json` 登记或去掉头 |
| `invalid_tenant` | 400 | 租户名非法 | 修字符集 |
| `queue_timeout` | 503 | 账号池排队超时 | 退避重试 |
| `upstream_timeout` | 504 | 上游超时（整体或流式空闲） | 重试（可转移） |
| `no_candidate` | 503 | 候选耗尽 | 检查冷却/配额/密钥 |
| `rate_limited` | 429 | 客户端限流 | 按 `Retry-After` |

## 5. 观测

- `GET /api/gateway/status`：租户并发/排队/配额与账号健康。
- `GET /v1/models`：可用模型与能力。
- `$GQY_HOME/gateway/requests.jsonl`：每请求一行（含 `tenant`、状态、耗时、错误码），不含正文与密钥。

## 6. 最小示例

```bash
curl -s http://127.0.0.1:5173/v1/chat/completions \
  -H 'Content-Type: application/json' \
  -H 'X-GQY-Tenant: drawing' \
  -d '{"model":"env/deepseek-chat","messages":[{"role":"user","content":"hi"}]}'
```
