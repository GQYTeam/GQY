# legacy-home · 旧版 GQY_HOME 导出结构参考

本页是旧版 GQY v0.7.0 的 `GQY_HOME` 导出结构快照（备份目录 `gqy_backup_20261005_180648`，只读）：旧库里有什么、路径怎么排、库表怎么建、字段怎么编码、哪些地方自相矛盾。给迁移器实现者、资料维护者与对账的人查。

它不做决定、不写正文：迁移规则在 `designs/18-记忆与迁移.md`（MEM1–MEM8、MG1–MG8），落地形状在 `blueprint/migrate.md`，目标侧记录模型在 `blueprint/memory.md`。本页与 `blueprint/migrate.md` §3 的 unit 映射表一一对齐。

**口径**：只写结构与计数。「账户目录名」写作 `<user>`，「人格」写作 `<persona>`，「密钥」写作 `<redacted>`，「正文」写作 `<…>`。`<user>` 在本备份里是**三处互不相同**的字符串（`home/<user>/`、`home/state/<user>/`、`home/home/<user>/`），统一成一个占位符；迁移必须显式映射三次，不能假设同名。所有计数在 2026-11-02 用 `file:<db>?mode=ro&immutable=1` 只读复核，命令见第 10 节。

## 1. 导出元信息

`manifest.json` 是文件级清单，一条 entry 四个字段：`unit` / `path` / `size` / `blake3`。注意 `path` 是**相对 `home/`** 的，不是相对备份根。

| 字段 | 值 | 说明 |
| --- | --- | --- |
| `format_version` | 1 | 导出格式代数 |
| `gqy_version` | 0.7.0 | 产出这份导出的产品版本 |
| `config_version` | 3 | 主配置代数，对应 `config.jsonc` 的 `config_version` |
| `exported_at` | 2026-10-05T18:06:50.724829+08:00 | 导出时刻；全备份唯一用本地偏移的时间戳 |
| `secrets_included` | true | **明文密钥与密码哈希已包含**，见第 7 节 |
| `scope` | `{index:false, platforms:true}` | 范围声明；文档层没有解释，别当契约 |
| `schema_versions` | 6 项，见 1.1 | 各库的结构版本（`PRAGMA user_version`） |
| `entries` | 174 条 / 415,970,370 B（396.7 MiB） | 清单声明 |
| 磁盘实存 | **173 个文件** | 清单里的 3 个 `memory.db` 缺失，磁盘多 3 个 `.DS_Store` |
| unit 数 | 27 个 | 按清单去重；任务单曾按 26 计 |

### 1.1 `schema_versions` = 各库 `PRAGMA user_version`

| unit | 库 | `user_version` |
| --- | --- | --- |
| `home.conversation` | conversation.db | 39 |
| `home.ledger` | ledger.db | 2 |
| `platform.message_history` | onebot history.sqlite3 | 4 |
| `personas.memory` | memory.db | 0 |
| `state.evicted_context` | evicted_context.db | 0 |
| `kb.meta` | kb_meta.db | 0 |

三个记忆/知识库库是 0：旧版改表不升版本，判断结构只能看 DDL（第 4 节）。

## 2. 布局树

`home/` 就是旧版 `GQY_HOME`。它有两层历史叠层：`home/<user>/` 是账户层，`home/home/<user>/` 是多用户层，两者都挂在同一个 `home/` 下。

