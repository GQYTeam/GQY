# config · 蓝图

## 1. 状态

部分（2026-11-02）：启动参数、解析与校验管线已建；文件监视与快照重载待建。

## 2. 对应设计决定

| 编号 | 一句话 |
| --- | --- |
| CF1 | 运行时只有一份不可变快照，全部读快照不读文件 |
| CF2 | 文件与环境变量分工：运行数据放文件，进程身份与密钥放环境变量 |
| CF3 | 热更新范围：三份配置文件热更新，环境变量重启生效 |
| CF4 | 重载管线：读 → 解析 → 校验 → 整体切换，失败保留旧快照 |
| CF5 | 原子切换：新请求新快照、在途请求旧快照 |
| CF6 | 文件监视：目录级 fsnotify + 事件合并，不盯单个文件 |
| CF7 | 手动重载：POST /api/config/reload，可选 SIGHUP |
| CF8 | 密钥只写不读：只返回 api_key_set |
| CF9 | 缺失文件用默认值；文件出现后热更新接管 |
| CF10 | fsnotify 是唯一新增依赖；启动与重载共用一套校验 |

## 3. 具体形状

### 配置清单三文件

| 文件 | 位置 | 字段表 |
| --- | --- | --- |
| providers.json | `$GQY_HOME/providers.json` | 见 `gateway.md`（providers / accounts 全字段） |
| routing.json | `$GQY_HOME/routing.json` | 见 `gateway.md`（default / groups / 上限） |
| settings.json | `$GQY_HOME/settings.json` | 见下，全字段展开 |

### settings.json 字段表（全）

| 字段 | 类型 | 必填 | 默认 | 校验 |
| --- | --- | --- | --- | --- |
| `watch_enabled` | bool | 否 | `true` | — |
| `rate_limit` | int | 否 | `0`（不限） | ≥0；负数整体校验失败 |
| `tool_timeout_s` | int | 否 | `120` | >0 |
| `stream_idle_timeout_s` | int | 默认 60，范围 5–600；流式连续无事件即中止 |
| `upstream_timeout_s` | int | 默认 120，范围 1–3600；每次上游调用整体超时 |
| `shell_timeout_s` | int | 否 | `60` | >0 |
| `shell_allowlist` | string[] | 否 | `[]` | 去空白、去重、丢空项 |

样例：

```json
{
  "watch_enabled": true,
  "rate_limit": 0,
  "tool_timeout_s": 120,
  "shell_timeout_s": 60,
  "shell_allowlist": []
}
```

### 启动参数表（Startup，对照 app/internal/config/env.go）

| Startup 字段 | 环境变量 | 默认 | 说明 |
| --- | --- | --- | --- |
| Home | `GQY_HOME` | `~/.gqy` | 数据根，所有路径基准 |
| Host | `HOST` | `127.0.0.1`（DefaultHost） | 回环默认（A3） |
| Port | `PORT` | `5173` | — |
| AuthToken | `GQY_AUTH_TOKEN` | `""` | 空 = 鉴权关闭 |
| Workspace | `GQY_WORKSPACE` | 启动目录 | shell 工作目录与相对路径基准 |
| ToolRoots | 合成 | workspace + `$GQY_HOME/sessions` + `GQY_TOOL_ROOTS` | 去重、去空 |
| Mock | `GQY_MOCK` | false | — |
| EnableExternal | `GQY_ENABLE_EXTERNAL` | false | — |
| APIBase / APIKey / APIUA / Model | `GQY_API_BASE` / `GQY_API_KEY` / `GQY_API_UA` / `GQY_MODEL` | deepseek 基线 | 环境变量 provider 兜底（CF9） |
| RateLimit | `GQY_RATE_LIMIT` | 无（nil） | 非空且 ≥0 时覆盖 settings 的 rate_limit |

`.env` 由 `LoadDotEnv` 读取：只填环境里不存在的变量，真实环境变量（含显式空值）优先。

### 不可变 Snapshot 结构（app/internal/config/types.go）

`Snapshot` 是运行时唯一事实来源（CF1），字段：

`Version`（重载次数）、`LoadedAt`（生成时间）、`Home`、`Host`、`Port`、`AuthToken`、`Workspace`、`ToolRoots`、`Mock`、`RateLimit`、`Providers []Provider`、`Routing`、`Settings`。

