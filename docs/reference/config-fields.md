# config-fields · 配置字段手册

本页是 `blueprint/config.md`、`blueprint/gateway.md` 的对外快照。字段不一致时以 blueprint 为准。

## 1. 配置清单

| 文件 / 来源 | 位置 | 管什么 | 生效方式 |
| --- | --- | --- | --- |
| providers.json | `$GQY_HOME/providers.json` | 多 provider 多账号、密钥、并发、配额 | 热更新 |
| routing.json | `$GQY_HOME/routing.json` | 默认模型、路由组、策略、重试上限 | 热更新 |
| settings.json | `$GQY_HOME/settings.json` | 限流、工具超时、shell 白名单 | 热更新 |
| 环境变量 / `.env` | 进程环境 | 数据根、监听、鉴权、工具根、环境变量 provider 兜底 | 重启生效 |

- `$GQY_HOME` 默认 `~/.gqy`。
- 热更新管线：读 → 解析 → 校验 → 整体切换；任一步失败保留旧配置（12 CF4）。

## 2. providers.json

### 字段表

| 字段 | 类型 | 必填 | 默认 | 校验 |
| --- | --- | --- | --- | --- |
| `providers` | array | 是 | — | 可为空数组；空 + 无环境变量兜底 = 无可用 provider |
| `providers[].id` | string | 是 | — | 非空、全局唯一；重复或为空整体校验失败 |
| `providers[].display_name` | string | 否 | = `id` | 任意字符串 |
| `providers[].base_url` | string | 是 | — | 以 `http://` 或 `https://` 开头；其他 scheme 拒绝 |
| `providers[].protocol` | string | 否 | `openai-chat` | 白名单 `["openai-chat"]`；空值等价 |
| `providers[].default_model` | string | 否 | `""` | 空则该 provider 不参与裸模型名匹配（02 R6） |
| `providers[].models` | string[] | 否 | `[]` | 只用于 `/v1/models` 列表展示，不校验成员存在 |
| `providers[].user_agent` | string | 否 | `""` | 任意字符串；出站请求的 User-Agent 覆盖，空则按 `GQY_API_UA`，再空用默认 UA |
| `providers[].accounts` | array | 是 | — | 至少 1 个；为空整体校验失败 |
| `providers[].accounts[].id` | string | 是 | — | 非空、provider 内唯一 |
| `providers[].accounts[].api_key` | string | 否 | `""` | 空则回退 `GQY_PROVIDER_<ID>_API_KEY`；两者都空 → 该账号不可用 |
| `providers[].accounts[].daily_token_limit` | number | 否 | `0`（不限） | `>0` 时按 UTC 自然日窗口计；达上限该账号不可用（08 A6） |
| `providers[].accounts[].disabled` | bool | 否 | `false` | `true` 时该账号不进候选（保留配置不删密钥） |
| `providers[].max_concurrency` | number | 否 | `2` | 1–100；越界取边界值；作用于该 provider 的每个账号 |

- `user_agent` 尚未写入 `blueprint/gateway.md` 的字段表；如有出入，以 blueprint 为准。