```text
<导出根>/
  manifest.json                # 文件清单（174 条）
  .DS_Store                    # 脏文件（清单外）
  home/                        # = 旧版 GQY_HOME
    .home-layout-v1            # 4 字节，内容就是一个账户名（<user>）
    config/
      config.jsonc             # 主配置（JSONC，21 个顶层键，27,815 B）
      imessage.json            # iMessage connector（含 token）
      webui-theme.css, webui-themes/*.css
    data/
      kb/kb_meta.db, kb/files/default-kb/kb/**     # 知识库索引 + 13 篇正文
      default-kb/state.json                        # 默认库发布态
      memes/<lib>/{index.json,images/*}            # 2 个表情库，28 文件
      prompts/*.md, prompts/hints/default.md       # 提示词与语气 hint
      platforms/onebot/message_history/history.sqlite3
    extensions/
      skills/<skill>/SKILL.md                      # 2 个技能，各 1 个文件
      scripts/<user>/<persona>/{index.json,*.py}   # 1 个启用脚本 + 7 条登记
    home/<user>/                # 第二层：运行数据（会话/图片/文档/记账/身份）
      conversation.db           # 会话/轮次/日志/图片（204 MB，32 张实表）
      pictures/{album/<persona>,generated-images,web-images}/**
      documents/**              # 4 篇研究文档
      ledger/ledger.db
      identities/*.md
    <user>/<persona>/           # 账户层：人格与记忆
      persona.toml              # 子系统开关（memory/skills/…）
      memory/memory.db          # 长期记忆（磁盘上只有 1 个）
    state/
      usage.json, profile.md, repl-history.jsonl
      imessage-contacts.json, arch_news_last_seen.json
      <user>/<persona>/memory/evicted_context.db   # 5 个
      skill-drafts/.published-draft-<hash>/**
```

## 3. unit 总表（27 个）

「组」列取自 `blueprint/migrate.md` §3，两页对齐用；字节数是清单 `size` 之和。

| unit | 路径模式 | 文件 | 字节 | 组 | 作用 |
| --- | --- | --- | --- | --- | --- |
| `home.conversation` | `home/home/<user>/conversation.db` | 1 | 204,607,488 | archive | 会话、轮次、轮内事件、图片与产物、账号 |
| `home.pictures` | `home/home/<user>/pictures/**` | 92 | 184,354,518 | archive | 相册 27 / 生成图 61 / 网络图 3 |
| `personas.memory` | `home/<user>/<persona>/memory/memory.db` | 4 | 17,948,672 | memory | 长期记忆；**磁盘只有 1 个**，缺 3 个 |
| `platform.message_history` | `home/data/platforms/onebot/message_history/history.sqlite3` | 1 | 4,390,912 | archive | OneBot 消息历史（6,046 条） |
| `state.evicted_context` | `home/state/<user>/<persona>/memory/evicted_context.db` | 5 | 2,174,976 | memory | 被挤出上下文的轮次 + 向量（1 个有数据） |
| `data.memes` | `home/data/memes/<lib>/{index.json,images/*}` | 28 | 2,047,414 | memes | 2 个表情库：25 张图 + 2 个 index.json + 1 个脏文件 |
| `extensions.skills` | `home/extensions/skills/<skill>/SKILL.md` | 2 | 106,983 | skills | 已装技能，纯 Markdown，无 manifest |
| `home.ledger` | `home/home/<user>/ledger/ledger.db` | 1 | 106,496 | archive | 记账（6 表，2 笔流水） |
| `home.documents` | `home/home/<user>/documents/**` | 4 | 103,061 | archive | 深度研究产出的 .md（正文敏感） |
| `config.main` | `home/config/config.jsonc` | 1 | 27,815 | config | provider / 路由 / 平台 / 插件 / 上下文 |
| `kb.files` | `home/data/kb/files/default-kb/kb/**` | 13 | 26,154 | kb | 知识库正文（2 个目录，13 篇） |
| `state.skill_drafts` | `home/state/skill-drafts/.published-draft-<hash>/**` | 2 | 19,947 | skills | 技能草稿快照（与 skills/ 有一份重复） |
| `extensions.scripts` | `home/extensions/scripts/<user>/<persona>/**` | 3 | 17,657 | scripts | 1 个脚本 + `index.json`；7 条登记里 5 条无文件 |
| `data.prompts` | `home/data/prompts/*.md`, `hints/*.md` | 4 | 12,524 | prompts | 系统提示词、2 份人格提示词、1 条 hint |
| `kb.meta` | `home/data/kb/kb_meta.db` | 1 | 12,288 | kb | 13 行文件索引（`path` 是绝对路径） |
| `state.repl_history` | `home/state/repl-history.jsonl` | 1 | 5,559 | usage | 58 行，每行一个 JSON 字符串（裸输入） |
| `config.webui_themes` | `home/config/webui-themes/<name>.css` | 1 | 3,226 | config | 额外主题包 |
| `config.webui_theme` | `home/config/webui-theme.css` | 1 | 2,636 | config | 当前主题（MD3 token） |
| `config.imessage` | `home/config/imessage.json` | 1 | 757 | config | iMessage 连接器（token / contacts / url） |
| `state.usage` | `home/state/usage.json` | 1 | 566 | usage | 累计用量计数器（10 个键） |
| `kb.default_state` | `home/data/default-kb/state.json` | 1 | 391 | kb | 默认库发布态（commit / hash / 时间） |
| `personas.manifest` | `home/<user>/<persona>/persona.toml` | 1 | 116 | <user> | 子系统开关（16 B 级别的小文件） |
| `state.arch_news` | `home/state/arch_news_last_seen.json` | 1 | 107 | other | Arch 新闻已读游标 |
| `state.imessage_contacts` | `home/state/imessage-contacts.json` | 1 | 59 | other | 联系人别名表（键是真实姓名） |
| `home.identities` | `home/home/<user>/identities/*.md` | 1 | 28 | identities | 身份卡（一句话） |
| `state.profile` | `home/state/profile.md` | 1 | 16 | identities | 用户画像（只有一行标题，实际为空） |
| `layout.home_marker` | `home/.home-layout-v1` | 1 | 4 | config | 布局标记，内容就是账户名 |

