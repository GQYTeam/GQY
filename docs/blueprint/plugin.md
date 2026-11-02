# plugin.md · 插件运行时蓝图

## 1. 状态

已实现（2026-11-02）：四层运行时、三种适配器、生命周期、并发、超时、回收、设置 schema、安装/删除。

## 2. 对应设计决定

兑现 `04-插件运行时.md` 的 P1–P12：

- P1 统一调用模型：业务只认「调用(插件ID, 动作, 参数)」，四层边界，社区专有类型只在适配器层。
- P2 注册中心：显式指定 → 社区名匹配 → 探测，三级选择，歧义报错。
- P3 manifest：字段与约束，目录 `$GQY_HOME/plugins/<id>/manifest.json`。
- P4 适配器：原生 stdio 换行 JSON-RPC、MCP stdio、unix / tcp 远程三种。
- P5 生命周期：加载中 → 就绪 → 关闭中 → 已关闭，外加失败；回滚与幂等。
- P6 并发模型：concurrent / single-channel / exclusive，适配器声明，运行时放行。
- P7 调用超时与取消：deadline 取 timeout_ms，晚到结果丢弃。
- P8 进程组回收：EOF 自退 → 5s → SIGTERM → 2s → SIGKILL。
- P9 热更新：安装 / 卸载 / 重装，全部幂等。
- P10 依赖：provides / requires 前置检查，缺依赖拒载、有依赖拒卸载。
- P11 工具注册：动作注册成外部工具，随 `GQY_ENABLE_EXTERNAL` 开关注入。
- P12 可观测：`/api/plugins` 状态与打点，孤儿兜底触发恒为 0。

## 3. 具体形状

### manifest 全字段表

| 字段 | 必填 | 取值与校验 | 默认 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | 是 | `^[a-z0-9][a-z0-9_-]{0,31}$`；全局唯一 | — | 重复注册报错 |
| `name` | 是 | 去首尾空白后 1–64 字符 | — | 仅展示 |
| `version` | 是 | `^\d{1,4}\.\d{1,4}\.\d{1,4}$` | — | 重装时判断新旧 |
| `description` | 否 | ≤200 字符 | `""` | 展示与工具描述 |
| `protocol` | 否 | `gqy-jsonrpc-v1` / `mcp` / `unix` / `tcp` | 缺省走 P2 后两级 | 显式指定适配器 |
| `community` | 否 | `^[a-z][a-z0-9_-]{0,31}$` | — | 仅 `protocol` 缺省时用于社区名匹配 |
| `command` | 条件必填 | 字符串数组，每项非空；不经过 shell；相对路径以 manifest 目录为基准 | — | stdio 两种协议必填；unix / tcp 忽略 |
| `cwd` | 否 | 相对 manifest 目录，解析后必须在插件目录内（不得含 `..`） | `.` | 进程工作目录 |
| `env` | 否 | string→string 映射 | `{}` | 追加到宿主环境；`PATH` / `HOME` / `GQY_HOME` 不可覆盖，忽略并告警 |
| `timeout_ms` | 否 | 整数，1–120000 | `10000` | 单次调用超时 |
| `concurrency` | 否 | `concurrent` / `single-channel` / `exclusive` | `concurrent` | 并发模型 |
| `actions` | 条件必填 | 数组，可为空；每项见下表 | `[]` | 仅 `gqy-jsonrpc-v1` 使用 |
| `address` | 条件必填 | unix：绝对路径字符串；tcp：`host:port`，host 非空、port 为 1–65535 | — | 仅 unix / tcp 必填 |
| `provides` | 否 | 字符串数组，每项 `^[a-z][a-z0-9_.-]{0,63}$`，去重 | `[]` | 服务依赖（P10） |
| `requires` | 否 | 字符串数组，每项 `^[a-z][a-z0-9_.-]{0,63}$`，去重 | `[]` | 服务依赖（P10） |

`actions[]` 每项：

