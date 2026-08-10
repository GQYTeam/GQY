# GQY 技术详解:Coding 能力、Agent 架构、底座与缓存策略

> 版本:v0.9.2 | 本文档只描述现状,不涉及改动建议。

GQY 定位是「桌面 AI 助手」,不是专职 Coding Agent。但她保留了相当完整的
代码相关能力:文件读写、补丁编辑、子任务派发、MCP 接入、自主 Agent 集群。
本文从四个维度拆解现状,帮助理解「用它来 Coding 到底行不行、怎么工作的」。

---

## 一、Coding 能力现状

### 1.1 直接可用的代码工具

| 工具 | 作用 | 说明 |
|---|---|---|
| `read_file` | 按行号分页读文件 | 支持绝对/相对/`~/` 路径,大文件分页,二进制拒绝 |
| `write_file` | 写文件 | 走补丁预览(`write_with_patch_preview`) |
| `edit_file` | 按行号区间替换 | 1-based 闭区间,替代后重新读取确认 |
| `edit_string` / `edit_replace` | 字符串编辑 | 精确文本替换 |
| `apply_patch` | 批量补丁 | **复杂编辑首选**;多文件/同文件多处修改用 diff 风格补丁 |
| `list_directory` / `glob` / `grep` | 探索代码库 | grep 支持正则,递归搜索 |
| `trash_path` | 删除(进回收站) | 不用 `rm`,可恢复 |
| `run_command` | 执行 shell 命令 | 受 `skills.allow_command_execution` 开关控制 |
| `task` | 子 agent 派活 | 探索/通用/研究员三种预设子代理 |
| `todowrite` | 任务清单 | 长任务拆步骤,模型自己维护进度 |
| `mcp` | 外部 MCP 工具接入 | JSON-RPC 2.0 over stdio,自定义服务器 |

### 1.2 Coding 定位上的刻意取舍

- 系统提示词(gqy.md)明确「不是专业 Coding Agent」,人格上偏聊天/排障/娱乐;
- `~/Desktop/GQY`(项目源码目录)有**硬护栏**:写文件工具会拦截对该目录的写入,
  防止她改自己源码;临时文件引导去 `~/gqy-workspace`;
- 工具集偏「日用系统助手」:天气、汇率、表情包、玄学、生图、语音、闹钟等
  大量非代码工具混在同一注册表,模型靠工具描述自行挑选。

### 1.3 与专职 Coding Agent 的差距(现状)

| 维度 | GQY 现状 | 专职 Coding Agent(如 pi 底座模式) |
|---|---|---|
| 补丁能力 | `apply_patch` 有,单文件为主 | 多文件、重构级补丁 |
| 探索 | read/glob/grep 基础齐全 | 更系统的代码理解流程 |
| 子任务 | `task` 三预设 + 自主 agent 集群 | 深度规划-执行循环 |
| 心智 | 每轮独立上下文 + 记忆联想 | 长会话内多轮连贯规划 |
| 开关 | `allow_command_execution` 默认关 | 默认开 |

---

## 二、Agent 架构

### 2.1 三种对话模式(历史隔离)

```rust
pub enum AgentMode { Normal, Plan, Chat }
```

- **Normal**:全工具,正经对话/任务;上下文 = 当前会话全部可见轮次;
- **Plan**:只读工具子集(`run_command` 只读变体 + read/grep 等),不写文件;
- **Chat**:12 轮窗口(可配置 `chat_history_turns`),女友人格,默认不注册工具
  (`chat_pure_text`),杜绝工具循环。

三条线在 SQLite 按 `mode` 列隔离,互不污染。

### 2.2 主循环(单 agent,非多轮规划器)

主循环是**单轮驱动**的 `chat_with_tools`:

```
用户消息
  → 组装 messages(系统人格 + 会话摘要 + 历史轮次 + 记忆联想 + 运行时上下文 + 当前输入)
  → 循环:调用模型 → 若返回工具调用 → 执行工具 → 把结果塞回消息 → 再调模型
  → 直到模型输出纯文本回复(或到达 max_rounds)
  → 每轮结束:写日记记忆、可选 finetune 样本、上下文溢出检查
```

- 工具循环上限 `tools.max_rounds`(默认 0 = 无限制;设正数则到顶后提示「可设为 0 以允许无限工具调用」);
- 流式输出经 `ReasoningTitleFilter` 等过滤器清洗;
- 问题澄清(`ask_question`)、队列(`queue`)、取消、溢出压缩都是这个循环的附属能力。

### 2.3 自主 Agent 集群(agents.rs,Kimi 式)

模型可以在对话中**自主创建命名 agent**:

- 工具:`spawn_agent` / `talk_to_agent` / `list_agents` / `destroy_agent` 等;
- 每个 agent 是独立 LLM 会话(独立 system prompt = 角色定义);
- 定义持久化在 `GQY_HOME/data/agents/agents.json`,重启后仍在,进程懒启动;
- 上限 16 个 agent,每 agent 历史 20 轮;
- **递归防护**:agent 自己的工具清单不含 spawn/talk_to,防止无限套娃。

两种底座下实现不同:
- **pi 底座**:每个 agent = 独立 pi 进程(自定义系统提示词 → 独立进程);
- **直连底座**:OpenAI 兼容客户端 + 实例内消息历史。

### 2.4 子 agent(task 工具)

预设三种 system prompt 的子代理,干一次性探索/研究活:

| 子代理 | 工具白名单 | 用途 |
|---|---|---|
| `subagent-explore` | read_file/glob/grep/check_os_info/web_* | 只读探索代码库/系统 |
| `subagent-general` | 排除 task/deep_research/meme/alarm 等 | 通用子任务 |
| `subagent-researcher` | 偏 web 研究 | 资料检索 |