## 4. 六个 SQLite 库

一共 6 类库、10 个文件。

### 4.1 memory.db —— 长期记忆（default 库 17.3 MB）

| 表 | 行数 | 关键列 |
| --- | --- | --- |
| `facts` | 347 | `content`、`memory_type`、`truth_status`、`status`、`importance`、`confidence`、`strength`、`recall_count`、`subjects`、`tags`、`source_episode_ids`、`origin_session_id`、`visibility`、`owner_principal` |
| `episodes` | 2054 | `content`、`retention`、`source`、`status`、`user_message`、`assistant_message`、`source_key`、`promotion_pending`、`promoted_at`、`consolidated_at`、`expires_at`、`origin_*`（9 列） |
| `memory_embeddings` | 3031 | 主键 `(kind,id)`；`model`、`content_sha256`、`embedding` BLOB |
| `memory_revisions` | 47 | `memory_id`、`old_content`、`new_content`、`source_episode_ids` |
| `memory_meta` | 1 | `generation`、`database_id`（`mem-<32hex>`）、`access_schema_version=2` |
| `pending_events` | 0 | `user_message`、`assistant_message`、`processed_at` |
| `skill_records` | 0 | `name`（唯一）、`path`、`summary` |

**枚举实测**（迁移必须保留的取值域）：

- `facts.memory_type`：`fact` 258 / `preference` 25 / `correction` 22 / `self` 17 / `task` 14 / `relationship` 10 / **`other` 1**（枚举外）。
- `facts.truth_status`：`accepted` 216 / `reported` 131；`facts.status` 全部 `active`。
- `facts.visibility`：`privileged` 330 / `public` 11 / `principal` 6——**三值**，新版蓝图只写了两值。
- `episodes.retention`：`long_term` 1054 / `short_term` 1000；`episodes.status`：`active` 1566 / `forgotten` 488。
- `episodes.source`：`episode` 1407 / `diary-organizer` 646 / `mood` 1；`episodes.visibility`：`privileged` 1961 / `principal` 93。
- `episodes.origin_kind`：空 1054 / `local` 812 / `owner_platform` 117 / `platform` 71；`origin_platform`：空 1866 / `onebot` 127 / `imessage` 61。
- `memory_embeddings.kind`：`episode` 2684 / `fact` 347；`model` 只有 `local:bge-small-zh-v1.5-int8` 一个值。
- 正文体量：facts 合计 36,585 字符（均 105），episodes 合计 624,008 字符（均 304）；迁成 JSONL 约 1 MB 量级。
- `sqlite_sequence`：episodes 3000、facts 349、revisions 47，都高于实际行数（有物理删除，见第 8 节）。