| 字段 | 必填 | 校验 |
| --- | --- | --- |
| `name` | 是 | `^[a-zA-Z][a-zA-Z0-9_-]{0,63}$` |
| `description` | 是 | 1–200 字符 |
| `schema` | 是 | 合法 JSON Schema 对象，根 `type` 必须为 `object` |

未知字段忽略（前向兼容）。校验失败时该 manifest 整体拒绝，不进注册表。

### 原生 stdio 换行 JSON-RPC（gqy-jsonrpc-v1）

一行一个 JSON-RPC 2.0，`\n` 分隔。请求与响应 `id` 一致。

initialize：

```json
{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocol":"gqy-jsonrpc-v1"}}
{"jsonrpc":"2.0","id":1,"result":{"ok":true,"name":"echo","version":"1.0.0"}}
```

call：

```json
{"jsonrpc":"2.0","id":2,"method":"call","params":{"action":"echo","arguments":{"text":"hi"}}}
{"jsonrpc":"2.0","id":2,"result":{"content":"hi"}}
```

shutdown：

```json
{"jsonrpc":"2.0","id":3,"method":"shutdown"}
{"jsonrpc":"2.0","id":3,"result":{"ok":true}}
```

错误形状：

```json
{"jsonrpc":"2.0","id":2,"error":{"code":-32601,"message":"unknown action"}}
```

- 标准码：`-32700` 解析错误、`-32600` 非法请求、`-32601` 方法不存在、`-32602` 参数非法、`-32603` 内部错误。
- 适配器把 `error.message` 映射为统一错误 `call_failed`；`timeout` 由运行时产生，`exited` 由进程退出产生，不进协议。
- 加载成功 = 进程启动 + `initialize` 返回 `ok:true`。
- 调用成功取 `result.content`（字符串）；失败取 `error.message`。

### MCP stdio（mcp）

按 MCP stdio 最小集，同样一行一个 JSON-RPC 2.0。最小握手序列：

1. `initialize` → 服务端返回 `protocolVersion` 与 `capabilities`。
2. `notifications/initialized`（通知，无 id）。
3. `tools/list` → 得到 `result.tools[]`。
4. `tools/call` → 得到 `result.content[]`。
5. 关闭：`shutdown` → 关闭 stdin。

initialize：

```json
{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"gqy","version":"0.1.0"}}}
{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2024-11-05","capabilities":{},"serverInfo":{"name":"mcp-echo","version":"1.0.0"}}}
{"jsonrpc":"2.0","method":"notifications/initialized"}
```

tools/list：

```json
{"jsonrpc":"2.0","id":2,"method":"tools/list"}
{"jsonrpc":"2.0","id":2,"result":{"tools":[{"name":"mcp-echo","description":"回显文本","inputSchema":{"type":"object","properties":{"text":{"type":"string"}},"required":["text"]}}]}}
```

tools/call：

```json
{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"mcp-echo","arguments":{"text":"hi"}}}
{"jsonrpc":"2.0","id":3,"result":{"content":[{"type":"text","text":"hi"}],"isError":false}}
```

- `tools/list` 返回空数组也合法：插件就绪但不注册任何工具。
- `result.content[]` 里 `type:"text"` 的 `text` 按序拼接为结果内容；`isError:true` 视为错误，映射 `call_failed`。
- 动作名保留原始 `name`；注册成工具时按 04 P11 加 `pluginID__` 前缀。

### unix / tcp 连接约定

- unix：连接 manifest `address` 指定的 socket 绝对路径。
- tcp：连接 `host:port`。
- 同一套换行 JSON-RPC（initialize / call / shutdown），不拉起进程、不杀远程进程。
- 加载成功 = 连接建立 + `initialize` 成功；连接失败即加载失败。
- 关闭 = 发 `shutdown` 后断开连接。
- 连接断开 → 实例置 `failed`；连接错误返回 `exited`，不重试。
- 地址只来自 manifest（11 SEC6）；不扫描网段、不自动探测。

### 生命周期状态机

