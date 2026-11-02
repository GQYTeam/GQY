# gateway · 蓝图

## 1. 状态

待施工（2026-11-02）。

## 2. 对应设计决定

| 编号 | 一句话 |
| --- | --- |
| R1 | 候选列表：解析模型 → 展开到账号 → 冷却排后 → 组内按策略；只有响应开始前才切换候选 |
| R2 | 路由策略只做三种：order / rotate / usage；组可嵌套，成员是 `provider/model` 或 `group/id` |
| R3 | 每账号信号量 + 有界队列；429/5xx/连接错误/超时冷却，4xx（除 429）不重试；重试上限 `max_attempts` |
| R4 | 会话亲和：同会话固定 provider/账号，内存态，响应头 `X-GQY-Provider` |
| R5 | 多账号与配额：账号级失败转移优先于 provider 级；日 token 配额内存态；密钥只写不读 |
| R6 | 模型解析四级：`group/<id>` → `<provider>/<model>` → 裸模型名 → 未命中报错 |
| R7 | 可观测：`requests.jsonl` 一行一请求（不落正文）+ `/api/gateway/status` |
| R8 | 端点：`/v1/chat/completions`、`/v1/models`；`/api/chat` 走同一网关，不旁路 |

另兑现：`08` A6/A7（配额窗口与候选剔除）、`12` CF4/CF9（配置整体校验、文件缺失默认值）。

## 3. 具体形状

### providers.json 字段表

顶层一个对象，一个键 `providers`。

| 字段 | 类型 | 必填 | 默认 | 上限 | 校验规则 |
| --- | --- | --- | --- | --- | --- |
| `providers` | array | 是 | — | — | 可为空数组；空 + 无环境变量兜底 = 无可用 provider |
| `providers[].id` | string | 是 | — | — | 非空、全局唯一；重复或为空整体校验失败 |
| `providers[].display_name` | string | 否 | = `id` | — | 任意字符串 |
| `providers[].base_url` | string | 是 | — | — | 以 `http://` 或 `https://` 开头；其他 scheme 拒绝 |
| `providers[].protocol` | string | 否 | `openai-chat` | — | 白名单 `["openai-chat"]`，空值等价；Anthropic / Gemini 待 `03` 后加入 |
| `providers[].default_model` | string | 否 | `""` | — | 空则该 provider 不参与裸模型名匹配（R6） |
| `providers[].models` | string[] | 否 | `[]` | — | 只用于 `/v1/models` 列表展示，不校验成员存在 |
| `providers[].accounts` | array | 是 | — | 至少 1 个 | 为空整体校验失败 |
| `providers[].accounts[].id` | string | 是 | — | — | 非空、provider 内唯一 |
| `providers[].accounts[].api_key` | string | 否 | `""` | — | 空则回退环境变量 `GQY_PROVIDER_<ID>_API_KEY`（ID=provider id，转大写、非字母数字转 `_`）；env 只能给整个 provider 一个兜底密钥，多账号不同密钥必须写进文件 |
| `providers[].accounts[].daily_token_limit` | number | 否 | `0`（不限） | — | `>0` 时按 UTC 自然日窗口计（A6），窗口内累计 token 达上限即不可用 |
| `providers[].max_concurrency` | number | 否 | `2` | 1–100 | 每账号并发信号量上限（R3）；越界取边界值 |

