# migrate · 迁移器（一次性插件）蓝图

**状态**：施工中（2026-11-02）

**对应设计决定**：MG1–MG8（`18-记忆与迁移.md`）、NX1–NX7（`15-一次性插件.md`）、MEM2–MEM4（`blueprint/memory.md`）。

## 1. 形态

迁移器是**一次性插件**，仓库独立：`GQYTeam/gqy-plugins-Migrator-default`。

- `mode: "oneshot"`，`protocol: "gqy-jsonrpc-v1"`，`command: ["node","index.js"]`。
- 用现成内核执行：`gqy run <迁移器目录>` 或 `gqy run git+https://github.com/GQYTeam/gqy-plugins-Migrator-default.git#<ref>`。
- 零依赖，只用 Node 内置模块；读旧库用 `node:sqlite`（Node ≥ 22.5），写文件用 `node:fs`。
- 只读源（MG1）；默认 dry-run（MG2）；幂等（MG3）；默认脱敏（MG4）；可回滚（MG5）。

### manifest.json

```json
{
  "id": "migrator-default",
  "name": "迁移器（旧版 GQY_HOME → 新版）",
  "version": "1.0.0",
  "description": "把旧版 GQY v0.7.0 的 GQY_HOME 导出迁到新数据根：人格、记忆、知识库、提示词、表情包、技能、身份与配置；默认 dry-run、幂等、脱敏",
  "mode": "oneshot",
  "protocol": "gqy-jsonrpc-v1",
  "command": ["node", "index.js"],
  "cwd": ".",
  "timeout_ms": 120000,
  "concurrency": "exclusive",
  "actions": [
    {
      "name": "run",
      "description": "执行迁移；参数见 docs/README.md",
      "schema": {
        "type": "object",
        "properties": {
          "input": {"type": "string", "description": "旧版导出目录（含 manifest.json 与 home/）"},
          "home": {"type": "string", "description": "目标 $GQY_HOME；缺省读环境变量 GQY_HOME"},
          "apply": {"type": "boolean", "description": "true 才写目标；默认 false = dry-run"},
          "kinds": {"type": "array", "items": {"type": "string"}, "description": "本次迁哪些 unit 组：config,personas,memory,kb,prompts,memes,skills,scripts,identities,usage,archive"},
          "persona": {"type": "string", "description": "只迁某个人格库"},
          "include_secrets": {"type": "boolean", "description": "迁移 api_key 明文（默认脱敏）"},
          "no_backup": {"type": "boolean", "description": "跳过覆盖前备份"},
          "report": {"type": "string", "description": "报告输出路径；默认 $GQY_HOME/.migrate-reports/<run>.json"}
        },
        "required": ["input"]
      }
    }
  ],
  "provides": [],
  "requires": []
}
```

## 2. 命令行

```bash
# dry-run（默认）：只出报告，不写目标
gqy run ./gqy-plugins-Migrator-default --args '{"input":"/path/gqy_backup_20261005_180648"}'

# 真正写入
gqy run ./gqy-plugins-Migrator-default --args '{"input":"/path/backup","apply":true}'

# 只迁记忆与人格
gqy run ./gqy-plugins-Migrator-default --args '{"input":"/path/backup","apply":true,"kinds":["personas","memory"]}'
```

stdout 一行 JSON 结果：`{"ok":true,"output":{"applied":true,"units":{…},"warnings":[…],"report_path":"…"}}`。

## 3. 单元映射

路径里 `<user>` 指旧库的账号目录（每台机器一个），迁移时不保留原名，统一映射到目标 `memory/<persona>`。

