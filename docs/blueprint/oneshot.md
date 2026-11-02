# oneshot · 一次性插件蓝图

## 1. 状态

待施工（2026-11-02）。

## 2. 对应设计决定

兑现 `15-一次性插件.md` 的 NX1–NX7：

- NX1 定位：一次性插件是执行模式不是新格式，manifest 同 04 P3，加 `mode` 区分。
- NX2 来源解析与拉取：本地路径 / git / url / 内置注册表四级，拉到 `$GQY_HOME/tmp/oneshot/<id>-<run>`。
- NX3 执行形态：声明 `run` 动作走 stdio JSON-RPC，未声明直接跑 `command`。
- NX4 生命周期：解析 → 拉取 → 校验 → 执行 → 收集产物 → 默认删工作目录，`--keep` 保留。
- NX5 与常驻关系：不注册工具、不走 04 P11、不占实例表。
- NX6 缓存：键 = 来源 + 版本 / 哈希，TTL 7 天，`gqy-clean --expired` 回收。
- NX7 安全：沿用 11 SEC5「执行即信任」，来源显式指定、哈希校验防篡改。

## 3. 具体形状

### oneshot manifest 全字段（对照 plugin.md）

公共字段校验与 `plugin.md` 相同（id / name / version / protocol / command / cwd / env / timeout_ms / concurrency / actions），一次性插件在次基础上加 `mode` / `hash` / `source`，并约定 `run` 动作。

| 字段 | 类型 | 必填 | 默认 | 校验与说明 |
| --- | --- | --- | --- | --- |
| `id` | string | 是 | — | `^[a-z0-9][a-z0-9_-]{0,31}$`；缓存与目录名基准 |
| `name` | string | 是 | — | 1–64 字符，仅展示 |
| `version` | string | 是 | — | `^\d{1,4}\.\d{1,4}\.\d{1,4}$`；缓存键的一部分 |
| `description` | string | 否 | `""` | ≤200 字符 |
| `mode` | string | 是 | — | 必须为 `oneshot`（缺省 = resident，不在本页） |
| `protocol` | string | 是 | — | 只支持 `gqy-jsonrpc-v1` / `mcp`（stdio 两种）；unix / tcp 与一次性语义冲突，不支持 |
| `command` | string[] | 条件必填 | — | 数组、不经过 shell；相对 manifest 目录 |
| `cwd` / `env` | — | 否 | `.` / `{}` | 同 plugin.md；`PATH` / `HOME` / `GQY_HOME` 不可覆盖 |
| `timeout_ms` | int | 否 | `10000` | 1–120000 |
| `concurrency` | string | 否 | `concurrent` | 同 plugin.md |
| `actions` | array | 条件必填 | `[]` | 原生协议使用；建议声明 `run` 作为执行入口 |
| `hash` | string | 否 | `""` | `sha256:<64 位小写 hex>`；存在则强校验 |
| `source` | string | 否 | `""` | 规范来源（NX2 格式）；CLI `--from` 覆盖 |

`actions[]` 里 `run` 动作的约定：

```json
{
  "name": "run",
  "description": "执行一次性任务",
  "schema": {
    "type": "object",
    "properties": {"source_dir": {"type": "string"}},
    "required": ["source_dir"]
  }
}
```

### 来源解析四级

| 级别 | 格式 | 示例 | 哈希锚点 |
| --- | --- | --- | --- |
| 本地路径 | 绝对 / 相对路径 | `/path/to/migrator`、`../migrator` | 不校验（本地可信） |
| git | `git+<url>@<ref>` 或 `<url>#<ref>` | `git+https://github.com/x/migrator.git@v1.0.0` | ref 解析出的 commit SHA |
| url | `https://…`，tarball 或单文件 | `https://example.com/migrator.tar.gz` | manifest `hash` 必须给出 |
| 内置注册表 | `reg:<name>` 或 `<name>@<version>` | `reg:migrator`、`migrator@1.0.0` | 注册表条目自带 `sha256` |

- git 的 `<ref>` 支持 commit SHA / tag / branch；branch 解析后锁定 commit SHA 并记入运行记录。
- url 与注册表来源缺哈希 → 告警后执行（NX7）。
- 实现现状（2026-11-02）：注册表文件固定为 `$GQY_HOME/oneshot-registry.json`；url 源目前只接受 `.tar.gz` / `.tgz` / `.tar`（单文件源未实现，见施工 009 留底）。