### 4.2 evicted_context.db —— 被挤出的上下文

5 个文件，合计 2.1 MiB；业务表 2 张 + FTS5 虚拟表 1 + 影子表 4。

| 表 | 行数（5 库合计） | 关键列 |
| --- | --- | --- |
| `evicted_turns` | **0** | `source_id`（部分唯一索引）、`timestamp`、`role`、`content`、`visibility`、`owner_principal`、`origin_session_id` |
| `evicted_embeddings` | **192** | `id`（对应轮次 id，非外键）、`model`、`embedding_json`、`embedding` BLOB |
| `evicted_turns_fts` | 0 | FTS5，`tokenize='trigram'`，`content='evicted_turns'`，3 个触发器 |
| FTS 影子表 | `_data` 289 / `_idx` 287，`_docsize` 0 | 正文已删但索引段没清 |

- 192 行向量全在 default 库，而该库 `evicted_turns` 是 0：**孤儿向量**，没有可挂的轮次；另外 4 个 persona 库是纯 schema 骨架（各 40 KB，两张表都 0 行）。
- `embedding_json` 是**死列**：192 行全是空串，`json.loads` 直接抛异常；数据只在 `embedding` BLOB 里。迁移只搬 BLOB，整列丢弃。

### 4.3 conversation.db —— 会话与轮次

195.1 MiB；`sqlite_master` 里 33 个 table 条目 = **32 张实表 + `sqlite_sequence`**；`user_version=39`；59 个索引；`journal_mode=delete`、`freelist_count=0`。

| 表 | 行数 | 关键列 |
| --- | --- | --- |
| `sessions` | 145 | 会话主表（DDL 名就是这 8 字符缩写）：`session_id`、`persona`、`kind`（user 132 / room-member 11 / subagent 1 / voice 1）、`parent_session_id`、`archived`、`sort_key`、`last_request_at`（INTEGER epoch） |
| `turns` | 2,158 | `turn_id`、`session_id`、`seq`、`user_content`、`assistant_content`、`assistant_reasoning`、`status`（completed 1928 / interrupted 230）、`is_summary` 3、`hidden` 217、`tool_reports`、`display_content`、`context_messages` |
| `turn_journal_events` | 27,666 | `turn_id`、`revision`、`segment_index`、`kind`（15 种）、`call_id`、`name`、`text_payload`、`blob_payload`、`ok`（三态：NULL 25,975 / 1 1,633 / 0 58） |
| `image_assets` | 112 | `asset_id`、`turn_id`、`tool_id`、`mime`（png 85 / jpeg 26 / gif 1）、`width`、`height`、`alt`、`data` BLOB |
| `artifact_assets` | 5 | `asset_id`、`turn_id`、`source_key`（绝对路径）、`file_name`、`mime`、`kind`、`size_bytes`、`data` |
| `accounts` | 2 | `id`、`username`、`display_name`、`password_hash`（117 字符）、`role`、`disabled` |
| `goals` | 2 | `session_id`、`goal_id`、`revision`、`objective`、`phase`、`blocked_code`、`rounds_started` |

- 体积构成：`image_assets.data` 159,767,665 B（**78%**），单张 607 B–5,374,998 B（5.4 MB）；`turn_journal_events.blob_payload` 719,763 B；`turn_redo_checkpoints.payload` 519,568 B；`artifact_assets.data` 92,564 B。**大 BLOB 内联在库里，做不了增量迁移**。
- 32 张实表里 **9 张为 0 行**：`platform_access_*` 2 张、`shared_files`、`sponsor_records`、`turn_inline_media`、`turn_redo_*` 4 张；`user_attachments` 39 行但 `data` 全 NULL（只有元数据）。
- 其余有数据的表：`turn_journal_segments` 232、`room_messages` 185、`session_loaded_items` 66、`session_reviews` 65、`user_attachments` 39、`queued_prompts` 27、`turn_tool_reports` 23、`question_exchanges` 20、`turn_redo_checkpoints` 20、`platform_meme_refs` 19、`room_participants` 11、`platform_session_bindings` 11、`platform_plugin_kv` 8、`app_state` 6、`subagent_trace` 3、`invites` 1。