| unit | 旧路径 | 目标 | 组 | 优先级 |
| --- | --- | --- | --- | --- |
| `layout.home_marker` | `.home-layout-v1` | `$GQY_HOME/.home-layout-v1`（写入 `gqy-1` 并保留旧值到 meta） | config | P0 |
| `config.main` | `config/config.jsonc` | `settings.json` + `providers.json` + `routing.json`（脱敏） | config | P0 |
| `config.imessage` | `config/imessage.json` | `settings.json` 的 `platforms.imessage` | config | P2 |
| `config.webui_theme` | `config/webui-theme.css` | `$GQY_HOME/themes/webui-theme.css` | config | P1 |
| `config.webui_themes` | `config/webui-themes/*.css` | `$GQY_HOME/themes/*.css` | config | P1 |
| `personas.manifest` | `<user>/<persona>/persona.toml` | `memory/personas.json` | personas | P0 |
| `personas.memory` | `<user>/<persona>/memory/memory.db` | `memory/<persona>/{facts,episodes,revisions}.jsonl` | memory | P0 |
| `state.evicted_context` | `state/<user>/<persona>/memory/evicted_context.db` | `memory/<persona>/evicted.jsonl` | memory | P0 |
| `kb.default_state` | `data/default-kb/state.json` | `$GQY_HOME/kb/default-kb/state.json` | kb | P1 |
| `kb.meta` | `data/kb/kb_meta.db` | `$GQY_HOME/kb/meta.json`（表结构→ JSON） | kb | P1 |
| `kb.files` | `data/kb/files/**` | `$GQY_HOME/kb/files/**`（原样） | kb | P1 |
| `data.prompts` | `data/prompts/**` | `$GQY_HOME/prompts/**` | prompts | P1 |
| `data.memes` | `data/memes/**` | `$GQY_HOME/memes/**` | memes | P1 |
| `extensions.skills` | `extensions/skills/**` | `$GQY_HOME/skills/**` | skills | P1 |
| `extensions.scripts` | `extensions/scripts/**` | `$GQY_HOME/scripts/**` | scripts | P1 |
| `state.skill_drafts` | `state/skill-drafts/**` | `$GQY_HOME/skills/drafts/**` | skills | P2 |
| `home.identities` | `home/<host>/identities/*.md` | `$GQY_HOME/identities/*.md` | identities | P1 |
| `state.profile` | `state/profile.md` | `$GQY_HOME/profile.md` | identities | P2 |
| `state.usage` | `state/usage.json` | `$GQY_HOME/state/usage.json` | usage | P2 |
| `state.repl_history` | `state/repl-history.jsonl` | `$GQY_HOME/state/repl-history.jsonl` | usage | P2 |
| `home.conversation` | `home/<host>/conversation.db` | `$GQY_HOME/sessions/<session>/…`（best-effort） | archive | P2 |
| `home.pictures` | `home/<host>/pictures/**` | `$GQY_HOME/data/pictures/**` | archive | P2 |
| `home.documents` | `home/<host>/documents/**` | `$GQY_HOME/data/documents/**` | archive | P2 |
| `home.ledger` | `home/<host>/ledger/ledger.db` | `$GQY_HOME/data/ledger/ledger.db`（原样复制） | archive | P2 |
| `platform.message_history` | `data/platforms/**/*.sqlite3` | `$GQY_HOME/data/platforms/**`（原样复制） | archive | P2 |

### 3.1 记忆字段映射（`personas.memory`）

facts：旧列同名字段直迁；`memory_type/truth_status/importance/tags/source_episode_ids/origin_session_id` 全部保留；**列形状按实测**：`subjects` 是对象数组 `[{"name":…}]`（保持对象数组）、`tags` 是字符串数组、`source_episode_ids` 是整数数组——不要一律 `JSON.parse` 之后当字符串数组用。`legacy_id = legacy:facts:<id>`；`source = migration:legacy-0.7`。
episodes：旧列直迁 + `retention/user_message/assistant_message/expires_at/consolidated_at/promotion_pending/promoted_at/source_key/origin_*`；`legacy_id = legacy:episodes:<id>`。
revisions：`legacy_id = legacy:memory_revisions:<id>`。
embeddings：按 `(kind,id)` 挂到对应记录上；**编码按 BLOB 长度判断，不按 model 名**（实测 2048B = 512 维 float32，model 名却写 int8，见 `blueprint/memory.md` 的陷阱说明）。挂不上的向量不丢、只计数。实测口径：embeddings 3031 条，能挂上 **2085**（facts 347 + episodes 1738）；**孤儿 946**（余量 630 + 挂不到具体记录的 316）；`missing_vectors=316`；`evicted_embeddings` 192 条全为孤儿（`embedding_json` 列是空串死列，只有 BLOB 可用）。报告同时给 `orphan_vectors` / `missing_vectors` / `vector_surplus`，totals 合计 1138。
pending_events：`processed_at IS NULL` 的作为 episodes（`retention:"short_term"`）迁，已处理的跳过（实测该表 0 行）。
skill_records：写进 `memory/<persona>/skills.json`（只读索引，实测 0 行）。
**缺失即缺失**：manifest 声明 4 个 persona 记忆库，磁盘上只有 `default`；其余 3 个不报错、不造空库，记进报告 `missing`。真实数据只有 `default`：facts 347 / episodes 2054 / revisions 47 / embeddings 3031。

### 3.2 配置映射（`config.main`）

| 旧字段 | 新位置 | 说明 |
| --- | --- | --- |
| `providers[]` | `providers.json.providers[]` | `id/name/base_url/protocol/models` 保留；`api_key` **默认不迁**，改 `accounts[].api_key_env` |
| `active_provider` / `active_provider_models` | `routing.json.default` | 取第一个模型作默认引用 |
| `model_tiers` | `routing.json.groups[]` | 每档一个 group，`strategy: "order"` |
| `context.*` | `settings.json.context` | 只迁数值型（窗口、预算、轮数） |
| `tools.*` | `settings.json.tools` | 超时与允许清单 |
| `prompt.system_prompt_file` | `settings.json.system_prompt_file` | 指向 `prompts/system-prompt.md` |
| `platforms` / `accounts` | `settings.json.platforms` | 账号 token 脱敏，同 api_key 规则 |
| `voice.*`（minimax 等） | `settings.json.voice` | `api_key` 同样脱敏为 env 引用 |
| `mcp.servers[].env` | `settings.json.mcp` | 值里疑似 cookie/token 的键（`*_COOKIE` / `*_SESSDATA` / `*_TOKEN`）脱敏为 env 引用 |
| `api_quota.*.accounts[].api_key` | `providers.json.accounts[].api_key_env` | 按 provider 归并到对应账号 |
| 其它 | `settings.json.migrated.legacy` | 原样塞进 `migrated` 命名空间留档，不参与运行 |

