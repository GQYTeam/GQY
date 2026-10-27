# 插件运行时设计

依据：`docs/AboutPlugin.md` 的统一模型、生命周期、进程回收；`docs/research/MagpieGateway.md` 的 manifest / registry / 服务依赖 / 插件工具。

目标：把“工具”从编译期内置扩展到外部插件，同时不给核心引入第三方依赖。

## 范围

**做：**

- manifest 发现：`$GQY_HOME/plugins/<id>/manifest.json`。
- 两种适配器：
  - `gqy-jsonrpc-v1`：原生 stdio JSON-RPC，manifest 声明 actions。
  - `mcp`：MCP stdio（initialize → tools/list → tools/call），启动时发现工具。
- 运行时：注册表、实例表、单插件状态机、调用超时、并发模型、关闭分级、进程组回收。
- 插件动作注册成 `tool.Tool`（外部工具），随 `GQY_ENABLE_EXTERNAL=1` 注入模型；也可通过 HTTP 单独调用。
- 插件管理接口与前端扩展页。
- 契约测试：加载、调用、关闭、重复 id、超时、进程退出。

**不做（后置）：**

- 动态加载 Go `.so` / WASM。
- 远程 gRPC / Unix Socket 适配器。
- 插件服务依赖（Requires/Provides）与热重配。
- 权限系统；安装插件 = 信任它。
- 插件市场。

## Manifest

```json
{
  "id": "echo",
  "name": "Echo 示例",
  "version": "1.0.0",
  "description": "回显参数，用于验证插件链路",
  "protocol": "gqy-jsonrpc-v1",
  "command": ["node", "index.js"],
  "cwd": ".",
  "env": {"FOO": "bar"},
  "timeout_ms": 10000,
  "concurrency": "concurrent",
  "actions": [
    {
      "name": "echo",
      "description": "回显 text 参数",
      "schema": {"type": "object", "properties": {"text": {"type": "string"}}, "required": ["text"]}
    }
  ]
}
```

- `id`：`^[a-z0-9][a-z0-9_-]{0,31}$`，全局唯一。
- `protocol`：`gqy-jsonrpc-v1` / `mcp`。
- `command`：数组，不经过 shell；相对路径以 manifest 目录为基准。
- `cwd`：相对 manifest 目录；默认 manifest 目录。
- `env`：在宿主环境上追加；不覆盖 `PATH` 等关键变量。
- `timeout_ms`：单次调用超时，默认 10000，上限 120000。
- `concurrency`：`concurrent`（默认）或 `exclusive`（运行时加互斥）。
- `actions`：仅原生协议使用；MCP 走 `tools/list`。

## 原生协议

换行分隔 JSON-RPC 2.0：

```json
{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocol":"gqy-jsonrpc-v1"}}
{"jsonrpc":"2.0","id":1,"result":{"ok":true,"name":"echo","version":"1.0.0"}}
{"jsonrpc":"2.0","id":2,"method":"call","params":{"action":"echo","arguments":{"text":"hi"}}}
{"jsonrpc":"2.0","id":2,"result":{"content":"hi"}}
{"jsonrpc":"2.0","id":3,"method":"shutdown"}
```

- 加载成功 = 进程启动 + `initialize` 成功。
- 调用结果取 `result.content`；错误取 `error.message`。
- `shutdown` 后进程应退出；宿主最多等 5s，再 SIGTERM 进程组，再 2s 后 SIGKILL。
- 插件必须处理 stdin EOF：读到 EOF 自行退出（最后一道孤儿防线）。

## MCP 协议

按 MCP stdio 最小集实现：

1. `initialize`（protocolVersion `2024-11-05`，capabilities `{}`，clientInfo）。
2. `notifications/initialized`。
3. `tools/list` → `result.tools[]`（`name` / `description` / `inputSchema`）。
4. `tools/call`（`name` / `arguments`）→ `result.content[]`；文本拼接，`isError` 视为错误。
5. `shutdown` + 关闭 stdin。