### 4.4 ledger.db —— 记账（104 KiB，`user_version=2`）

| 表 | 行数 | 要点 |
| --- | --- | --- |
| `ledger_books` | 1 | `base_currency=CNY` |
| `ledger_accounts` | 5 | `kind`：ewallet 2 / cash 1 / credit 1 / bank 1 |
| `ledger_categories` | 15 | `direction`：expense 10 / income 5 |
| `ledger_entries` | 2 | 均 expense、CNY、`rate_status=same`、未删除；金额一律 `*_minor` 整数（分） |
| `ledger_budgets` / `ledger_rates` | 0 / 0 | 空 |

### 4.5 kb_meta.db —— 知识库索引（12 KiB，`user_version=0`）

只有 1 张表 `files`：`name`（主键）、`path`、`size_bytes`、`mtime`（REAL）、`content_sha256`、`updated_at`（REAL），13 行，与 `kb/files/**` 的 13 篇一一对应。`path` 存的是**带用户主目录的绝对路径**（`/Users/<user>/.gqy/...`），换机即失效——派生数据，迁移后建议按文件重建（ST2）。

### 4.6 platform onebot history.sqlite3（4.19 MiB，`user_version=4`）

| 表 | 行数 | 要点 |
| --- | --- | --- |
| `messages` | 6,046 | 会话四元组 `(platform,account_id,conversation_kind,conversation_id)`；`text`、`media_json`、`mentions_json`、`sender_id`、`is_bot`；`sent_at`/`recorded_at`/`recalled_at` 是 INTEGER epoch |
| `recalls` | 140 | 撤回记录；`messages.recalled_at` 非空 139 条，差 1 条 |
| `context_boundaries` | 1 | `persona_scope`、`after_row_id`、`reset_at` |
| `messages_fts` | 6,046 | FTS5 `(text, sender_name)`，**外部内容模式**挂在 `messages` 上，3 个触发器 |

分布：`conversation_kind` group 5642 / private 404；`is_bot=1` 268；不同 `sender_id` 231 个，不同 `conversation_id` **只有 5 个**。外部内容模式的库不能只 `ATTACH`+`INSERT`，搬完要 `INSERT INTO messages_fts(messages_fts) VALUES('rebuild')`。

## 5. 编码约定

1. **时间有两套表示**。主体是 RFC3339 + 6 位微秒 + 数字偏移，定长 32 字符，且**全部是 `+00:00`（UTC）**；但 `sessions.last_request_at`、`messages.sent_at`/`recorded_at`/`recalled_at`、`context_boundaries.reset_at`、`skill-drafts/draft.json.created_at` 是 **INTEGER epoch 秒**。不能全局 `strptime`，也不能把 `+00:00` 当本地时间再加 8 小时。`ledger_entries.occurred_at` 还混着带微秒与不带微秒两种写法。
2. **JSON 文本列形状不一**。`subjects` 是**对象数组**，元素键只有三种组合 `{name}` / `{name,principal}` / `{principal}`；`tags` 是字符串数组；`source_episode_ids` 是**整数数组**；`sessions.model_override` 是对象数组；`turns.tool_footprint` 是对象（键 `read` / `modified` 互斥出现）；`session_reviews.notes_json` 是字符串数组；`question_exchanges.payload` 固定三键。
3. **「JSON 列名但存裸文本」**。`turns.display_content` 295/300 不是 JSON（4 条是整数）；`app_state.value` 6 条全非 JSON。不能全局按 JSON 解析。
4. **向量是 512 维 float32**。`embedding` BLOB 定长 2,048 B = 512 × 4；按小端解包 `norm=1.0000`、`|v|≤0.33`、无 NaN。模型名 `local:bge-small-zh-v1.5-int8` 里的 `int8` **与载荷不符**，按 int8 反量化会得到垃圾向量。`content_sha256` 3,031 个全部不同。
5. **布尔是 INTEGER 0/1**（`archived`、`hidden`、`is_summary`、`is_bot`、`promotion_pending`）；`ok` 在三态里 NULL 不等于失败。
6. **主键两套**：记忆表用 `INTEGER PRIMARY KEY AUTOINCREMENT`；会话/记账用 `TEXT` 业务 id（`turn_id`、`session_id`、`entry_id`）+ 局部 `seq INTEGER`。`turns.seq` 不是全局序号（2,158 行只到 621，单会话最多 438 轮）。
7. **FTS5 两处**：`evicted_turns_fts` 与 `messages_fts`，都 `tokenize='trigram'` + 外部内容表 + 三个触发器。属派生数据，迁移不复制、只重建。
8. **结构版本靠 `PRAGMA user_version`**，而记忆/淘汰/知识库三个库是 0，等于没有版本信息——按列名探测，缺列给默认值。

