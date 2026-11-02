# memory · 记忆子系统蓝图

**状态**：施工中（2026-11-02）

**对应设计决定**：MEM1–MEM8（`18-记忆与迁移.md`）、ST2/ST4（`09-存储.md`）。

## 1. 目录与文件

```
$GQY_HOME/memory/
  meta.json                    # 库级元信息（0600）
  personas.json                # 人格清单
  <persona>/facts.jsonl
  <persona>/episodes.jsonl
  <persona>/revisions.jsonl
  <persona>/evicted.jsonl
```

- `<persona>`：`^[a-z0-9][a-z0-9_-]{0,31}$`；默认 `default`。
- 所有 JSONL 一行一条记录，UTF-8，`\n` 结尾；追加写，不原地改（MEM1）。
- 目录由 `storage.EnsureRoot` 创建（0700），与 `gateway/`、`sessions/`、`plugins/` 同级。

## 2. 记录字段

### 2.1 facts.jsonl

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `id` | int64 | 库内自增；重复 id 以最后一行有效 |
| `legacy_id` | string | 迁移来源键 `<unit>:<表>:<主键>`；原生写入为空 |
| `kind` | string | 固定 `fact` |
| `content` | string | 陈述正文 |
| `memory_type` | string | `fact` / `preference` / `self` / `correction` / `task` / `relationship` |
| `truth_status` | string | `accepted`（已确认）/ `reported`（转述） |
| `status` | string | `active` / `archived` / `superseded` |
| `importance` | int | 1–5，默认 3 |
| `confidence` | float | 0–1，默认 1 |
| `strength` | float | 衰减后的强度，默认 1 |
| `recall_count` | int | 被召回次数 |
| `last_recalled_at` / `last_decay_at` | string? | RFC3339 |
| `created_at` / `updated_at` | string | RFC3339，带偏移 |
| `visibility` | string | `privileged` / `normal`（沿用旧值） |
| `owner_principal` / `owner_display_name` | string | 归属；接口输出掩码 |
| `subjects` | object[] | 旧库是**对象数组**（`[{"name":…}]`），迁移后保持对象数组，未知字段原样保留 |
| `tags` | string[] | 旧库是字符串数组，直接迁移 |
| `source_episode_ids` | int64[] | 旧库是**整数数组**，迁移后仍是数字数组 |
| `origin_session_id` | string | 来源会话 |
| `source` | string | 来源标记（`migration:legacy-0.7` / `agent` / `api`） |
| `embedding` | object? | `{model,dim,encoding,data}`；`encoding` = `int8`/`float32`，`data` 为 base64 |

**向量编码陷阱（实测）**：旧库 `model` 写 `local:bge-small-zh-v1.5-int8`，但 3031 条向量实测都是 **2048 字节 = 512 维 float32（L2 归一 ≈1.0）**。编码**只能按 BLOB 长度判断**：`len == dim` → `int8`，`len == 4*dim` → `float32`；不许按 `model` 名字反量化，名字会骗人，按名字解码会静默产出垃圾。

### 2.2 episodes.jsonl

在 facts 公共字段基础上加：

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `retention` | string | `short_term` / `long_term` |
| `user_message` / `assistant_message` | string | 片段原文 |
| `expires_at` | string? | 短期片段过期时间 |
| `consolidated_at` / `promoted_at` | string? | 已固化 / 已提升为 fact |
| `promotion_pending` | bool | 待提升 |
| `source_key` | string | 长片段去重键（唯一） |
| `origin_kind` / `origin_platform` / `origin_account_id` | string | 来源分类 |
| `origin_conversation_kind` / `origin_conversation_id` | string | 来源会话类型与 id |
| `origin_sender_id` / `origin_sender_display_name` | string | 来源发送者 |
| `origin_message_id` | string | 来源消息 id |

### 2.3 revisions.jsonl

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `id` / `legacy_id` / `kind`(`revision`) | — | 同公共约定 |
| `memory_id` | int64 | 被修订的记录 id |
| `old_content` / `new_content` | string | 修订前后 |
| `source_episode_ids` | string[] | 依据的片段 |
| `created_at` | string | 时间 |

### 2.4 evicted.jsonl

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `id` / `legacy_id` / `kind`(`evicted`) | — | 同公共约定 |
| `source_id` | string? | 原轮次 id（旧库唯一） |
| `timestamp` / `created_at` | string | 时间 |
| `role` | string | `user` / `assistant` / `tool` |
| `content` | string | 原文 |
| `visibility` / `owner_principal` / `owner_display_name` / `origin_session_id` | — | 同公共 |
| `embedding` | object? | 可选 |

### 2.5 meta.json / personas.json

```json
{"schema_version":1,"database_id":"mem-<16hex>","generation":1,"access_schema_version":2,
 "updated_at":"2026-11-02T21:40:00+08:00",
 "migrated_from":{"source":"gqy-home-v0.7","source_fingerprint":"sha256:…","at":"…"}}
```

```json
{"personas":[{"id":"default","source":"legacy",
  "subsystems":{"memory":true,"skills":true,"persona_reminder":true,"voice":true,"emotion":true},
  "plugins":{"skills":[]}}]}
```