样例：

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
      "accounts": [
        {"id": "a1", "api_key": "sk-…", "daily_token_limit": 20000000},
        {"id": "a2", "api_key": ""}
      ],
      "max_concurrency": 2
    }
  ]
}
```

文件缺失或为空：只用环境变量 provider 兜底（`GQY_API_BASE` / `GQY_API_KEY` / `GQY_MODEL` 合成一个 `env` provider）；文件首次出现后由热更新接管（12 CF9）。

### routing.json 字段表

| 字段 | 类型 | 必填 | 默认 | 上限 | 校验规则 |
| --- | --- | --- | --- | --- | --- |
| `default` | object | 否 | `null` | — | 见下行；为 null 且未命中其他规则 → 报模型不存在 |
| `default.provider` | string | 是（default 存在时） | — | — | 必须等于某 provider `id` |
| `default.model` | string | 否 | 该 provider `default_model` | — | 为空取 provider 默认模型 |
| `groups` | array | 否 | `[]` | — | 可为空 |
| `groups[].id` | string | 是 | — | — | 非空、全局唯一；重复注册报错 |
| `groups[].name` | string | 否 | = `id` | — | 展示用 |
| `groups[].strategy` | string | 否 | `order` | — | 白名单 `order` / `rotate` / `usage` |
| `groups[].members` | string[] | 是 | — | 至少 1 个 | 每项是 `provider/model`、`group/id` 或裸模型名；引用不存在的 provider / group、或嵌套成环 → 整体校验失败 |
| `max_attempts` | number | 否 | `3` | 1–5 | 超上限取 5，小于 1 取 1 |
| `cooldown_seconds` | number | 否 | `30` | 0–600 | 超上限取 600 |
| `queue_timeout_ms` | number | 否 | `1500` | ≥1 | 队列等待上限，超时明确报错不无限排队 |

样例：

```json
{
  "default": {"provider": "deepseek", "model": "deepseek-chat"},
  "groups": [
    {"id": "daily", "name": "日常", "strategy": "order",
     "members": ["deepseek/deepseek-chat", "moonshot/kimi-k2"]}
  ],
  "max_attempts": 3,
  "cooldown_seconds": 30,
  "queue_timeout_ms": 1500
}
```

### 模型解析判定表（R6）

| 输入 | 判定 | 结果 |
| --- | --- | --- |
| `group/<id>` | `id` 命中 `groups` | 该组按策略展开候选；组是边界，fallback 不出组 |
| `<provider>/<model>` | provider 存在 | 该 provider 全部账号为候选；不跨 provider 转移（无可用账号且配置 fallback 时除外） |
| 裸模型名 | 与某 provider `default_model` 相等 | 命中多个时按 providers 配置顺序，全部为候选 |
| 空模型 / 以上未命中 | 看 `default` | 用 `default.provider` / `default.model`；仍无 → 返回明确「模型不存在」错误，不猜 |

### 候选展开与排序（R1 / R2 / R3 / A7）

1. **解析**：按上表得到候选来源（组 / 单 provider / 多 provider / default）。
2. **展开到账号**：每个 provider 的每个账号（配置顺序）是一个候选；组递归展开成员（`provider/model` → 该 provider 账号；`group/id` → 递归）。
3. **剔除**：配额用尽的账号直接剔除（A7），不进候选；冷却中的候选保留但排最后。
4. **排序**：组内按策略——`order` 保持配置顺序；`rotate` 按组游标轮转起点；`usage` 按最近调用数升序（带半衰期）。
5. **重试循环**：`attempt` 从 0 到 `max_attempts-1`，按序取候选；每账号先过信号量，满则进有界队列等 `queue_timeout_ms`。
6. **切换边界**：只有还没向客户端写出任何字节时才换下一个候选；已开始流式输出则错误直接交还客户端（R1）。
7. **结果**：成功 → 记亲和、写日志；429/5xx/连接错误/超时 → 该账号进冷却、计数失败、换下一个；4xx（除 429）→ 不重试、不冷却，直接返回；候选耗尽 → `no_candidate`。

### requests.jsonl 字段表（R7）

每行一个请求，JSONL 追加写；不落 prompt / 回复正文。

| 字段 | 类型 | 含义 | 取值域 |
| --- | --- | --- | --- |
| `ts` | string | 请求时间（RFC3339） | 时间戳 |
| `request_id` | string | 请求标识 | 每请求唯一 |
| `session` | string | 会话 id | 无则空串 |
| `provider` | string | 最终回答的 provider | provider `id`；未路由则为空 |
| `model` | string | 最终使用的模型 | 模型名 |
| `account` | string | 最终账号 | 账号 `id`；未路由则为空 |
| `status` | string | 结果 | `ok` / `error` / `cooldown` / `no_candidate` / `rate_limited` |
| `http_status` | number | 返回给客户端的状态码 | HTTP 状态码 |
| `duration_ms` | number | 网关侧总耗时 | ≥0 |
| `attempt` | number | 第几次尝试 | 1..`max_attempts` |
| `error_code` | string | 错误码 | 无则空串；不落正文与密钥 |

### GET /api/gateway/status 样例（健康 / 冷却 / 并发 / 配额）

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
  "groups": [{"id": "daily", "strategy": "order", "cursor": 0,
              "members": ["deepseek/deepseek-chat", "moonshot/kimi-k2"]}]
}
```

