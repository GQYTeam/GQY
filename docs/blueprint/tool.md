# tool.md · 工具蓝图

## 1. 状态

待施工（2026-11-02）。

## 2. 对应设计决定

兑现 `06-工具.md` 的 T1–T10：

- T1 工具契约：一次调用六要素（name / schema / arguments / result / duration_ms / status），结果统一为文本 + 状态。
- T2 来源与注入：核心工具自动注入，外部工具默认关。
- T3 注册表：工具名全局唯一，重名报错、注销幂等，核心 / 外部两份列表。
- T4 核心工具职责与边界：read / edit / shell / list_dir / websearch / ask_user。
- T5 read 与 edit 边界：10 MB 上限、图片 data URI、原子写、保留权限。
- T6 shell 边界：固定 workspace、60s 超时、16 KB 输出上限、进程组回收、可选白名单。
- T7 ask_user：question + options（2–4），暂停 loop，数字键 / 自定义答案。
- T8 外部工具与开关：provider_usage + 插件动作，`GQY_ENABLE_EXTERNAL` 与 `/api/tools/external`。
- T9 工具轨迹：一次调用对应一个 05 S2 `tool` 事件与一条 jsonl 日志。
- T10 沙盒指针：落地规则见 SEC2–SEC4。

## 3. 具体形状

### 工具调用契约六要素

```json
{
  "name": "read",
  "schema": {"type": "object", "properties": {"path": {"type": "string"}}, "required": ["path"]},
  "arguments": {"path": "README.md"},
  "result": "…文件内容…",
  "duration_ms": 12,
  "status": "ok"
}
```

- 最小契约：名称、描述、参数 JSON Schema、执行、来源标记。执行语义：执行(上下文, 参数) → (结果文本, 错误)。
- `status` 四取值：`ok` / `error` / `timeout` / `canceled`。
- `status != ok` 时 `result` 为空，改由 `error` 字段携带错误文本。
- 模型只看到 `result` 或 `error`；六字段进工具轨迹（T9）与日志（05 S5）。
- 统一错误码：`invalid_argument` / `not_found` / `timeout` / `canceled` / `call_failed`（失败时给模型回填 `error` 文本）。

### 核心工具职责与边界表

| 工具 | 参数 | 默认与上限 | 职责与边界 |
| --- | --- | --- | --- |
| `read` | `path`（必填，string） | 文本上限 10 MB，超过报错不整读 | 读文件；图片（png / jpg / jpeg / gif / webp）转 data URI；只读允许根 |
| `edit` | `path`、`content`（均必填） | 新文件 0644；无大小上限，但内容一次性给全 | 覆盖写；同目录临时文件 + rename 原子写；保留已有权限；自动建父目录；只写允许根 |
| `shell` | `command`（必填，string） | 默认超时 60s；输出上限 16 KB；白名单默认关 | 工作目录固定 workspace；超时 / 取消杀进程组；白名单开时只许列表内命令并拒绝元字符 |
| `list_dir` | `path`（可选，string） | 默认 `.` | 列目录项，每行一项；目录项以 `/` 结尾；只读允许根 |
| `websearch` | `query`（必填，string） | 后端未配置即报错 | 网络搜索；不编造结果 |
| `ask_user` | `question`（必填）；`options`（可选，2–4 个） | 无 | 暂停 loop 向用户提问；数字键 / 自定义答案；对接 05 S2 `question` 事件 |

- 六者全部标记为核心，自动注入。
- `write_artifact` 也是核心工具，但归产物域（05 S3）：单文件 ≤ 2 MB、一次最多 20 个文件、总量 ≤ 2 MB，先整体校验再写，不留半截产物。

### 外部工具注入与开关

- 外部工具两个来源：`provider_usage`（无参数，返回可用性 / 货币 / 余额，低余额带提示）+ 插件动作（04 P11）。
- 注入开关：`GQY_ENABLE_EXTERNAL=1` 开启；运行时接口 `/api/tools/external` 读写；受 08 A1 / A3 鉴权（Bearer / cookie，未设令牌仅本机）。
- 关闭开关 = 模型工具列表不出现任何外部工具；已注册的外部工具仍可通过 HTTP 单独调试调用。