### 拉取目录布局

```
$GQY_HOME/tmp/oneshot/
  <id>-<run>/            # 一次运行的工作目录，默认执行后删除
    manifest.json
    …插件文件…
    .run.json            # 运行记录：来源、哈希、开始时间、退出码、状态
    out/
      stdout.txt         # stdout
      result.json        # 结构化结果（content 可解析为 JSON 时）
      files/             # 声明收集的文件（若 result 带 files 数组）
  .cache/
    <id>-<version>/      # 缓存（NX6），TTL 7 天
      manifest.json
      …插件文件…
```

### 执行入口

声明 `run` 动作 → 走 04 P4 的 stdio `call` 语义，`action` 固定为 `run`：

```json
{"jsonrpc":"2.0","id":1,"method":"call","params":{"action":"run","arguments":{"source_dir":"/path"}}}
{"jsonrpc":"2.0","id":1,"result":{"content":"{\"migrated\":12,\"files\":[\"out/migrated.json\"]}"}}
```

未声明 `run` → 直接执行 `command`，参数以 stdin 单行 JSON 传入，stdout 为结果。

### 产物收集形状

| 产物 | 落点 | 规则 |
| --- | --- | --- |
| stdout | `out/stdout.txt` | 原样保存 |
| 结构化结果 | `out/result.json` | `result.content` 能解析为 JSON 对象时保存 |
| 文件 | `out/files/` | `result` JSON 带 `files: ["相对路径"]` 时，把工作目录内的这些文件复制进来 |

### 缓存键与 TTL（NX6）

- 缓存键：`<id>-<version>`；命中要求来源解析出的哈希与缓存目录 `manifest.json` 的 `hash` 一致。
- 路径：`$GQY_HOME/tmp/oneshot/.cache/<id>-<version>/`。
- TTL：7 天（按目录 mtime），由 `gqy-clean --expired` 回收（09 ST7）。

### `gqy run` CLI 形状（建议）

```
gqy run <source> [--args '<json>'] [--keep] [--no-cache] [--id <id>]
```

- `source`：NX2 四级格式。
- `--args`：一次性参数 JSON；`--keep`：保留工作目录；`--no-cache`：跳过缓存。
- 成功输出：`{"ok":true,"run_dir":"…","result":{…},"duration_ms":…}`
- 失败输出：`{"ok":false,"error":"…","message":"…"}`

## 4. 边角情况

- **拉取失败**：git 不可达 / url 下载失败 / 注册表查无 → 返回错误，不创建工作目录残留。
- **哈希不匹配**：manifest 或注册表哈希与拉取内容不符 → 拒绝执行，不写缓存。
- **执行失败清理**：退出码非 0 → 工作目录仍默认删除（`--keep` 除外），不写缓存，日志记退出码。
- **--keep**：保留 `$GQY_HOME/tmp/oneshot/<id>-<run>/`，并在输出里给出目录路径；仍不注册、不常驻。
- **同版本重复执行缓存命中**：缓存存在且哈希一致 → 直接复用，不重新拉取；`--no-cache` 强制重拉。
- **TTL 到期**：`gqy-clean --expired` 删除超过 7 天的缓存；下次执行视为未命中，重新拉取。

## 5. 计划中的测试

- `TestOneshotPullFail`：git / url / 注册表不可达 → 报错且无目录残留。
- `TestOneshotHashMismatch`：哈希不匹配 → 拒绝执行、不写缓存。
- `TestOneshotRunAndCleanup`：声明 `run` 动作 → call 执行 → 产物收集 → 工作目录默认删除。
- `TestOneshotDirectCommand`：未声明 `run` → 直接跑 `command`，stdin 传参、stdout 为结果。
- `TestOneshotKeep`：`--keep` 保留工作目录；仍不注册、不常驻。
- `TestOneshotCacheHit`：同来源同哈希重复执行命中缓存，不重新拉取。
- `TestOneshotGcRecycle`：`gqy-clean --expired` 回收超过 TTL 的缓存，未到期不动。

## 6. 对应代码

待建。旧无实现。CLI 建议 `cmd/gqy-run`（独立子命令）或 `gqy-server` 子命令，施工单定。