- `key_set` 只有 true，永不含明文密钥（CF8 / A8）。
- `cooldown_remaining_s` 为 0 表示不在冷却；`quota_exhausted` 为 true 表示该账号今天不可用。
- `cursor` 是 `rotate` 组当前指针（`order` / `usage` 下恒为 0 无意义）。

### 端点表（R8）

| 方法 + 路径 | 用途 | 请求体 | 成功响应 | 错误 |
| --- | --- | --- | --- | --- |
| `POST /v1/chat/completions` | OpenAI 兼容对话 | `model`（可空用 default）、`messages`（必填）、`stream`（可选 bool）、`tools`（可选）、`reasoning_effort`（可选） | 非流式 `choices[0].message` + `usage`；流式 SSE，响应头 `X-GQY-Provider` | OpenAI 错误形状 `{"error":{"message","type","code"}}` |
| `GET /v1/models` | 模型列表 | 无 | `{"object":"list","data":[…]}`，条目含 `provider/model` 与 `group/id` | — |
| `POST /api/chat` | 内部对话（走同一网关） | 同 agent 请求 | SSE 事件流 | `{"error","message"}` |

## 4. 边角情况

- **全部候选冷却**：该请求返回 `no_candidate`（R3），日志 `status=no_candidate`。
- **组嵌套**：递归展开；引用不存在的组 / provider、或成员嵌套成环 → 加载与热更新校验失败，保留旧配置（12 CF4）。
- **裸模型名命中多个 provider**：按 providers 配置顺序全部为候选，不再跨 provider 二次挑选。
- **流式已开始后失败**：不切换候选，错误直接交还客户端；日志记该候选与错误码（R1）。
- **队列超时**：候选全部并发满、等待超过 `queue_timeout_ms` → 明确超时错误，不无限排队（R3）。
- **4xx（除 429）**：不重试、不冷却，原样返回；换账号没有意义（R3）。
- **账号配额耗尽**：展开候选时直接剔除（A7）；该 provider 全部账号耗尽才轮到组内下一个成员或 `no_candidate`。
- **`providers.json` 缺失**：只用环境变量 provider 兜底；文件出现后热更新接管（CF9）。
- **密钥读取**：`api_key` 为空 → 回退 `GQY_PROVIDER_<ID>_API_KEY`；两者都空 → 该账号视为无密钥不可用（不进候选）。

## 5. 由哪些测试守着

计划中的测试（现在还没有代码）：

1. `TestModelResolve`：裸模型名、`provider/model`、`group/id` 三种解析各一用例；未命中报模型不存在。
2. `TestFallbackOn429`：首个账号 429 → 冷却 → 转到第二个账号；日志记录两次 attempt。
3. `TestNoSwitchAfterStreamStart`：流式已开始后上游失败 → 不换候选，错误给客户端。
4. `TestRotateUsage`：`rotate` 连续三次落在不同成员；`usage` 优先最闲成员。
5. `TestNoRetryOn4xx`：4xx 不重试、不冷却；`max_attempts` 用尽 → `no_candidate`。
6. `TestAffinityHeader`：同会话两次请求，`X-GQY-Provider` 相同。
7. `TestRequestLogFields`：`requests.jsonl` 每行字段完整、取值域合法、无正文。
8. `TestQuotaExhausted`：账号日配额用尽 → 不进候选；全部用尽 → `no_candidate`（08 A7）。

## 6. 对应代码

待建：`app/internal/gateway/`（旧实现同路径在 git 历史 `HEAD~2`，仅作事实参考，不照抄）。