```
扫描发现 → discovered
discovered --Load--> loading
loading --成功--> ready
loading --失败--> 回滚：摘除实例（回到不存在）
ready --Unload--> closing
ready --进程退出--> failed
closing --回收完成--> closed → 摘除实例
failed --Load--> loading
failed --Unload--> closed（清理残留句柄）
```

每态允许的操作：

| 状态 | Load | Call | Unload |
| --- | --- | --- | --- |
| discovered | → loading | `not_ready` | 移除记录，不启动进程 |
| loading | 返回「加载中」 | `not_ready` | 中止加载并回滚 |
| ready | 幂等返回已就绪 | 执行 | → closing |
| closing | 拒绝（关闭中） | `not_ready` | 幂等返回「关闭中」 |
| closed | —（已摘除，按未加载处理） | `not_found` | 幂等返回 `not_found` |
| failed | → loading | `not_ready` | 清理残留 → closed |

### 并发模型放行规则

| 模型 | 放行规则 |
| --- | --- |
| `concurrent` | 每次调用直接并发进适配器；适配器按请求 id 多路复用，不串包 |
| `single-channel` | 运行时并发提交，适配器内部排队；任何时刻至多一个在途调用 |
| `exclusive` | 运行时实例级互斥，调用排队；Load / Call / Unload 不重叠，Unload 等在途结束 |

### /api/plugins 状态接口

`GET /api/plugins`：

```json
{
  "plugins": [
    {
      "id": "echo",
      "name": "Echo 示例",
      "version": "1.0.0",
      "protocol": "gqy-jsonrpc-v1",
      "state": "ready",
      "concurrency": "concurrent",
      "pid": 1234,
      "started_at": "2026-11-02T18:00:00+08:00",
      "calls": 10,
      "failures": 1,
      "last_error": "",
      "actions": ["echo"],
      "provides": [],
      "requires": []
    }
  ]
}
```

- `state` 取值：`discovered` / `loading` / `ready` / `closing` / `failed`；`closed` 已摘除，不出现在列表。
- 管理接口（均受 02 R8 鉴权）：

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| `GET` | `/api/plugins` | 状态列表 |
| `POST` | `/api/plugins/{id}/load` | 加载 |
| `POST` | `/api/plugins/{id}/unload` | 卸载 |
| `POST` | `/api/plugins/{id}/call` | 调试调用，body `{action, arguments}` |
| `POST` | `/api/plugins/reload` | 重新扫描 manifest |

调试调用返回：成功 `{"ok":true,"content":"…","duration_ms":8}`；失败 `{"ok":false,"error":"call_failed","message":"…"}`。

### 进程回收时序

只针对运行时拉起的 stdio 进程；固定顺序：

1. 置 `closing`，拒绝新调用。
2. 等在途调用结束，上限 5s；到点未返回的在途调用取消并返回 `canceled`。
3. 关闭 stdin：插件读到 EOF 应自行退出。
4. 等 5s。
5. 未退 → 对整个进程组 SIGTERM。
6. 等 2s。
7. 仍未退 → 对整个进程组 SIGKILL。
8. 回收：等待协程回收进程（无僵尸）、删临时目录、关管道与 socket 文件、从实例表摘除。

- 启动即独立进程组（Unix setpgid）；Windows 用 Job Object 或 `taskkill /T /F` 兜底。
- 父进程异常退出时：stdin EOF（第一层）+ 父死子亡信号 / Job Object（第二层）兜底。
- stderr 写入 `$GQY_HOME/plugins/<id>.log`，1MB 轮转。

## 4. 边角情况