**凭据清单（实测，迁移必须逐项脱敏）**：`providers[]` 6 个非空 `api_key`；`platforms.qq.access_token`；`imessage.token`；`voice.tts.minimax.api_key`；`mcp.servers[].env` 里的 `NETEASE_COOKIE` / `BILIBILI_SESSDATA`；`api_quota.deepseek.accounts[].api_key`。全部默认转 env 引用，`--include-secrets` 才迁明文（目标 0600，报告仍不出现明文）。

**绝对路径重写**：旧库 `kb_meta.files.path`、`artifact_assets.source_key` 写死 `/Users/<user>/…` 绝对路径。迁移时按「导出根之后的相对路径」重写；重写不了的记进报告 `path_rewrites` 并保留原名到 `legacy_path` 字段，不静默丢弃。

## 4. 报告

```json
{
  "schema_version": 1,
  "run_id": "20261102T214500",
  "input": "/path/backup",
  "home": "/tmp/gqy-home",
  "applied": false,
  "source_fingerprint": "sha256:…",
  "units": {
    "personas.memory": {"files": 4, "records": {"facts": 347, "episodes": 2054, "revisions": 47, "embeddings": 3031},
                        "orphan_vectors": 192, "skipped": 0, "status": "planned"}
  },
  "totals": {"records": 2448, "files": 26, "bytes": 19400000, "redacted": 7},
  "warnings": ["state.evicted_context: evicted_turns=0，evicted_embeddings=192 为孤儿，已计入 orphan_vectors"],
  "skipped": [],
  "duration_ms": 1234,
  "exit_code": 0
}
```

报告写入 `$GQY_HOME/.migrate-reports/<run_id>.json`；`--report` 可覆盖路径。**报告不含正文与密钥**（只含计数、路径、告警）。

## 5. 幂等与安全

- 目标已存在 `memory/<persona>/facts.jsonl` 时：按 `legacy_id` 去重，只补差集；写前把目标备份到 `$GQY_HOME/.migrate-backup/<run_id>/`（`--no-backup` 关）。
- `meta.json.migrated_from.source_fingerprint`：对 manifest 的 `entries[].blake3` 排序后做 sha256，用于判断「源没变就不重复迁」。
- 目标目录用 `0700`、文件 `0600`；符号链接一律拒绝（`lstat` 判断），防穿越。
- 源库以 `file:…?mode=ro` 打开；拿不到只读能力时直接失败，不降级成可写。

## 6. 仓库结构

```
gqy-plugins-Migrator-default/
  manifest.json
  index.js               # JSON-RPC 主循环：initialize / call{action:run} / shutdown
  src/
    args.js              # 参数校验与默认值
    legacy.js            # 旧库读取（node:sqlite，只读）
    memory.js            # facts/episodes/revisions/evicted → JSONL
    config.js            # config.jsonc → settings/providers/routing（脱敏）
    assets.js            # kb/prompts/memes/skills/scripts/identities 资产复制
    report.js            # 报告与指纹
  test/
    args.test.mjs
    memory.test.mjs      # 用运行期生成的临时旧库（node:sqlite 建表）跑真路径
    config.test.mjs
    idempotent.test.mjs
  package.json           # engines.node >= 22.5，scripts.test 串起全部测试
  README.md docs/README.md
  LICENSE NOTICE CONTRIBUTING.md SECURITY.md CHANGELOG.md .editorconfig .gitignore Makefile .github/
```

## 7. 验收

```bash
# 1. 真备份 dry-run：报告里 facts=347 / episodes=2054 / revisions=47 / evicted=0
#    向量：挂上 2085、孤儿 946（余量 630 + 挂不上 316）、缺向量 316、evicted 孤儿 192、合计 1138；3 个 persona 库缺失记 missing
gqy run ./gqy-plugins-Migrator-default --args '{"input":"<backup>"}'

# 2. 真备份 apply 到临时 home
GQY_HOME=/tmp/gqy-home gqy run ./gqy-plugins-Migrator-default --args '{"input":"<backup>","apply":true}'

# 3. 记忆可用
gqy-server &  # 或用现有实例
curl -s localhost:5173/api/memory | jq '.personas[]|{id,counts}'
curl -s -X POST localhost:5173/api/memory/default/recall -d '{"query":"<关键词>","limit":5}' | jq '.items[].score'

# 4. 幂等：再跑一次 apply，报告里 "records_added": 0
# 5. 脱敏：grep -r "sk-" /tmp/gqy-home 无命中；providers.json 里都是 api_key_env
# 6. npm test 全绿；CI（Node 22）绿
```