## 6. 计数核对

与 `designs/18-记忆与迁移.md`「验收」第 1 条、`blueprint/migrate.md` §4 的报告口径对齐。数字全部来自磁盘上**那一个**可用记忆库（default）。

| 项 | 旧库位置 | 计数 | 迁到新库 | 结论 |
| --- | --- | --- | --- | --- |
| facts | `memory.db.facts` | 347 | `facts.jsonl` | 一致 |
| episodes | `memory.db.episodes` | 2054 | `episodes.jsonl` | 一致（long 1054 + short 1000） |
| revisions | `memory.db.memory_revisions` | 47 | `revisions.jsonl` | 一致 |
| embeddings | `memory.db.memory_embeddings` | 3031 | 挂到对应记录的 `embedding` | 一致（fact 347 + episode 2684） |
| evicted_turns | `evicted_context.db` | 0 | `evicted.jsonl` | 0 条，空文件 |
| evicted_embeddings | `evicted_context.db` | 192 | 无处可挂 | **孤儿**，报告记 `orphan_vectors=192` |
| 记忆内孤儿向量 | `memory_embeddings` vs `episodes` | **946** | 过滤后不迁 | episode 向量 2684 中 946 条找不到宿主 |
| 无向量的 episode | `episodes` | **316** | 原样迁，无 `embedding` | 允许缺向量 |
| pending_events / skill_records | `memory.db` | 0 / 0 | 空索引 | 一致 |
| conversation turns / journal | `conversation.db` | 2,158 / 27,666 | archive 组，默认不迁 | 只报体积 |
| platform messages | `history.sqlite3` | 6,046 | archive 组，原样复制 | 只报体积 |

核对结论：**347 / 2054 / 47 / 3031 / 0 / 192** 六个数字通过，可当验收基线；另外必须同时报 **946 条记忆内孤儿向量**与 **316 条无向量 episode**，否则「条数对上」是假象。这些数字只覆盖 default 库；清单里另外 3 个 `memory.db` 缺失，照清单迁会得到空库（第 8 节第 1 条）。

## 7. 隐私与脱敏

导出声明 `secrets_included: true`，整包应视为**高危明文凭据包**：备份根目录是 `0700`，但 `home/` 子目录是 `0755`（同机其他用户可读）。

**凭据（13 处非空）**

| 位置 | 数量 / 形态 |
| --- | --- |
| `providers[].api_key` | 6 个，全部非空 |
| `platforms.qq.access_token` / `platforms.connectors.imessage.token` | 各 1，长度 48 |
| `voice.tts.minimax.api_key` | 1，长度 126 |
| `plugins.image_generation.api_keys[]` / `plugins.api_quota.<provider>.accounts[].api_key` | 各 1 |
| `mcp.servers[].env.*` | 5 组，含 `NETEASE_COOKIE`（883 字符）、`BILIBILI_SESSDATA`、`TAPTAP_MCP_CLIENT_IDE` |
| `config/imessage.json` 的 `token` | 1 |
| 空置但存在同名字段 | `plugins.exchange_rate.api_key`、`plugins.map.amap_key`、`plugins.express.kuaidi100_key` |