---

## 三、底座(LLM 提供方)

### 3.1 两种协议(provider.protocol)

```rust
enum LlmClient {
    OpenAi(OpenAiCompatibleClient),   // protocol = "auto"/"openai"
    Pi(PiRpcClient),                  // protocol = "pi"
}
```

**A. OpenAI 兼容直连(默认)**
- 任意 `base_url` + `api_key`,自动 `GET /models` 发现模型;
- 供应商热切换:对话里说「帮我加个供应商」即可,配置写 `config.jsonc`;
- 流式 SSE + 工具调用(JSON function calling)。

**B. pi 底座(RPC)**
- 通过 `pi --mode rpc` 把「大脑」交给 pi:
  - pi 自己跑 agent 循环与内置工具(read/write/edit/bash/find/grep/ls);
  - GQY 把每轮消息经 JSONL 发给 pi,把流式事件翻译成自己的渲染层;
  - 会话状态在 pi 进程内(`--no-session` 内存态),GQY 的记忆/备份照旧;
- 进程生命周期:system prompt 不变则复用同一 pi 进程(pi 维护对话历史);
  切人格/子 agent/压缩则换新进程,天然隔离;
- 单轮等待上限默认 30 分钟(`GQY_PI_TURN_TIMEOUT` 可调);
- 事件环形缓冲 8192 条。

**当前默认**:`active_provider = opencode-go`(opencode 公共模型服务,
模型如 `deepseek-v4-flash`),即直连模式;pi 底座是可选协议,配置 provider 时
设 `protocol: "pi"` 启用。

### 3.2 模型元数据缓存(models_cache)

- 启动时从 `https://models.dev/api.json` 拉取全量模型元数据
  (上下文窗口、多模态、限速、reasoning 选项等),本地缓存;
- 供应商热切换时据此自动补全模型能力,如上下文窗口大小;
- 缓存失败回退到本地默认表(`default_models.rs`)。

---

## 四、缓存与上下文策略

### 4.1 Token 估算(三层)

```
estimate_tokens(text)
  ├─ tiktoken(o200k_base 精确 BPE)   ← 首选,编译期内嵌压缩词表
  ├─ 回退:字符规则(CJK 2 字符/token,拉丁 4 字符/token)
  └─ 至少计 1
```

- `o200k_base` 词表在构建时压成二进制(`build.rs` → `OUT_DIR/o200k_base.bin`),
  运行时直接反序列化,零外部依赖;
- 图片按固定估算:`IMAGE_TOKEN_ESTIMATE = 765` token/张;
- 聊天模式历史窗口:`chat_history_turns`(默认 12)。

### 4.2 上下文溢出管理(三层防线)

```
上下文组装(load_visible_turns)
  → 进入回合前:trim_visible_context(按比例裁剪)
  → 回合结束后:handle_overflow_after_turn(压缩或弹出)
  → 兜底:evict(逐出到归档库,摘要保留)
```

**A. 修剪(回合前)** — `trim_visible_context`
- 需要知道当前模型上下文窗口(来自 models_cache);
- 阈值:`context_window × trim_at_ratio`(默认 0.9,即 90% 触发);
- 目标:`context_window × (1 - trim_batch_ratio)`(默认 15%),把最旧的可逐出轮次移出。

**B. 压缩(回合后)** — `compact`(`on_overflow = "compact"`,默认)
- 用同一模型把旧对话压成摘要,写回为 `is_summary` 轮次;
- 预留 10% 上下文(`RESERVED_RATIO`)+ 至少 4096 token 给压缩本身;
- 最多 5 轮合并(`MAX_MERGE_ROUNDS`),压缩消耗 token 计入用量。

**C. 弹出(替代方案)** — `on_overflow = "pop"`
- 直接丢最旧轮次(比压缩省,但丢信息)。

**D. 逐出归档(长期)** — evicted_turns
- 被逐出的轮次进独立归档库(`evicted_context_*`),保留语义可检索
  (memory 里 `search_evicted_context`)。

### 4.3 记忆缓存(不是上下文,但影响连续性)

- `facts`(长期事实,`remember_fact` 工具)与 `episodes`(每轮自动日记);
- **联想注入**:每轮按当前输入关键词,SQLite 本地检索最近 1000 条记忆,
  命中前 N 条以 `<associative-memory>` system 消息注入上下文;
- 强化/遗忘:`recall_count` + `strength`(越常想起越稳固),长期不唤起则衰减
  至 forgotten;
- 全部本地,零模型开销。

### 4.4 会话级缓存

- WebUI 用量统计、余额查询有 60s 防抖缓存;
- 对话历史全量在 SQLite(`conversation.db`,WAL 模式),每轮落库;
- 记忆快照:每轮对话后可触发 Git 快照(`backup`),绑定私有远程自动推送。

---

## 五、用它 Coding 的实操结论

| 场景 | 表现 |
|---|---|
| 读代码、搜代码、改单文件 | ✅ 完全可用(read/glob/grep/edit/apply_patch) |
| 多文件重构 | ⚠️ 能做但弱(补丁单文件为主,靠多轮工具调用拼) |
| 跑测试/命令 | ⚠️ 受 `allow_command_execution` 开关限制(默认关) |
| 长任务规划 | ⚠️ 有 task/todowrite/agent 集群,但非深度规划器 |
| 深度编码 | ✅ **切换到 pi 底座** 得到完整 pi agent 循环 |
| 保护项目源码 | ✅ `~/Desktop/GQY` 写护栏 |

**一句话**:GQY 直连模式是「能改代码的日常助手」;要当专职编码 agent,
把 provider 切成 `protocol: "pi"` 底座,把 agent 循环交给 pi。