密钥视图（CF8 / A8）：

- `ProviderView`：id / display_name / base_url / protocol / default_model / models / max_concurrency / accounts。
- `AccountView`：id / name / `api_key_set`（bool）/ daily_token_limit / disabled；**永不含明文密钥**。
- `Snapshot.ProviderViews()` 负责从 `Snapshot` 生成该安全视图。

### 重载管线七步（CF4 / CF5 / CF6）

```
监视（fsnotify 目录级，$GQY_HOME）
  → 防抖合并（约 200ms 静默窗口，一批改动只触发一次）
  → 读文件（整读，防半写）
  → 解析（JSON）
  → 校验（id 唯一、白名单、上限、组环，复用 Build）
  → 构建新 Snapshot（Build：读+解析+校验+合成）
  → 原子替换指针，Version + 1
```

任何一步失败：保留旧快照，错误落日志、进状态接口（CF4）。

### 热更新 vs 重启范围表（CF3）

| 配置 | 生效方式 |
| --- | --- |
| providers.json | 热更新 |
| routing.json | 热更新 |
| settings.json（watch_enabled / rate_limit / tool_timeout_s / shell_timeout_s / shell_allowlist） | 热更新 |
| Host / Port / AuthToken / Workspace / ToolRoots / Mock / EnableExternal | 重启生效（Startup 只启动读一次） |
| 环境变量 provider（APIBase / APIKey / APIUA / Model） | 重启生效 |
| 环境变量密钥 `GQY_PROVIDER_<ID>_API_KEY` | 重启生效 |

## 4. 边角情况

- **校验失败保留旧快照**：providers / routing / settings 任一解析或校验失败，整份不切换（CF4）。
- **文件缺失用默认**（CF9）：providers.json 缺失或空 → `envProviders`（无 `GQY_API_KEY` 则为空列表）；routing.json 缺失 → 默认 `max_attempts=3`、`cooldown_seconds=30`、`queue_timeout_ms=1500`，default 取第一个 provider（env 优先）；settings.json 缺失 → `DefaultSettings()`。
- **.env 不覆盖真实环境变量**：`LoadDotEnv` 先 `LookupEnv`，已存在（含显式空值）即跳过。
- **密钥回退**：`api_key` 为空 → 读 `GQY_PROVIDER_<ID>_API_KEY`，ID 经 `EnvName` 转大写、非字母数字转 `_`；两者都空 → 该账号 `api_key_set=false`，不进候选。
- **编辑器原子保存**：目录级监视 + 防抖合并，写临时文件再 rename 不丢 watch、不读半写（CF6）。
- **上限越界**：`max_attempts>5` 整体校验失败；`cooldown_seconds>600` 整体校验失败（gateway.md 的字段表一致）。
- **Startup.RateLimit 覆盖 settings**：环境变量显式设置时优先于 settings.json（A4）。

## 5. 由哪些测试守着

计划中的测试：

1. `TestBuildSnapshot`：三文件合法 → `Build` 产出字段齐全、默认值填充正确的 `Snapshot`。
2. `TestBuildRejects`：坏 JSON、重复 provider id、组循环引用、越界上限 → `Build` 返回错误、无快照。
3. `TestMissingFiles`：三文件缺失 → env provider 兜底 + routing 默认 + `DefaultSettings`。
4. `TestLoadDotEnv`：`.env` 不覆盖已存在环境变量，显式空值优先。
5. `TestEnvNameAndKeyFallback`：`EnvName("my-provider")=="MY_PROVIDER"`；`api_key` 为空时读取 `GQY_PROVIDER_MY_PROVIDER_API_KEY`。
6. `TestProviderViews`：`ProviderViews()` 只含 `api_key_set`，无明文；重载（待建）失败时版本号不变、旧快照仍在。

## 6. 对应代码

- 已建：`app/internal/config/env.go`（Startup、LoadStartup、LoadDotEnv、EnvName）、`app/internal/config/types.go`（Provider / Account / Routing / Settings / Snapshot / ProviderView）、`app/internal/config/load.go`（Build 管线与校验，含组环检测）。
- 待建：快照重载（原子替换、版本号递增）、文件监视（fsnotify 目录监视 + 防抖合并）、手动重载端点 `POST /api/config/reload`（在 `httpapi`）。