### 完整样例

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
      "user_agent": "GQY/1.0",
      "accounts": [
        {"id": "a1", "api_key": "sk-…", "daily_token_limit": 20000000, "disabled": false},
        {"id": "a2", "api_key": "", "disabled": false}
      ],
      "max_concurrency": 2
    }
  ]
}
```

## 3. routing.json

### 字段表

| 字段 | 类型 | 必填 | 默认 | 校验 |
| --- | --- | --- | --- | --- |
| `default` | object | 否 | `null` | 为 null 且其他规则未命中 → 报「模型不存在」 |
| `default.provider` | string | 是（`default` 存在时） | — | 必须等于某 provider `id` |
| `default.model` | string | 否 | 该 provider `default_model` | 为空取 provider 默认模型 |
| `groups` | array | 否 | `[]` | 可为空 |
| `groups[].id` | string | 是 | — | 非空、全局唯一；重复报错 |
| `groups[].name` | string | 否 | = `id` | 展示用 |
| `groups[].strategy` | string | 否 | `order` | 白名单 `order` / `rotate` / `usage` |
| `groups[].members` | string[] | 是 | — | 至少 1 个；每项是 `provider/model`、`group/id` 或裸模型名；引用不存在或嵌套成环 → 整体校验失败 |
| `max_attempts` | number | 否 | `3` | 1–5；超上限取 5，小于 1 取 1 |
| `cooldown_seconds` | number | 否 | `30` | 0–600；超上限取 600 |
| `queue_timeout_ms` | number | 否 | `1500` | ≥1；队列等待上限，超时明确报错 |

### 完整样例

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

## 4. settings.json

### 字段表

| 字段 | 类型 | 必填 | 默认 | 校验 |
| --- | --- | --- | --- | --- |
| `watch_enabled` | bool | 否 | `true` | — |
| `rate_limit` | int | 否 | `0`（不限） | ≥0；负数整体校验失败 |
| `tool_timeout_s` | int | 否 | `120` | >0 |
| `stream_idle_timeout_s` | int | 否 | 60 | 5–600；流式连续无新事件即中止 |
| `upstream_timeout_s` | int | 否 | 120 | 1–3600；每次上游调用的整体超时 |
| `shell_timeout_s` | int | 否 | `60` | >0 |
| `shell_allowlist` | string[] | 否 | `[]` | 去空白、去重、丢空项 |

### 完整样例

```json
{
  "watch_enabled": true,
  "rate_limit": 0,
  "tool_timeout_s": 120,
  "shell_timeout_s": 60,
  "shell_allowlist": []
}
```

## 5. 环境变量

| 环境变量 | 类型 | 默认 | 生效时机 | 说明 |
| --- | --- | --- | --- | --- |
| `GQY_HOME` | string | `~/.gqy` | 启动 | 数据根，所有路径基准 |
| `GQY_WORKSPACE` | string | 启动目录 | 启动 | shell 工作目录与相对路径基准 |
| `GQY_AUTH_TOKEN` | string | `""` | 启动 | 空 = 鉴权关闭，仅回环放行（08 A3） |
| `GQY_API_BASE` | string | `https://api.deepseek.com/v1` | 启动 | 环境变量 provider 兜底的 base_url |
| `GQY_API_KEY` | string | `""` | 启动 | 环境变量 provider 兜底密钥 |
| `GQY_API_UA` | string | `""` | 启动 | 环境变量 provider 的 User-Agent |
| `GQY_MODEL` | string | `deepseek-chat` | 启动 | 环境变量 provider 默认模型 |
| `GQY_RATE_LIMIT` | int | 无（nil） | 启动 | 非空且 ≥0 时覆盖 `settings.json` 的 `rate_limit` |
| `GQY_ENABLE_EXTERNAL` | bool | `false` | 启动 | 外部工具注入开关（06 T8） |
| `GQY_TOOL_ROOTS` | string（冒号 / 逗号分隔） | 无 | 启动 | 追加允许根；与 workspace、`$GQY_HOME/sessions` 合成 ToolRoots，去重去空 |
| `GQY_PROVIDER_<ID>_API_KEY` | string | `""` | 启动 | provider 级密钥兜底；`<ID>` 为 provider id 转大写、非字母数字转 `_` |
| `HOST` | string | `127.0.0.1` | 启动 | 监听地址（回环默认，08 A3） |
| `PORT` | string | `5173` | 启动 | 监听端口 |

- 环境变量全部启动时读一次，改动需重启（12 CF2 / CF3）。
- `.env` 只填环境里不存在的变量；真实环境变量（含显式空值）优先。
- 其余启动参数（如 `GQY_MOCK`）见 `blueprint/config.md` 启动参数表。

## 6. 密钥只写不读与 api_key_set

- `providers.json` 的 `api_key` 可写、可热更新；快照里存明文仅供出站请求使用。
- `api_key` 为空时回退 `GQY_PROVIDER_<ID>_API_KEY`（12 CF9 边角）；两者都空 → 该账号 `api_key_set=false`，不进候选。
- 任何读接口（`GET /api/gateway/status`、`GET /api/config`、重载返回、状态接口）只返回 `api_key_set`（bool），永不含明文（12 CF8、08 A8）。
- 日志与重载错误不打印 `api_key` 值。
