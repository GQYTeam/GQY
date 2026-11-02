# 插件 manifest 规范（v1.1）

插件 = 目录 + `manifest.json` + 入口程序。本文件规定 manifest 的字段、**两种模式**与设置 schema；GQY 启动时按此解析，`TestPluginReposConform` 对组织内所有插件仓库强制执行。

## 1. 两种模式（PL2）

| 模式 | `source` | 判定 | 插件本体 | 设置 |
| --- | --- | --- | --- | --- |
| 在线安装 | `installed` | 目录里有 `.gqy-installed` 标记文件，或 manifest 声明 `"source":"installed"` | **只读**，不允许修改/覆盖 | 可改 |
| 本地自建（开发模式） | `local`（默认） | 目录里没有安装标记 | 可随时改，改完重扫即可 | 可改 |

- 安装器负责：下载 → 校验 sha256 → 解包到 `$GQY_HOME/plugins/<id>/` → 写入 `.gqy-installed` → `POST /api/plugins/reload`。
- 本地开发：直接把插件目录放进 `$GQY_HOME/plugins/<id>/`，不要放安装标记。
- **设置值永远不写进插件目录**：存在 `$GQY_HOME/plugin-settings/<id>.json`（0600），所以安装版插件也能改设置而不破坏本体。

## 2. 必填字段

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `id` | string | 插件唯一 id，`[a-z0-9][a-z0-9-]*`，与目录名一致 |
| `name` | string | 展示名 |
| `version` | string | 语义化版本；重扫时版本变化才重启 |
| `protocol` | string | `gqy-jsonrpc-v1` / `mcp` / `remote` |
| `command` | string[] | 启动命令（remote 协议改用 `address`） |
| `actions` | array | native 协议必填；MCP/remote 运行期发现，可为空 |

可选：`description`、`community`（安装来源页）、`cwd`、`env`、`timeout_ms`、`concurrency`（`serial`/`concurrent`）、`address`、`provides`、`requires`、`settings`。

## 3. 设置 schema（PL3）

```json
"settings": [
  {"key": "greeting", "title": "问候语", "type": "string", "default": "hi"},
  {"key": "limit",    "title": "上限",   "type": "number", "default": 3},
  {"key": "loud",     "title": "大声",   "type": "boolean", "default": false},
  {"key": "mode",     "title": "模式",   "type": "select", "default": "a", "options": ["a", "b"]}
]
```

- 支持类型：`string` / `number` / `boolean` / `select`（`select` 必须给 `options`）。
- `key` 全局唯一；WebUI 依次渲染为输入框/数字框/勾选框/下拉框。
- 保存时按 schema 严格校验：未知键、类型不符、选项越界都拒绝；值传 `null` 表示恢复默认。

**核心消费插件设置的实例**：`emote-pack-default` 的 `auto_emote`（boolean，默认 `true`）与 `min_score`（number，默认 `0`，0–1）。

```json
"settings": [
  {"key": "auto_emote", "title": "回复后自动发表情", "type": "boolean", "default": true},
  {"key": "min_score",  "title": "最低置信度",       "type": "number",  "default": 0.0}
]
```

- GQY 的 `/api/chat` 在每轮回复结束后读这两项：`auto_emote=false` 不自动发表情；否则只有 `suggest` 的 top1 分数 ≥ `min_score` 才追加 `emote` 事件（05 S8、S2）。
- 默认 `true` 表示装好插件就默认开启；关掉只需改设置，不必卸载；彻底不参与则卸载/删除插件。
- 设置值仍只存 `$GQY_HOME/plugin-settings/<id>.json`，插件目录不动（安装版只读也能改设置）；核心每次读「默认值 + 已保存值」，改设置即时影响下一次回复。

## 4. 生命周期与删除（PL4）

- 发现：`POST /api/plugins/reload` 扫描目录，只读插件不可被覆盖安装。
- 加载/卸载：`POST /api/plugins/{id}/load`、`/unload`。**卸载只停止实例、回到 `discovered`（仍列在插件页，可再次加载）；删除才移除目录与实例。**
- 生效设置随 `initialize` 的 `params.settings` 下发给插件（默认值 + 已保存值合并）；改了设置后卸载→加载即生效。
- 设置：`GET|PUT /api/plugins/{id}/settings`。
- 删除：`DELETE /api/plugins/{id}`——先卸载，再删插件目录与设置文件；目录必须位于 `plugins/` 之内（路径穿越拒绝）。**安装版与本地版都可删除**；区别只在「本体能不能改」。

## 5. 强制校验

`GQY/app/internal/plugin/repos_test.go` 的 `TestPluginReposConform` 会：

1. 校验 `GQY/app/plugins/*/manifest.json`；
2. 校验同级 `gqy-plugins-*` 仓库的 manifest；
3. 检查 id/name/version/protocol/command/actions、settings 类型与唯一性；
4. 检查**源码仓库不得包含 `.gqy-installed`**（安装标记只能出现在用户安装目录）。

不合规 = 测试红 = `make check` 失败。这是能把规矩钉死的地方；钉不死的部分（例如别人在本地随便改插件）只能靠「安装标记 + 只读语义」和文档约束。