**账号与身份**：`conversation.db.accounts`（`username` / `display_name` / `password_hash` 117 字符，2 行）、`invites.code_hash`；`platforms.qq` 的 `admin_users` / `owner_users` / 各 `whitelist` / `admin_aliases`（QQ 号即真实身份）；`connectors.imessage.contacts[].handles` 与 `state/imessage-contacts.json` 的键（真实姓名）；`messages.sender_id` / `platform_session_bindings`（OneBot 账号 id）；`tools.github.coauthor_email`。

**正文（一律 `<…>`）**：`facts.content`、`episodes.content`/`user_message`/`assistant_message`、`memory_revisions.old/new_content`、`evicted_turns.content`、`turns.*_content`/`assistant_reasoning`/`display_content`、`turn_journal_events.text_payload`/`blob_payload`、`image_assets.data`（截图 BLOB）、`documents/**`、`prompts/**`、`kb/files/**`、`memes` 图片、`repl-history.jsonl`、`profile.md`、`identities/*.md`、`room_messages.content`。

**路径类**：`kb_meta.files.path`、`artifact_assets.source_key`、`plugins.image_generation.output_dir`、`tools.sandbox.readable/writable`、`mcp.servers[].command/args`，以及备份目录名本身。

**迁移默认处理**（`designs/18-记忆与迁移.md` MG4、MEM6）：

1. 密钥一律不迁，改写成 `api_key_env` 引用并报告「已脱敏 N 项」；`--include-secrets` 才迁明文，目标 0600，报告仍不出现明文。
2. `password_hash` / `code_hash` 不迁——新版鉴权不走这套。
3. 正文按 `--kinds` 决定是否搬；搬了也只落新库文件（0600 / 目录 0700），不进日志、不进报告。
4. 迁移报告只含计数、路径、告警、脱敏项数，不含正文与密钥（MG8）。
5. 旧库一律 `mode=ro&immutable=1` 打开，源目录只读遍历，不写任何字节（MG1）。

## 8. 不规范项

这些是迁移器必须处理的坑，也是新版别重犯的清单。

| # | 现象 | 影响 | 迁移处理 |
| --- | --- | --- | --- |
| 1 | 清单声明 4 个 `memory.db`，磁盘只有 1 个；另 3 个连 persona 目录都不在 | 照清单迁得到 3 个空记忆库 | 以磁盘为准，缺失项进 `skipped` 并告警 |
| 2 | 三套 `home` 叠层 + **三处互不相同**的 `<user>` 段 | 假设同名会把记忆、淘汰、会话放错位置 | 按 unit 映射表逐条定点，不按前缀递归 |
| 3 | 7 个 `.DS_Store`，其中 3 个不在清单（根、`home/`、`<user>/`） | 污染计数，被当成「未声明文件」 | 一律跳过并计数 |
| 4 | 记忆库内**孤儿向量 946 条**，另有 316 条 episode 无向量 | 直接搬会破坏 id 一致性 | `LEFT JOIN` 过滤，数量进报告 |
| 5 | `evicted_context.db` 自相矛盾：turns 0 / embeddings 192 / FTS 影子表残留 289+287 | 「正文已删、向量还在」的幽灵片段 | 只搬 BLOB，FTS 不搬 |
| 6 | `embedding_json` 是死列（192 行空串） | 下游 `json.loads` 会崩 | 整列丢弃 |
| 7 | 模型名 `-int8` 与载荷（512 维 float32）不符 | 按 int8 反量化得到垃圾向量 | 按 float32 解释，dim=512 |
| 8 | 4 个空 persona 库 + 影子 persona：7 个会话指向磁盘上不存在的 persona | 迁完变悬空引用 | 补默认开关，悬空引用记告警 |
| 9 | 单库 204 MB，78% 是内联图片 BLOB，单张最大 5.4 MB，`freelist_count=0` | 无法增量/差异迁移，复制慢 | 归 archive 类，默认只报体积（MG7） |
| 10 | 32 张实表里 9 张 0 行；`user_attachments` 39 行但 `data` 全 NULL | 「迁成功」但业务是空的 | 报告区分「0 条」与「跳过」 |
| 11 | 同一列存两种类型：`display_content` 裸文本 + 整数，`app_state.value` 非 JSON | 统一 `json.loads` 会崩 | 先判类型再处理 |
| 12 | 时间戳两套表示（TEXT RFC3339 与 INTEGER epoch）混用 | 全局 `strptime` 会失败 | 按列判断 |
| 13 | 绝对路径写死：`kb_meta.path`、`artifact_assets.source_key`、`image_generation.output_dir` | 换机即失效 | 只迁相对部分，索引重建 |
| 14 | 配置与磁盘矛盾：`plugins.memes.persona_libraries` 是 `{}`，磁盘有 2 个表情库 | 以为没配就不迁 | 库是扫目录发现的，目录整棵拷 |
| 15 | 重复内容：草稿包里的 `SKILL.md` 与 `extensions/skills/` 那份字节数相同 | 导入两次会出现两份同名技能 | 按内容哈希去重 |
| 16 | 计数对不上：`sqlite_sequence` 高于行数（episodes 3000 vs 2054）；`messages.recalled_at` 139 vs `recalls` 140 | 按 id 续号会冲突；撤回记录缺一条 | 不重置 sequence；先 messages 后 recalls |
| 17 | `secrets_included: true` + `home/` 目录 `0755` | 误迁即泄漏，同机他人可读 | 默认脱敏（MG4），备份整包按凭据处理 |
| 18 | `profile.md` 16 B、`identities` 28 B、`usage.json` 计数疑似重复累计（37 亿 token / 6,897 请求） | 迁过去没有价值，还会污染新统计 | 可跳过；用量不迁或只作展示 |