## 3. HTTP 接口（`/api/memory`）

全部要求鉴权（现有中间件）。错误沿用 `writeError(code, message)` 形状。

| 方法 + 路径 | 说明 | 返回 |
| --- | --- | --- |
| `GET /api/memory` | 人格清单与统计 | `{schema_version, personas:[{id, counts:{facts,episodes,revisions,evicted}, updated_at}]}` |
| `GET /api/memory/{persona}/stats` | 单库统计 | `{persona, counts, by_type:{…}, by_truth:{…}, last_updated}` |
| `GET /api/memory/{persona}/facts` | 列表 | `{items:[FactView], total, offset, limit}` |
| `GET /api/memory/{persona}/episodes` | 列表 | `{items:[EpisodeView], total, offset, limit}` |
| `GET /api/memory/{persona}/evicted` | 列表 | `{items:[EvictedView], total, offset, limit}` |
| `GET /api/memory/{persona}/revisions?memory_id=` | 修订链 | `{items:[RevisionView]}` |
| `POST /api/memory/{persona}/recall` | 召回 | `{query, items:[{id,kind,content,score,memory_type,importance,updated_at}]}` |
| `POST /api/memory/{persona}/facts` | 写入事实 | `{ok:true, fact:FactView}` |
| `PATCH /api/memory/{persona}/facts/{id}` | 归档 / 恢复 | `{ok:true, fact:FactView}` |

查询参数（列表）：`q`（词法过滤）、`limit`（默认 20，上限 200）、`offset`、`type`、`truth`、`status`、`include_archived`、`retention`。

`recall` 请求体：`{"query":"…","limit":5,"kinds":["fact","episode","evicted"],"include_archived":false,"query_embedding":{…}?}`

### 3.1 视图形状（脱敏）

```json
{"id":12,"kind":"fact","content":"…","memory_type":"preference","truth_status":"accepted",
 "status":"active","importance":4,"confidence":0.9,"strength":0.8,"recall_count":3,
 "created_at":"…","updated_at":"…","visibility":"privileged","owner":"B***t",
 "tags":["偏好"],"subjects":[],"legacy_id":"legacy:facts:12"}
```

- `owner` 用掩码；`owner_principal` 原值不出现在列表响应里。
- `content` 超 4000 字符截断并标 `"truncated":true`（召回同样截断）。

## 4. 召回打分

```text
lexical           = 命中词数 / 查询词数（0–1，空查询 0）
importanceFactor  = 0.5 + importance/10          # 1→0.6 … 5→1.0
recency           = max(0.3, exp(-ageDays/30))   # ageDays 由 updated_at 算
score             = lexical × importanceFactor × strength × recency
若带 query_embedding 且记录带 embedding：
score             = 0.65×lexical + 0.35×cosine   # 再乘 importanceFactor × strength × recency
```

- 分词：CJK 双字滑窗；ASCII 按 `[a-z0-9]+` 切词并小写；全部去重。
- 排序：`score` 降序 → `updated_at` 降序 → `id` 升序；`score` 四舍五入到 4 位小数。
- `kinds` 只影响参与打分的记录类型（默认三类全参与）。
- 召回后**不回写** `recall_count`（读路径不写盘；计数由显式 `memory_touch` 后置再做）。

## 5. agent 工具

| 工具名 | 参数 Schema | 返回 |
| --- | --- | --- |
| `memory_recall` | `{query:string, limit?:int(1–20, 默认 5), kinds?:string[]}` | JSON 数组字符串：`[{"id","kind","content","score","importance"}]` |
| `memory_remember` | `{content:string(1–2000), memory_type?:enum, importance?:int(1–5), tags?:string[]}` | JSON 字符串：`{"ok":true,"id":N}` |

- 两个工具都注册为**核心工具**（`SourceCore`），随 `ToolDecls()` 自动注入模型。
- 工具执行只写记忆库，不写会话文件；错误按 `tool.ErrInvalidArgument` 包装。
- `artifactsFor` 不登记（不产生文件产物）。

## 6. 计划中的测试

- `TestStoreAppendLoad`：写入 → 重新加载 → 记录数与字段一致。
- `TestStoreIdempotentLegacyID`：同 `legacy_id` 重复写不产生第二行。
- `TestRecallDeterministic`：同输入两次召回顺序完全一致。
- `TestRecallCJKBigram`：中文双字命中；英文大小写不敏感。
- `TestRecallImportanceDecay`：重要度与时间衰减对分数的影响单调。
- `TestArchiveViaRevision`：归档写 revisions + 新行，读时取最后一行。
- `TestHTTPMemoryEndpoints`：`/api/memory*` 六个端点（httptest + 临时 home）。
- `TestToolMemoryRecallRemember`：两个工具的 Execute 与参数校验。

## 7. 边界

- 单库 1 万条以内直接全量载入内存（约 20 MB 上限），超过后另立分段索引施工单。
- 写入并发用进程内 `sync.Mutex` 串行化；跨进程并发不在范围内（单机单进程）。
- 文件损坏（半行 JSON）：跳过该行并在 `meta.json` 旁记 `load_warnings`，不让启动失败。
