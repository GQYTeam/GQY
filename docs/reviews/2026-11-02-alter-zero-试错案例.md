# 2026-11-02 · alter-zero 试错案例精读

对象：[linuztx/alter-zero](https://github.com/linuztx/alter-zero)（Rust，Apache-2.0，v0.17.0），一个 RAM 高效的终端 AI 编码 agent harness（单二进制、ratatui+crossterm、tokio 单线程事件循环）。本次精读重点是它在设计/实现中留下的「现象 → 根因 → 修法」试错记录，只借思路不复制实现。锚点仓库 `docs/` 目录（110 余篇设计文档，多数文档自带 bug 复盘与字节级回归验证），聚合站 [DeepWiki](https://deepwiki.com/linuztx/alter-zero) 作交叉核对。

## 仓库概览

一行：alter-zero 是 Rust 写的终端内 AI 编码 agent，以「纯核心库 + 边界 I/O」分层、离线 dummy 后端 + 可替换真后端、每轮/每回合全量重发上下文的 agentic loop 为骨架，其 `docs/` 是罕见的「每个 bug 都带测量数字、根因链和回归门」的试错案例库。

## 精读清单

| 文档（仓库相对路径） | 一句话 |
| --- | --- |
| `README.md` | 产品全貌：四档权限、checkpoint、子代理、skills、MCP、hooks、`/compact`、`/context`、`/model` |
| `docs/design.md` | 总设计：数据流、关键类型、各子系统的边界与取舍（master 索引） |
| `docs/memory.md` | RAM 优化的四类大案：模型列表解析、图片粘贴、每轮重发图片、流式 write 缓存 |
| `docs/flicker.md` | 滚动提交与重绘必须进同一同步帧，否则输入框闪烁 |
| `docs/slow-stream.md` | 慢速 token 流下 box 跳动、无同步终端仍闪烁，repaint in place 的来历 |
| `docs/interrupt.md` | Esc 中断：绝不 join 阻塞线程（interrupt-lag 冻结） |
| `docs/overlay-repaint.md` | 只读浮层每帧全量重写杀死鼠标选择，改为 diff + 静止即零字节 |
| `docs/tool-view-performance.md` | Ctrl+O 大会话打开慢：原子切换 + 增量 transcript 缓存 + 预热 |
| `docs/checkpoint.md` | 每轮 git 快照回滚代码；作用域守卫与成本预检防卡死/防自我复利 |
| `docs/backtrack.md` | Esc-Esc 回溯：purge 重建防残留，按键与提示同源 |
| `docs/compact.md` | 上下文压缩 append-only：追加标记、派生时替换，不重写历史 |
| `docs/context-usage.md` | 上下文账目页：估计与真实分开，signature + memo 增量重算 |
| `docs/agent-context-gauge.md` | 子代理视图要报「屏上那个」代理的上下文，不是主的 |
| `docs/llm.md` | 真后端与 /model 选择器：并行拉取、超时与取消解耦、配置热读 |
| `docs/prompt-caching.md` | 提示缓存：字节级稳定前缀、每后端亲缘键、usage 用 provider 真账 |
| `docs/parallel-tools.md` | 并行工具批次：先公告全量、顺序执行、中断时完整回放 |
| `docs/unfinished-replies.md` | 空回复/截断回复不当答案：统一 stop reason + 有界续写 |
| `docs/agent-progress.md` | 子代理中间叙述结构化进上下文，轮询用游标增量 |
| `docs/subagents.md` | 子代理定义为磁盘 md 文件；tools 白名单在执行处强制 |
| `docs/tty-detach.md` | sudo 劫持 TUI：子进程必须新 session 脱离控制终端 |
| `docs/long-lines.md` | 折叠/截断以显示行为单位，hint 报真实展开量 |
| `docs/async-rewrite.md` | 事件循环改 tokio select!，帧调度器合并重绘（120fps 上限） |
| `CHANGELOG.md` | 版本级改动记录，与 docs 交叉印证 |

## 试错案例表

| # | 现象 | 根因 | 修法 | 对 GQY 的启发 |
| --- | --- | --- | --- | --- |
| 1 | Esc 中断后界面冻结 1–2 s 才恢复 | 单线程事件循环上 `cancel + join` 阻塞在真后端的阻塞网络读（3 s 超时兜底） | `abandon_inflight`：cancel + detach（绝不 join）+ 换新回复通道；阻塞网络操作移入独立 transport 线程，drain 每 ~50 ms 轮询 cancel；`StallAi` 测试替身做确定性回归 | 网关/插件的取消路径绝不 join 阻塞线程；超时是「停滞探测器」，不是「取消唤醒器」；用可注入 stall 后端把取消时延变成可测断言 |
| 2 | 打开一次 `/model` 永久 +8.7 MB RSS，重复打开继续涨 | 把 669 KB 模型列表整棵 `serde Value` 树解析，约 10× 膨胀；glibc 小分配碎片 drop 后不归还 | 记录以 `RawValue` 借用保存，逐条解码（峰值=单条树）；响应体读加 8 MiB 上限；全 provider 并行拉取共享一个 HTTP client | GQY 解析大列表（模型目录、插件清单）禁止先建全树再挑字段；「drop 了内存就会回来」是错觉，要用 RSS 不回落做回归门 |
| 3 | 粘贴截图后每发一条消息 RAM 阶梯上涨 ~14 MB，只涨不降 | 每轮重新读文件、base64、深拷贝消息、建请求 Value 树、reqwest 序列化双倍扩容；glibc 动态 mmap 阈值把大块锁进线程 arena | attachment 每会话编码一次、`Arc` 共享；所有 wire 请求按引用借消息；请求体在独立线程流式序列化进 64 KB 管道，永不整块建 body | GQY 转发大 JSON/图片时：编码一次共享、请求体流式写出；「每轮重建整个请求体」是长驻进程最贵的隐藏开销 |
| 4 | 流式回复时输入框每提交一行就闪一下 | `insert_before` 立即 I/O，清屏 `ESC[J` 落在同步更新块之外，呈现「无框中间态」 | 提交行只入 pending 队列，与重绘合并进同一 BSU/ESU 同步帧；光标 Hide/Show 只在最终落点；字节级断言（`tmux pipe-pane`）14/14 清除 → 0 | GQY 前端流式输出的重绘要「排队 + 单帧原子提交」；用原始字节流断言代替截图断言 |
| 5 | 慢速 token 流下 box 上下跳动；不支持 mode 2026 的终端每秒闪一次 | preview 行数随 markdown 语法状态收缩（围栏开/闭）；`ESC[J` 在半解析帧被无同步终端呈现 | repaint in place（blit 覆盖，只清残留行，零 `ESC[J`）；preview 加「不收缩」地板；差分 fuzz 承载不变量 | 流式 UI 的不变量写进差分测试（committed+preview==全量、preview 收缩≤本步提交）；对不支持同步更新的终端也要保底 |
| 6 | Ctrl+O/Ctrl+D 打开期间鼠标无法选中复制 | alt-screen 每帧全量重写 3000 cells（31–120 Hz），终端在被改写时丢弃选择 | overlay 也做 diff，未变化帧写 0 字节；动画时钟链在 overlay 下停摆 | 只读视图「静止即静默」；把写出的字节数当性能指标，而不是只看 CPU |
| 7 | 146 KB 大会话打开 Ctrl+O 要 ~270 ms，kitty 光标拖影 | 每次打开全量 rebuild 并语法高亮；先切 alt-screen 清屏、后构建 | 原子切换（排队不 flush）；transcript 冻结前缀 + 增量 + 循环底部预热 + 紧凑 slab 行缓存 | GQY 前端长会话/大列表视图做增量缓存与预热，别「打开时全量渲染」 |
| 8 | resume/回溯只回滚对话，代码留在最新状态，转录与代码不一致 | 两个回滚手势都只截断 history，没有「代码快照」维度 | 每轮结束对工作目录做独立 git store 快照，keyed by history 长度；`restore_target` 纯函数统一 resume 与回溯；恢复前先备份 | GQY 会话产物（S）的「回滚」要同时覆盖对话与副作用；快照要独立于用户 git、可恢复、可解释 |
| 9 | 在 `~` 下启动卡死数秒；store 在项目内自我快照复利膨胀 | 首帧前 `git add -A` 全量哈希 235 MB 树；checkpoints 根落在 work tree 内时每轮重拉入上一快照 | 作用域守卫（拒绝 home/系统/共享目录）+ 成本预检（git ls-files 统计新文件，~800× 便宜，超预算退役）+ 自我排除；启动时公告快照成本 | GQY 任何「全量快照/扫描」都要作用域守卫 + 成本预检 + 明确提示，不能悄悄卡死或悄悄失效 |
| 10 | 回滚后旧对话还留在屏上；按 Esc 想退出却进了编辑 | 普通 overlay 返回只 flush 不清屏；hint 文案与按键行为不同源 | 缩历史一律 purge 重建（清屏+scrollback）；hint 与按键 arm 共用同一 predicate | 键位多义时用单一 predicate 同时驱动提示与行为；「缩历史」类操作必须 purge 重建而非增量 flush |
| 11 | 一行 minified 文件吃满整个折叠单元格，`+1 lines` 说谎 | 按源行给预算、块无行预算；hint 数源行而非显示行 | 折叠以显示行为单位（3 行 + hint）；hint 数展开将增的显示行；首块跳过空行；JSON 行美化前先 round-trip 校验 | GQY 前端折叠/截断一律按显示行、hint 报真实展开量、空白行不占预算、重排版前校验不改内容 |
| 12 | 模型空回复或 output cap 截断时，turn 被当「完成」结束 | 只按「无工具调用=答案」判定；从不读各 wire 的 stop reason | 统一 `StopReason` 分类；空/截断轮注入 continuation prompt（最多连续 2 次）；仍无果给红错并点名原因 | GQY agent loop 把 stop reason 当一等信号；空回复重发同一请求是官方认证的错误，正确做法是新 user 消息续写 |
| 13 | 并行工具批次只显示当前一个；Esc 后模型忘了自己还叫过别的调用 | UI 只渲染当前 tool；中断时丢弃 waiting 兄弟 | `ToolBatch` 先行公告全量（Running/Waiting 同屏，顺序执行）；中断/错误时全队列 resolve 为 failed 并按 provider call id + position 完整回放 | 展示与执行解耦：顺序执行但全批次可见；取消时要完整记录模型已发出的意图，否则下一轮上下文失真 |
| 14 | bash 工具跑 `sudo` 时密码提示直接画到 TUI 上，挂到超时 | 子进程继承 TUI 控制终端，sudo 开 `/dev/tty` 而非读 stdin | 子进程进新 session（无控制终端），`/dev/tty` 打开即失败、毫秒级报错进日志；因 `forbid(unsafe)` 造出 setsid→helper re-exec→attached 三级链 | GQY 沙盒（SEC）执行工具必须让子进程脱离控制终端，失败要快、可读、可进日志；安全约束会倒逼出优雅的降级链 |
| 15 | ChatGPT 轮次缓存 0 命中；`~` 别名模型每轮全价；重建后端后缓存前缀失配 | 亲缘键放错位置（ChatGPT 忽略 body 键）；别名漏出前缀表；每次重铸 call id + 批拆分打散字节级前缀 | 每后端各自亲缘键（body key / session_id / headers）；别名入前缀表；持久化 call id/position + 保留上一请求前缀做恢复 | GQY 路由（R）要多 provider 实测（live tests）而非文档推断；缓存命中要求字节级稳定前缀；usage 用 provider 自己的账、snap 不 add |
| 16 | 子代理视图显示主代理的上下文（23.7k vs 真实的 64.9k）；`/context` 重算卡 55 ms | footer 不问「哪个会话在屏上」；每 commit 全量 tokenize | `context_gauge` 只报屏上会话；signature + memo 增量重算（hash 未变不重算，新文本才进 tokenizer） | GQY 用量/上下文统计要归属正确（谁在屏上就报谁）+ 增量重算；统计页必须与实际请求同源，不能另造一套 |
| 17 | 子代理的中间叙述没人看到，`agentoutput` 把词丢掉 | forwarder 每次 tool 事件清空累积文本；activity 行只显示 call；轮询每 50 ms 全量拷贝历史 | `ProgressUpdate` 结构化记录 + `Agent:` 行 + 游标增量拉取（只读新条目） | GQY 子任务/插件的中间进度要结构化进上下文与轮询，用游标增量，别每 poll 全量拷贝 |
| 18 | `/compact` 重写历史会连带破坏转录、checkpoint、回溯 | 单份 history 被渲染/录制/checkpoint/回溯/上下文派生多方共用，原地替换代价不可控 | append-only：只追加 `Compaction` 标记，替换发生在「派生时」；自动压缩按 90% 阈值、每用户轮至多一次 | GQY 会话存储被多处引用时，压缩/重写用「追加标记 + 派生时替换」，保持下游零迁移 |

## 对 GQY 的采用 / 不采用

| 能力/教训 | 采用 | 落到 GQY 哪层 | 说明 |
| --- | --- | --- | --- |
| 取消路径 detach 不 join + stall 替身回归 | 是 | R 网关 / P 插件运行时 | 插件或上游卡住必须可即时取消，超时只是兜底 |
| 大列表逐条解码、请求体流式写、attachment 编码一次共享 | 是 | R 网关 / S 会话与产物 | 长驻进程的 RAM 是功能不是偶然；加 RSS 回归门 |
| 渲染「排队 + 单帧原子提交」、只读视图 diff 后静止即静默 | 是 | FE 前端界面 | 流式输出与浮层重绘都要字节级可断言 |
| 回滚同时覆盖对话与副作用（快照 keyed by 历史长度） | 是 | S 会话与产物 | GQY 的会话回滚要与产物/文件副作用一致 |
| 快照/扫描的作用域守卫 + 成本预检 + 显式公告 | 是 | S / T 工具 | 防 `~` 下卡死、防自我复利 |
| stop reason 一等化 + 有界续写 | 是 | R / T 工具 | 空回复重发同请求是错的 |
| 并行批次先公告全量、顺序执行、中断完整回放 | 是 | T 工具 / FE | 展示与执行解耦 |
| 缓存亲缘键按后端实测、前缀字节级稳定、usage snap 不 add | 是 | R 网关与路由 | 直接对应 GQY 的多 provider 路由与配额 |
| 上下文/用量统计增量重算 + 归属屏上会话 | 是 | FE / A 鉴权与配额 | `/context` 类页面按此实现 |
| 压缩 append-only、派生时替换 | 是 | S / ST 存储 | 保护下游派生与 checkpoint 键 |
| 折叠/截断按显示行、hint 报真实展开量 | 是 | FE 前端界面 | 长输出、diff、日志折叠通用规则 |
| 子代理定义为磁盘 md 文件、tools 白名单在执行处强制 | 是（简化） | P 插件运行时 | GQY 的子任务/插件清单适合外置定义、执行时再验 |
| ratatui/crossterm 的终端实现细节（BSU/ESU、OSC 8、kitty 图片协议） | 否 | — | GQY 是 Go + 非终端 TUI 界面，机制不同，只取「原子帧、diff、零字节」原则 |
| Rust 特有实现（`forbid(unsafe)`、serde RawValue、glibc arena 分析） | 否 | — | Go 的分配器与 JSON 策略不同；取「测量 RSS、别建全树」的结论而非代码 |
| 单二进制终端 agent 形态、离线 dummy 后端 | 否 | — | GQY 是网关，不服务终端，不需要 dummy 演示后端 |
| 提示缓存每 provider 的显式 breakpoint 细节 | 部分 | R | GQY 若直连 Anthropic/OpenRouter 才需要；先做「前缀稳定 + 亲缘键」骨架 |

## 来源链接

- 仓库首页：<https://github.com/linuztx/alter-zero>
- 仓库 docs 目录：<https://github.com/linuztx/alter-zero/tree/main/docs>
- DeepWiki 聚合：<https://deepwiki.com/linuztx/alter-zero>

逐文档（本报告引用的均为 `main` 分支快照，2026-11-02）：

- <https://github.com/linuztx/alter-zero/blob/main/docs/design.md>
- <https://github.com/linuztx/alter-zero/blob/main/docs/memory.md>
- <https://github.com/linuztx/alter-zero/blob/main/docs/flicker.md>
- <https://github.com/linuztx/alter-zero/blob/main/docs/slow-stream.md>
- <https://github.com/linuztx/alter-zero/blob/main/docs/interrupt.md>
- <https://github.com/linuztx/alter-zero/blob/main/docs/overlay-repaint.md>
- <https://github.com/linuztx/alter-zero/blob/main/docs/tool-view-performance.md>
- <https://github.com/linuztx/alter-zero/blob/main/docs/checkpoint.md>
- <https://github.com/linuztx/alter-zero/blob/main/docs/backtrack.md>
- <https://github.com/linuztx/alter-zero/blob/main/docs/compact.md>
- <https://github.com/linuztx/alter-zero/blob/main/docs/context-usage.md>
- <https://github.com/linuztx/alter-zero/blob/main/docs/agent-context-gauge.md>
- <https://github.com/linuztx/alter-zero/blob/main/docs/llm.md>
- <https://github.com/linuztx/alter-zero/blob/main/docs/prompt-caching.md>
- <https://github.com/linuztx/alter-zero/blob/main/docs/parallel-tools.md>
- <https://github.com/linuztx/alter-zero/blob/main/docs/unfinished-replies.md>
- <https://github.com/linuztx/alter-zero/blob/main/docs/agent-progress.md>
- <https://github.com/linuztx/alter-zero/blob/main/docs/subagents.md>
- <https://github.com/linuztx/alter-zero/blob/main/docs/tty-detach.md>
- <https://github.com/linuztx/alter-zero/blob/main/docs/long-lines.md>
- <https://github.com/linuztx/alter-zero/blob/main/docs/async-rewrite.md>