MCP 动作名保留原始 `name`；注册成工具时加 `pluginID__` 前缀并做白名单替换。

## 运行时

```go
type Adapter interface {
    ID() string
    Match(Manifest) bool
    Load(ctx, Manifest, Host) (Instance, error)
}
type Instance interface {
    Call(ctx, action string, args map[string]any) (string, error)
    Close(ctx) error
    Info() InstanceInfo
}
type Manager struct {
    mu sync.RWMutex
    manifests map[string]Manifest
    instances map[string]*managed
    adapters []Adapter
    host Host
    registry *tool.Registry
}
```

### 状态机

`discovered → loading → ready → closing → closed`，失败为 `failed`。

- 调用只在 `ready` 放行；其他状态返回 `ErrNotReady`。
- `Load` 幂等：已 ready 直接返回；`loading` 返回“加载中”。
- `Unload` 幂等：未加载返回 `ErrNotFound`，不 panic。
- 加载失败回滚：摘除实例、关进程、释放信号量。
- 进程退出由等待 goroutine 捕获：状态置 `failed`，在途调用返回 `ErrExited`，不无限重启。

### 并发

- 实例表读写锁：加载/卸载写锁，查表读锁。
- 单插件：`sync.Mutex` + 状态机；`exclusive` 插件调用串行，`concurrent` 插件可并发。
- 原生 stdio 用“请求 id → pending channel”多路复用；MCP 同一套传输。
- 每次调用带 `context.WithTimeout`；客户端取消传播到进程（原生协议发 `cancel` 可选，当前直接让调用返回，后续可加）。

### 进程管理

- Unix：`SysProcAttr{Setpgid: true}`；关闭时对 `-pgid` 发 SIGTERM，超时 SIGKILL。
- Windows：先用 `taskkill /T /F` 兜底，不做 Job Object（文档标注）。
- 标准输入作为生命线；关闭先关 stdin。
- stderr 写入 `$GQY_HOME/plugins/<id>.log`（截断到 1MB 轮转）。
- 销毁临时目录；不残留 socket。

### 指标

`/api/plugins` 返回每个插件的状态、pid、启动时间、调用次数、失败次数、最近错误。日志打点：启动、退出码、强杀、重启（当前不做自动重启）。

## 工具注册

- 原生 action / MCP tool → `pluginTool{id, action, desc, schema}`，实现 `tool.Tool`，`Kind()` 返回 `KindExternal`。
- 工具名：`<pluginID>__<action，非字母数字转 _>`，最长 64。
- 加载成功注册；卸载注销。注册冲突返回错误并回滚加载。
- `agent.Loop.ToolsSchema()` 已按 `KindExternal` 过滤，因此插件工具默认不注入；`GQY_ENABLE_EXTERNAL=1` 才可见。

## HTTP

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| `GET` | `/api/plugins` | 发现 + 状态 + actions + 调用统计 |
| `POST` | `/api/plugins/{id}/load` | 加载 |
| `POST` | `/api/plugins/{id}/unload` | 卸载 |
| `POST` | `/api/plugins/{id}/call` | `{action, arguments}`，调试/前端用 |
| `POST` | `/api/plugins/reload` | 重新扫描 manifest |

所有接口只支持本机；调用返回 `{ok, content, durationMs}`，错误带 `{error, message}`。

## 测试

- 重复 manifest id 报错。
- 未知 protocol 不匹配任何适配器。
- 加载 → 调用 → 卸载 → 再卸载返回 not found。
- 超时：测试插件 sleep 超过 timeout，调用返回超时，进程被清理。
- 进程退出：测试插件主动 exit，在途/后续调用返回 not ready。
- 并发：10 个并发调用不串包，id 一一对应。
- 孤儿：父进程测试进程退出后，插件读到 stdin EOF 退出（契约测试）。

## 目录

```
$GQY_HOME/plugins/
  echo/
    manifest.json
    index.js
  <id>.log
```

示例插件放仓库 `app/plugins/echo/`，安装方式是复制到 `$GQY_HOME/plugins/`；也可以在设置页一键“安装示例”。