```json
GET  /api/tools/external            → {"enabled": false}
POST /api/tools/external            body {"enabled": true} → {"enabled": true}
```

### 工具事件字段（对应 05 S2 tool 事件）

```json
{"type": "tool", "name": "edit", "arguments": {"path": "a.txt", "content": "hi"}, "result": "已写入 a.txt（2 字节，权限 0644）", "duration_ms": 8, "status": "ok"}
```

- 事件字段：`type`、`name`、`arguments`、`result`、`error`（可选）、`duration_ms`、`status`。
- 与契约六要素一一对应；`schema` 不随事件发（在注册表里）。
- 落盘按 05 S5：`logs/tools/<日期>.jsonl` 一行一条，字段至少 timestamp、tool_name、status、duration_ms、input_summary、output_summary、artifact_paths；敏感参数脱敏。

### 沙盒规则落地（SEC2 / SEC3 / SEC4）

| 环节 | 落地规则 | 来源 |
| --- | --- | --- |
| 最小权限 | read / list_dir 只读允许根；edit 只写允许根；shell 只有 workspace + 白名单；websearch 只出站网络。允许根 = workspace + `$GQY_HOME/sessions` + `GQY_TOOL_ROOTS`；拒绝路径优先于允许根；默认没有，不是全盘 | SEC2 |
| 文件路径解析 | 固定顺序：绝对化（相对路径以 workspace 为基准）→ Clean 去 `..` → 从最近已存在祖先 EvalSymlinks → 接回剩余段 → **先查拒绝路径，再查允许根** | SEC3 |
| 写目标符号链接 | edit 最终目标是符号链接 → 拒绝，不跟随写入 | SEC3 |
| shell 边界 | cwd 固定 workspace，参数不可改；默认 60s 超时；16 KB 输出上限（截断标记）；超时 / 取消杀整个进程组；白名单按首 token 的 basename 匹配 | SEC4 |
| 白名单防绕过 | 白名单非空时拒绝 `;` `|` `&` `>` `<` 反引号 `$` `(` `)` 换行等元字符，防「安全命令 + 串联」绕过 | SEC4 |

## 4. 边角情况

- **重名注册**：返回错误，不静默覆盖；插件工具重名 → 插件加载失败回滚（04 P11）。
- **注销幂等**：注销不存在的工具返回「未找到」，不报错、不 panic。
- **参数 schema 校验失败**：参数不符合 Schema 或缺必填字段 → 返回 `invalid_argument`，不执行工具。
- **shell 超时杀组**：到 60s 杀整个进程组（含子进程），返回 `timeout` 与已收集输出；再等 2s 兜底回收，不残留孤儿。
- **ask_user 取消**：用户在提问界面取消 / 停止 → 本轮回复终止，已产出增量保留（05 S4），该工具状态记 `canceled`。
- **外部工具失败不拖垮核心**：外部工具报错只影响本轮结果；核心工具与后续 loop 不受影响；开关关闭后模型看不到外部工具。

## 5. 计划中的测试

- read：大文件超限报错；图片返回 data URI；`../`、符号链接逃逸、拒绝路径命中三种越界被拒。
- edit：原子写不产生半截文件；保留原权限；自动建父目录；目标为符号链接时拒绝。
- shell：超时杀整组无孤儿；输出截断带标记；白名单拒绝元字符与未列入命令。
- list_dir：目录项以 `/` 结尾；越界目录被拒。
- websearch：后端未配置时返回明确错误，不返回编造内容。
- ask_user：暂停 loop；`question` 事件带选项；数字键与自定义答案都能回填；取消记 `canceled`。
- 注册表：重名注册报错；注销不存在幂等。
- 外部工具：开关关闭时模型工具列表无外部工具，开启后 provider_usage 与插件动作出现；外部工具失败不影响核心工具。

## 6. 对应代码

待建。旧实现仅作事实线索（蓝图不照抄）：

- `app/internal/tool/`：`tool.go` / `builtin.go` / `ask.go` / `artifact.go` / `provider_usage.go` / `sandbox.go`。