- **加载失败回滚**：摘除实例、关闭已启动进程、撤销已注册工具、释放信号量；不留半成品，下次 Load 从零开始。
- **重复 id**：扫描时发现同 id manifest，报错拒绝该 manifest，不加载；不影响已运行的同 id 实例。
- **卸载时还有依赖**：返回「有依赖」错误并列出依赖者 id；依赖者先卸载，才能卸载本插件。
- **重装幂等**：同 id 同 version 重扫不重启运行中实例；同 id 新 version 先卸载旧实例，再扫描，可重新 Load；重复 Load 返回已就绪，重复 Unload 返回 `not_found`。
- **插件崩溃**：进程退出由等待协程捕获，置 `failed`，在途调用返回 `exited`，不自动重启。
- **孤儿进程**：父进程异常退出时靠 stdin EOF + 父死子亡信号 / Job Object 兜底；孤儿兜底触发次数恒为 0，非零告警（P12）。
- **stdio 半行**：以 `\n` 为界，未遇 `\n` 先缓冲，不把半行当消息；单行缓冲上限 8 MB，超限视为协议错误并关闭进程；EOF 时仍有半行则丢弃，按进程退出处理。
- **MCP tools/list 空**：合法，插件就绪但注册 0 个工具。
- **调用超时晚到结果**：到点返回 `timeout` 并删除 pending 记录；晚到响应按 id 查不到 pending 就丢弃，不串给别的调用；连续卡死由进程监控置 `failed`。

## 5. 计划中的测试

- 契约：manifest 重复 id 报错、非法 id 拒绝、未知 protocol 无适配器匹配报错。
- 契约：原生加载 → initialize → call → shutdown → 卸载全链路；结果与错误翻译正确。
- 契约：MCP initialize → tools/list 发现动作 → tools/call 调用；`isError` 映射 `call_failed`。
- 契约：unix / tcp 加载不拉起进程；断开后实例置 `failed`。
- 契约：加载失败回滚无残留；Unload 幂等；closing 拒绝新调用；崩溃置 `failed` 不无限重启。
- 契约：10 个并发调用不串包，响应按请求 id 一一对应；`exclusive` 串行可断言。
- 契约：插件 sleep 超过 `timeout_ms` 返回 `timeout`；卡死进程被回收。
- 契约：stdin EOF 自退；SIGTERM 未退升级 SIGKILL；父进程异常退出后插件限时自退，无孤儿。
- 契约：缺依赖拒绝加载；有依赖拒绝卸载；环拒绝加载。
- 契约：工具注入随 `GQY_ENABLE_EXTERNAL` 开关出现 / 消失；命名冲突回滚加载。
- 契约：热更新安装 → 扫描 → 加载；重装先卸载旧实例；重扫不重启运行中实例。

## 6. 对应代码

待建。旧实现仅作事实线索（蓝图不照抄）：

- `app/internal/plugin/`：`plugin.go` / `native.go` / `mcp.go` / `remote.go` / `transport.go` / `proc_unix.go` / `proc_windows.go` / `tool.go`。
- 示例：`app/plugins/echo`（`index.js`、`manifest.json`）、`app/plugins/mcp-echo`（`index.js`、`manifest.json`）。


## 7. 当前实现（2026-11-02）

- manifest：`id` / `name` / `version` / `protocol` / `command` / `cwd` / `env` / `timeout_ms` / `concurrency` / `actions` / `provides` / `requires` / `settings` / `source`。
- 设置：`settings[]` 只支持 `string` / `number` / `boolean` / `select`；值存 `$GQY_HOME/plugin-settings/<id>.json`（0600），插件目录不动；`GET|PUT /api/plugins/{id}/settings`；加载时把「默认值 + 已保存值」以 `GQY_PLUGIN_SETTINGS` 环境变量注入进程。
- 来源：`source: local|installed`；`plugins/<id>/.gqy-installed` 存在即 installed，插件本体只读。
- 安装：`POST /api/plugins/install` 接受 `{source: https Git URL, id}`，`git clone --depth 1` → manifest 校验 → 复制到 `$GQY_HOME/plugins/<id>` → 打安装标记 → 扫描；拒绝符号链接、重复 id、超过 128 MB。
- 删除：`DELETE /api/plugins/{id}` 先卸载，再删除插件目录与设置，路径必须在 `plugins/` 内。
- WebUI：插件页展示来源/状态/动作/设置，安装项目插件仓库（ShowImages / EmotePack），设置表单按 schema 渲染；保存后若插件运行中则自动卸载/重载。
- 测试：`internal/plugin/plugin_test.go`、`settings_test.go`、`install_test.go`、`repos_test.go`。
