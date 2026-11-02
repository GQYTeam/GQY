# store · 存储与目录

**状态**：待施工（2026-11-02）

**对应设计决定**：ST1–ST7（09-存储）、S5–S6（05-会话与产物）、R7（02-网关与路由）。

## 具体形状

### 数据根

`$GQY_HOME`，默认 `~/.gqy`。创建时强制权限：目录 0700，敏感文件 0600。

```
$GQY_HOME/
  providers.json          # 见 gateway.md
  routing.json            # 见 gateway.md
  settings.json           # 见 config.md
  gateway/
    requests.jsonl        # 见 gateway.md（一行一请求，不落正文）
  sessions/
    <SESSION_ID>/
      manifest.json
      tmp/
        uploads/
        generated/
        scratch/
      logs/
        tools/YYYY-MM-DD.jsonl
      io/
        input/
        output/
        context/
          context.md
          messages.jsonl
        webui/
          open.sh
          index.html
      scripts/
  plugins/
    <plugin-id>/manifest.json   # 见 plugin.md
```

### manifest.json

```json
{
  "session_id": "…",
  "workspace": "/path/to/project",
  "created_at": "2026-11-02T10:00:00+08:00",
  "artifacts": [
    {"id": "…", "path": "…", "kind": "final|intermediate", "created_at": "…"}
  ],
  "cleanup_policy": {"session_tmp_ttl_days": 7, "logs_ttl_days": 30}
}
```

- `session_id`：创建后不变；`artifacts` 只增不改（移除产物时标记而非删除行）。
- `workspace` 可为空字符串 = 无工作区会话。

### 工具日志 `logs/tools/YYYY-MM-DD.jsonl`

一行一次工具调用，字段固定：

```json
{"timestamp":"…","tool_name":"shell","status":"ok","duration_ms":123,"input_summary":"cat README.md","output_summary":"…","artifact_paths":[]}
```

| 字段 | 取值 |
| --- | --- |
| `timestamp` | RFC 3339，本地时区带偏移 |
| `tool_name` | 核心工具名或 `pluginID__action` |
| `status` | `ok` / `error` / `timeout` / `canceled` |
| `duration_ms` | 整数 |
| `input_summary` | 脱敏后的参数摘要，不含密钥/token/正文 |
| `output_summary` | 脱敏后的输出摘要 |
| `artifact_paths` | 字符串数组，可为空 |

### 文件格式与写入

| 类型 | 格式 | 写入方式 |
| --- | --- | --- |
| 配置 | JSON | 临时文件 + fsync + rename（原子写） |
| 请求日志 / 工具日志 | JSONL | 按行追加，写完整行 |
| manifest | JSON | 原子写 |

## 边角情况

- **配置半截**：读到不合法 JSON，沿用旧快照并记日志（ST4、CF4）。
- **日志坏行**：跳过该行、计数，不影响其余行（ST4）。
- **日志重名**：工具日志按日期分文件，跨天自动切新文件。
- **崩溃恢复**：会话从消息事件流重建到最后一个完整回合；manifest 原子写保证不会半截（ST5）。
- **路径越界**：`../`、绝对路径逃逸、符号链接指向数据根之外，一律拒绝（ST6、SEC3）。
- **清理误删**：`gqy-clean` 删除前核对目标路径在 `$GQY_HOME` 内（ST7）。

## 计划中的测试

- `TestDataRootPermissions`：数据根 0700、敏感文件 0600。
- `TestConfigAtomicWrite`：写入中途崩溃读到的仍是完整旧版。
- `TestLogCorruptLineSkipped`：坏行跳过并计数。
- `TestSessionRebuildFromEvents`：从事件流重建到最后一个完整回合。
- `TestCleanupExpiredOnly`：`gqy-clean --expired` 只清过期，未到期不动。
- `TestPathTraversalRejected`：`../` 与符号链接逃逸被拒。
- `TestNoSecretInLogs`：密钥/token/正文不出现在任何落盘文件。

## 对应代码

待建。旧实现参考：`app/internal/session/store.go`、`cleanup.go`（git 历史 HEAD~2，仅作事实线索）。
