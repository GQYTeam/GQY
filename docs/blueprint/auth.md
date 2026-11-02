# auth · 蓝图

## 1. 状态

已实现（2026-11-02）：令牌、cookie、限流、配额与状态接口；另有统一安全响应头。

## 2. 对应设计决定

| 编号 | 一句话 |
| --- | --- |
| A1 | 访问令牌默认关，开启后覆盖 /api、/v1、/v1beta（/api/auth 与静态文件豁免） |
| A2 | 携带方式 Bearer / cookie，常量时间比较；cookie 走 HttpOnly + SameSite=Lax |
| A3 | 未设置令牌只在回环监听时放行本机；非回环无令牌的安全边界归 11 |
| A4 | 全局限流：每客户端每分钟固定窗口；限流在鉴权外层，防暴力猜令牌 |
| A5 | 两层分工：入口限流 vs R3 账号并发 / 冷却 |
| A6 | 账号级日 token 配额：内存态，UTC 自然日窗口，超限不可用直到重置 |
| A7 | 配额与候选交互：配额用尽不进候选，全不可用 no_candidate |
| A8 | 密钥只写不读：只返回 api_key_set |
| A9 | 可观测：状态接口暴露 auth_enabled、rate_limit、每账号 token 用量 |

## 3. 具体形状

### GQY_AUTH_TOKEN 判定流程

```
AuthToken == "" ?
  ├─ 是 → 鉴权关闭。服务只在回环地址监听（默认 Host=127.0.0.1）；
  │       绑定非回环地址且未设令牌是配置错误，启动时强警告（拦截细节归 11）。
  └─ 否 → 鉴权开启，/api、/v1、/v1beta 全校验。
```

豁免清单：

| 路径 | 是否校验 |
| --- | --- |
| `/api/auth` | 不校验（登录换 cookie 的唯一入口） |
| 静态文件与其他路径 | 不校验（不在三组保护前缀内） |
| `/api/*`、`/v1/*`、`/v1beta/*`（除 `/api/auth`） | 校验 |
| `/metrics` | 校验（M1：设置令牌后要求 Bearer/cookie；未设置时只允许本机；不计入口限流） |

### Bearer 与 cookie 形状

| 方式 | 形状 | 说明 |
| --- | --- | --- |
| Bearer | `Authorization: Bearer <token>` | 外部客户端与 OpenAI 兼容调用方首选 |
| cookie | `gqy_token=<token>` | `HttpOnly`、`SameSite=Lax`、`Path=/`、`MaxAge=7d`；TLS 或 `X-Forwarded-Proto: https` 时加 `Secure` |

- 比较用常量时间比较，不因前缀命中提前返回。
- 旧实现另有 `X-GQY-Token` 请求头，蓝图不保留（A2 只定两种携带方式）。

### 安全响应头

所有响应（静态、API、SSE）统一加：

| 头 | 值 |
| --- | --- |
| `Content-Security-Policy` | `default-src 'self'; img-src 'self' data: blob:; style-src 'self' 'unsafe-inline'; script-src 'self'; connect-src 'self'; font-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'` |
| `X-Content-Type-Options` | `nosniff` |
| `X-Frame-Options` | `DENY` |
| `Referrer-Policy` | `no-referrer` |
| `Permissions-Policy` | `camera=(), microphone=(), geolocation=()` |

`style-src 'unsafe-inline'` 是因为界面用少量内联 style 画主题预览；脚本没有内联，`script-src 'self'` 保持严格。

### 全局限流窗口与 429 响应体

- 窗口：按客户端 IP（`RemoteAddr` 的 IP，不信任 `X-Forwarded-For`）做每分钟固定窗口计数；桶只在内存，超过 4096 个旧桶清理上一分钟。
- 位置：在鉴权外层执行——未认证请求同样计数。
- `rate_limit` 取 settings.json 的 `rate_limit`；环境变量 `GQY_RATE_LIMIT` 非空时覆盖（config.go `Startup.RateLimit`）。0 = 不限。

429 响应体按协议区分：

| 前缀 | 响应体 |
| --- | --- |
| `/v1/` | `{"error":{"message":"请求过于频繁","type":"gqy_error","code":"rate_limited"}}` |
| `/v1beta/` | Gemini 错误形状，同 code |
| `/api/` | `{"error":"rate_limited","message":"请求过于频繁，请稍后再试"}` |

统一带 `Retry-After: 60`。

### 账号级日配额的状态字段

- 窗口：UTC 自然日，`00:00 UTC` 重置；用量从上游返回的 usage 累加，内存态，重启清零（A6）。
- 超限 = 该账号不可用：候选展开时直接剔除（A7），直到窗口重置。

### GET /api/gateway/status 样例（鉴权 / 限流 / 配额）

```json
{
  "auth_enabled": true,
  "rate_limit": 60,
  "default": {"provider": "deepseek", "model": "deepseek-chat"},
  "providers": [
    {
      "id": "deepseek",
      "accounts": [
        {
          "id": "a1",
          "inflight": 1,
          "max_concurrency": 2,
          "calls": 12,
          "failures": 1,
          "cooldown_remaining_s": 0,
          "token_used_today": 3100000,
          "token_limit": 20000000,
          "quota_exhausted": false,
          "key_set": true
        }
      ]
    }
  ]
}
```

- `key_set` 只有 true，永无明文（A8）。
- `token_limit` 为 0 表示不限；`quota_exhausted` 为 true 表示该账号今天不进候选。

## 4. 边角情况

- **令牌比较**：常量时间，Bearer 前缀不匹配也不能提前返回。
- **限流在鉴权外**：未带令牌的请求先被限流计数，防止暴力猜令牌。
- **配额耗尽**：账号不进候选（A7）；该 provider 全部耗尽才轮到组内下一成员或 `no_candidate`。
- **重启清零**：日配额与限流桶都是内存态，重启即清零（G6）。
- **cookie Secure**：TLS 直连或反向代理给 `X-Forwarded-Proto: https` 时加 `Secure`，防止令牌走明文回源后继续明文发送。
- **未设令牌 + 非回环**：启动强警告；是否拒绝启动归 `11-安全与沙盒.md` 定。
- **rate_limit 非法值**：settings.json 负数整体校验失败（保留旧快照）；`GQY_RATE_LIMIT` 非法或负数忽略，回退 settings。

## 5. 由哪些测试守着

计划中的测试（现在还没有代码）：

1. `TestAuthRequired`：设令牌后无凭据访问 `/v1/chat/completions` → 401，OpenAI 错误形状。
2. `TestAuthBearerAndCookie`：Bearer 通过、错误令牌 401；登录后 cookie 直通 `/api/chat`。
3. `TestAuthLoginEndpoint`：`/api/auth` 无需令牌；错误令牌不种 cookie；cookie 带 HttpOnly / SameSite。
4. `TestLoopbackOnly`：未设令牌且监听 127.0.0.1 时放行本机请求。
5. `TestRateLimit`：`rate_limit=5` 时第 6 个请求 429 + `Retry-After`；未认证请求同样被计数。
6. `TestQuotaExhaustedCandidate`：账号配额用尽后不进候选、转到下一账号；全部用尽 → `no_candidate`。
7. `TestStatusFields`：状态接口含 `auth_enabled` / `rate_limit` / 每账号 `token_used_today` / `token_limit` / `quota_exhausted`；无 `api_key` 明文。

## 6. 对应代码

待建：`app/internal/httpapi/`（旧实现同路径 `auth.go`、`ratelimit.go`、`server.go` 在 git 历史 `HEAD~2`，仅作事实参考）。