## 9. 与新版口径的差异

1. **`visibility` 三值**：旧库是 `privileged` / `public` / `principal`；`blueprint/memory.md` 现在写的是 `privileged` / `normal` 并注明「沿用旧值」，实际旧值没有 `normal`。迁移按原值迁，映射规则待补。
2. **`memory_type` 多一个 `other`**：新枚举 6 值之外还有 1 条 `other`。迁移保留原值，读路径按未知类型兜底。
3. **会话表名不可读**：主会话表在 DDL 里是 8 字符缩写 `sessions`，而新版目录就叫 `$GQY_HOME/sessions/`——名字从这里来。映射表必须硬编码该表名，别按语义猜。
4. **时间与布尔不统一**：旧库 `+00:00` + 微秒 + 部分 INTEGER epoch、布尔用 0/1 且 `ok` 有三态；新版统一 RFC3339 带偏移、布尔真值。迁移要逐列转换，不能整库照搬。

## 10. 复核方式

只读打开，不写源。路径按需替换，别把真实备份路径写进任何提交物。

```bash
sqlite3 "file:<备份>/home/<user>/<persona>/memory/memory.db?mode=ro&immutable=1" \
  "select (select count(*) from facts),(select count(*) from episodes),
          (select count(*) from memory_revisions),(select count(*) from memory_embeddings);"
# 期望 347|2054|47|3031

sqlite3 "file:<备份>/home/state/<user>/<persona>/memory/evicted_context.db?mode=ro" \
  "select (select count(*) from evicted_turns),(select count(*) from evicted_embeddings);"
# 期望 0|192；另 4 个 persona 库是 0|0

jq '.entries|length, (map(.unit)|unique|length)' <备份>/manifest.json   # 期望 174 与 27
find <备份> -type f | wc -l                                            # 期望 173（比清单少 3 个库）
```

## 11. 相关文档

- 决定与验收口径：`../designs/18-记忆与迁移.md`（MEM1–MEM8 记忆、MG1–MG8 迁移）。
- 目标侧记录模型与召回：`../blueprint/memory.md`。
- unit 映射表、报告格式、仓库结构：`../blueprint/migrate.md`。
- 施工单：`../construction/026-记忆子系统.md`、`../construction/027-迁移器一次性插件.md`。
- 一次性插件形态：`../designs/15-一次性插件.md`、`../blueprint/oneshot.md`。
- 新版目录与原子写：`../blueprint/store.md`、`../designs/09-存储.md`。
