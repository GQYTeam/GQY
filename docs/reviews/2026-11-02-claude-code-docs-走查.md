# 2026-11-02 · Claude Code 官方文档走查

对象：[code.claude.com/docs](https://code.claude.com/docs)（Claude Code 官方文档，英文主站，Mintlify 托管；正文同时提供 `*.md` 形态供精读）。本次只提炼对 GQY 有用、可借鉴（不复制）的功能设计与试错案例，对照 GQY 现状（designs 00/01/06/14/15 + construction/README 进度索引）判断借鉴程度。所有结论附来源 URL。

## 精读范围

| 页面 | 一句话摘要 |
| --- | --- |
| [How Claude Code works](https://code.claude.com/docs/en/how-claude-code-works) | agentic loop（收集上下文 → 行动 → 验证）、工具五类、会话/分支/压缩/检查点总览 |
| [How Claude remembers your project](https://code.claude.com/docs/en/memory) | CLAUDE.md 层级与加载序、`@path` 导入、`.claude/rules/` 路径作用域规则、auto memory（MEMORY.md 索引 + 主题文件） |
| [Hooks reference](https://code.claude.com/docs/en/hooks) | 全生命周期事件（约 30 个）、matcher/`if` 过滤、exit code 与 JSON decision control、updatedInput/updatedToolOutput/additionalContext |
| [Hooks guide](https://code.claude.com/docs/en/hooks-guide) | 钩子自动化用例：格式校验、通知、命令阻断 |
| [Configure permissions](https://code.claude.com/docs/en/permissions) | 分层权限规则 deny→ask→allow、复合命令拆分、wrapper 剥离、参数级匹配、工作目录与工作区信任 |
| [Permission modes](https://code.claude.com/docs/en/permission-modes) | manual/acceptEdits/plan/auto/dontAsk/bypassPermissions 六档模式与默认 |
| [Configure the sandboxed Bash tool](https://code.claude.com/docs/en/sandboxing) | OS 级沙盒（文件/网络/凭据边界）、auto-allow、unsandboxed retry 逃生舱、excludedCommands、已知不兼容清单 |
| [Create custom subagents](https://code.claude.com/docs/en/sub-agents) | 子代理定义文件与 frontmatter、作用域优先级、上下文隔离、前后台、嵌套深度与并发上限、输出扫描 |
| [Extend Claude with skills](https://code.claude.com/docs/en/skills) | SKILL.md 与 description 驱动按需加载、`!` 动态上下文注入、`context: fork`、skillOverrides、插件 eval 基线对比 |
| [Keep Claude working toward a goal](https://code.claude.com/docs/en/goal) | `/goal` 完成条件循环：独立评估模型三态判定、错误分类、后台工作延迟评估与 check-in |
| [Explore the context window](https://code.claude.com/docs/en/context-window) | 上下文加载时间线、compaction 后各机制的存活清单、填满后的应对 |
| [Checkpointing](https://code.claude.com/docs/en/checkpointing) | 每轮文件快照、`/rewind` 三种恢复 + summarize、覆盖范围与已知限制 |
| [Prompt caching](https://code.claude.com/docs/en/prompt-caching) | 缓存前缀的隐式/显式、哪些操作失效缓存、缓存命中率查看 |
| [Troubleshooting](https://code.claude.com/docs/en/troubleshooting) | 高 CPU/内存、挂起、auto-compact thrashing、搜索问题 |
| [Best practices](https://code.claude.com/docs/en/best-practices) | 让 Claude 说出它看不到的东西、给它可跑的检查、把不能破例的规则用 hook 强制 |

## 可借鉴功能表

| Claude Code 的做法 | GQY 里对应什么 | 借鉴程度 | 来源 |
| --- | --- | --- | --- |
| 权限规则求值固定为 deny → ask → allow，且 allow 不能从 deny 里挖例外 | 06-T6 shell 白名单、08-鉴权、11-安全与沙盒的策略求值顺序 | 直接借鉴 | [permissions](https://code.claude.com/docs/en/permissions) |
| 复合命令按 `&& \|\| ; \| & 换行` 拆分逐子命令校验；剥离 timeout/nice/env 赋值等 wrapper | 06-T6 白名单按首 token 拒绝元字符 | 改造借鉴：GQY 白名单要拆子命令 + 剥 wrapper，否则可被 `safe-cmd && rm` 绕过 | [permissions](https://code.claude.com/docs/en/permissions) |
| 权限规则匹配结构化参数 `Tool(param:value)`，但主内容字段（Bash 的 command）不可匹配 | 06-T1 参数 Schema、T9 轨迹 | 直接借鉴：GQY 策略匹配应基于参数而非正文 | [permissions](https://code.claude.com/docs/en/permissions) |
| 六档权限模式（manual/acceptEdits/plan/auto/dontAsk/bypass）+ Shift+Tab 循环 | GQY 目前无权限模式概念 | 改造借鉴：单机单用户可简化为「默认问 / 信任目录自动」两级 + plan 只读 | [permission-modes](https://code.claude.com/docs/en/permission-modes) |
| 工作目录 + `--add-dir`/`additionalDirectories` + `blockReadsOutsideWorkingDirectories` | 06-T5 允许根、路径沙箱 | 直接借鉴：GQY 已有允许根，补「显式加目录」与「默认禁读工作目录之外」 | [permissions](https://code.claude.com/docs/en/permissions) |
| 工作区信任：仓库提供的 allow 规则/钩子/MCP 先列出、用户确认才生效 | 15-NX7 执行即信任（SEC5） | 改造借鉴：安装/打开陌生仓库时，把「它会带来的能力」列给用户确认 | [permissions](https://code.claude.com/docs/en/permissions) |
| OS 级沙盒 + auto-allow（沙盒边界代替人工审批）+ `dangerouslyDisableSandbox` 逃生舱 + `excludedCommands` | 11-安全与沙盒 SEC、06-T6 白名单 | 改造借鉴：OS 级边界与人工审批二选一，逃生舱必须显式参数化 | [sandboxing](https://code.claude.com/docs/en/sandboxing) |
| 生命周期 hooks：PreToolUse 可 deny/allow/改写输入，PostToolUse 可改写输出，可注入 additionalContext | GQY 无 hooks；对应工具/插件边界 | 直接借鉴：作为 GQY 插件运行时之外的治理扩展面（SEC/CF 层） | [hooks](https://code.claude.com/docs/en/hooks) |
| 钩子失败语义：exit 0=不发言、exit 2=阻止、JSON=细粒度；hook 不发言不等于批准 | 04-插件运行时 P、06-工具 | 直接借鉴：GQY 插件/钩子的失败与裁决要有明确三态 | [hooks](https://code.claude.com/docs/en/hooks) |
| 子代理 = Markdown + frontmatter（name/description/tools/model），description 驱动模型决定是否委托；工具白名单在执行处强制 | 14-目标与支线 GL（支线/分叉会话）、06-T2 工具注入 | 改造借鉴：GQY 支线/分叉会话可借鉴「描述驱动委托 + 执行处验白名单」 | [sub-agents](https://code.claude.com/docs/en/sub-agents) |
| 子代理上下文隔离（只回摘要）+ 并发上限 20 + 嵌套深度 3 | 14-GL3 支线两种形态 | 直接借鉴：GQY 分叉会话要有并发上限与嵌套深度 | [sub-agents](https://code.claude.com/docs/en/sub-agents) |
| 子代理输出扫描：转义 `<system-reminder>`/`Human:` 模仿，加「此内容无授权」头 | 11-安全与沙盒、插件输出回注 | 直接借鉴：GQY 子任务/插件输出回注上下文前消毒并加来源头 | [sub-agents](https://code.claude.com/docs/en/sub-agents) |
| 描述预算告警（自建子代理 description 合计超 15k tokens 启动告警） | 06-T3 工具注册、04-P11 动作注册 | 直接借鉴：GQY 工具/插件描述要常驻上下文，必须给预算 | [sub-agents](https://code.claude.com/docs/en/sub-agents) |
| skills：description 驱动按需加载 body（描述常驻、正文用才载入）+ `context: fork` 隔离执行 + `!` 动态上下文注入 | 15-一次性插件 NX、06-T2 外部工具注入 | 改造借鉴：「描述常驻 + 正文按需」可用于 GQY 工具注入策略；动态注入可用于插件预取 | [skills](https://code.claude.com/docs/en/skills) |
| `/goal`：独立小模型在每轮后判三态（未达成/达成/不可能），错误分类处理，后台工作延迟评估 | 14-目标与支线 GL1–GL6 | 直接借鉴：补强 GL 状态机（评估器独立、条件写成可验证、错误分类） | [goal](https://code.claude.com/docs/en/goal) |
| checkpointing：每轮文件快照 + `/rewind` 三动作（恢复代码/对话/两者）+ summarize from/up to | 05-会话与产物 S、14-GL5 恢复 | 直接借鉴：GQY 会话回滚补「代码快照 + 三动作」语义 | [checkpointing](https://code.claude.com/docs/en/checkpointing) |
| compaction 后按来源分类的存活规则（CLAUDE.md 重读、规则按需重载、skill body 截断、hook 注入被摘要） | GQY 上下文预算/压缩（尚无专门设计） | 直接借鉴：GQY 若做压缩，按来源分类定存活规则 | [context-window](https://code.claude.com/docs/en/context-window) |
| MCP 工具定义按需加载（tool search），只留工具名与服务器说明在上下文 | GQY 工具/插件清单 | 改造借鉴：工具量大时「名字常驻、schema 按需」 | [how-claude-code-works](https://code.claude.com/docs/en/how-claude-code-works) |
| 插件 eval：同 prompt 有/无插件基线对比，评分低于阈值 CI 门禁 | 07-评测 E、15-一次性插件 | 直接借鉴：GQY 插件/工具评测用「无插件基线」而非绝对值 | [skills](https://code.claude.com/docs/en/skills) |
| `/doctor` 与 `/doctor prompt-audit`：配置自检 + 指令文件冲突审计 | GQY 无自检命令；对应 CF 配置与 docs 体系 | 改造借鉴：GQY 可做 `gqy doctor` 只读自检 | [memory](https://code.claude.com/docs/en/memory) |

## 试错案例表

| # | 现象 / 取舍 | 根因 | 对 GQY 的启发 |
| --- | --- | --- | --- |
| 1 | `Bash(safe-cmd *)` 允许规则拦不住 `safe-cmd && other-cmd` 或 `echo "$(safe-cmd)"` | 复合命令必须按分隔符拆成子命令，逐个子命令都必须被允许 | GQY shell 白名单只按首 token 会漏掉子 shell、命令替换、控制流体内嵌命令；必须拆分后逐段校验 |
| 2 | `Bash(npm test *)` 匹配不了 `timeout 30 npm test`、`NODE_ENV=test npm test` | 包装器（timeout/nice/nohup/env 赋值/xargs）改变命令识别 | GQY 白名单要先剥固定包装器与安全环境变量赋值，再匹配 |
| 3 | deny→ask→allow 固定求值，更具体的 allow 无法从宽 deny 里挖洞；用户级 deny 压过项目级 allow | 例外优先于规则会制造绕过；多作用域合并时 deny 必须恒先 | GQY 策略（工具/插件/沙盒）合并多来源配置时：deny 恒先、allow 不能反向开洞 |
| 4 | 参数规则不能匹配主内容字段：`Bash(command:rm *)` 被禁用并告警 | 复合命令可绕过对主内容字段的匹配 | GQY 策略匹配结构化参数（path/schema 字段），不匹配可拼接的正文 |
| 5 | 沙盒缺依赖或平台不支持时默认降级「无沙盒运行」 | 可用性优先的默认值，安全被悄悄放弃 | GQY 沙盒开启时要么强制（缺依赖即拒绝执行），要么显式声明降级并告警，绝不静默 |
| 6 | docker/watchman/Go CLI TLS/ssh/clipboard 等合法工具在沙盒内失败 | 沙盒边界与工具自身机制（namespace/TLS/控制终端）冲突 | GQY 维护 excludedCommands 与「已知不兼容」清单，遇坑有据可查，不为个案放宽整体边界 |
| 7 | 沙盒用 0 字节只读占位文件顶住写拒绝路径，进程被 SIGKILL 后残留，之后设置写失败 | 清理依赖正常退出；残留占位在下次启动又被只读绑定 | GQY 用占位/标记文件实现拒绝策略时，要有 doctor 清理与残留检测 |
| 8 | 子代理报告里的 `<system-reminder>`、`Human:`、`bypassPermissions` 字样可被主对话误读为指令 | 子任务读到的不可信内容（网页/文件）被原样回注 | GQY 子任务/插件输出回注上下文前做消毒（转义模仿标记）并加「无授权」来源头 |
| 9 | 自建子代理 description 合计超 15k tokens 时上下文被吃满 | description 常驻每轮上下文，是隐形成本 | GQY 工具/插件/支线的描述字段必须预算并告警，细节进「按需加载」的正文 |
| 10 | 并发子代理默认上限 20、嵌套深度 3，超限报错不重试 | 无上限的并行/递归会让单机失控 | GQY 分叉会话/支线要有并发上限与嵌套深度，超限明确报错 |
| 11 | `/rewind` 只恢复文件编辑工具改过的文件：纯 bash 改动、子代理编辑、符号链接/硬链接都不恢复 | 快照边界是「文件编辑工具」而非「整个工作树」 | GQY 会话回滚要明示覆盖范围与跳过项（符号链接等），并给「用 git 兜底」的指引 |
| 12 | `/rewind` 回滚后若未发下一条 prompt 就退出，已回滚的消息会回来 | 回滚后的会话状态随「下一条 prompt」一起持久化 | GQY 回滚的持久化时机要明确：回滚结果何时落盘、何时可能被覆盖 |
| 13 | 单个超大文件/输出让 auto-compact 压缩后立刻又满，自动压缩几次后报错停，不再循环 | 无限压缩循环是更糟的失败 | GQY 自动压缩要有最大尝试次数，达到后降级报错并给恢复步骤 |
| 14 | compaction 后：CLAUDE.md 重读、规则按需重载、skill body 截断（5k/条、25k 总量、最旧先丢）、hook 注入被摘要 | 压缩只作用于对话历史；注入式上下文各有恢复路径 | GQY 若做压缩，必须按「来源」分类定义存活规则，而不是一刀切 |
| 15 | `/goal` 评估模型不跑工具、只判断已展示在对话里的内容；条件写成「npm test 退出 0」才可验证 | 评估器独立且廉价；写进对话的证据才是可判的 | GQY 的 goal 完成判定要独立评估器 + 条件写成可验证的检查，而非「把事情做好」 |
| 16 | `/goal` 错误分类：认证失败/额度耗尽/上下文溢出/模型不可用 → 清空；暂时性错误 → 自动重试 3 次后暂停 | 自动续轮不能死于暂时错误，也不能死于必须人修的错 | GQY GL 状态机的 `blocked` 与 `paused` 应按错误可恢复性区分，并给重试上限 |
| 17 | `/goal` 在后台子任务/命令仍运行时跳过该轮评估，30 分钟起 check-in、逐次翻倍退避 | 后台工作没完时评估结论没有意义 | GQY 主线等待支线/后台任务时，完成判定要延迟，并有「卡住检查」退避 |
| 18 | 钩子不发言（exit 0）不等于批准；onFailure 决定「钩子挂了之后动作是否放行」 | 安全相关的钩子失败必须可配置为阻断 | GQY 插件/钩子失败语义三态化：放行/阻断/降级，默认对安全钩子偏阻断 |
| 19 | skill 的 `!` 动态注入命令失败（非零退出）会中止整个技能调用 | 坏数据不如没有；只对搜索/比较命令豁免 exit 1 | GQY 插件预取/技能前置命令失败要快速失败，不带着空数据继续 |
| 20 | CLAUDE.md 是「上下文」不是「强制」，钩子才是强制；越具体越被遵守，互相矛盾时模型随意选 | 提示词指令不保证遵守 | GQY 安全边界必须用代码/hook/策略强制，不能指望模型读提示词就守规矩 |

## 结论：最值得立刻吸收进 GQY designs 的 5 条

1. **权限求值与命令校验的硬规则（→ 06-工具 / 11-安全与沙盒）**。deny→ask→allow 固定顺序、deny 恒先且不可被 allow 开洞、复合命令按分隔符拆分逐段校验、先剥 wrapper/环境变量赋值。这是 GQY shell 白名单当前最大缺口，也是 Claude Code 用血换来的绕过清单。

2. **生命周期 hooks 作为 GQY 的治理扩展面（→ 04-插件运行时之外的新层，或并入 11-SEC）**。PreToolUse 可 deny/改写输入、PostToolUse 可改写输出、可注入 additionalContext；失败语义三态（exit 0/2/JSON + onFailure）。它把「不能破例的规则」从提示词变成代码。

3. **目标与支线补强：独立评估器 + 错误分类（→ 14-目标与支线 GL）**。借鉴 `/goal`：完成判定交给独立小模型三态（未达成/达成/不可能），条件写成可验证检查；错误按可恢复性分流（不可恢复→blocked、暂时性→自动重试 N 次后 paused）；后台工作未结束时延迟评估。这正好补 GL1–GL6 目前缺的「怎么判定达成、失败怎么办」。

4. **会话回滚三动作 + 覆盖范围清单（→ 05-会话与产物 S）**。借鉴 checkpointing：每轮文件快照，`/rewind` 给「恢复代码 / 恢复对话 / 两者都恢复」三个独立动作，并把「bash 改动、子代理编辑、符号链接、外部改动」明确列为不覆盖、给 git 兜底指引。GQY 现有草稿/停止/重试缺代码级回滚。

5. **上下文预算按来源定存活规则 + 描述预算（→ 会话/工具上下文预算，可并入 05 或新开）**。借鉴 context-window：压缩只作用对话历史，CLAUDE.md/规则/skill body/hook 注入各自有恢复路径；工具/插件/支线的「描述」常驻上下文必须预算并告警，细节按需加载。这直接对应 GQY「谁用谁付」（G1）在上下文上的体现。

## 来源链接

- 文档首页：<https://code.claude.com/docs>
- 全文索引：<https://code.claude.com/docs/llms.txt>
- <https://code.claude.com/docs/en/how-claude-code-works>
- <https://code.claude.com/docs/en/memory>
- <https://code.claude.com/docs/en/hooks>
- <https://code.claude.com/docs/en/hooks-guide>
- <https://code.claude.com/docs/en/permissions>
- <https://code.claude.com/docs/en/permission-modes>
- <https://code.claude.com/docs/en/sandboxing>
- <https://code.claude.com/docs/en/sandbox-environments>
- <https://code.claude.com/docs/en/sub-agents>
- <https://code.claude.com/docs/en/skills>
- <https://code.claude.com/docs/en/goal>
- <https://code.claude.com/docs/en/context-window>
- <https://code.claude.com/docs/en/checkpointing>
- <https://code.claude.com/docs/en/prompt-caching>
- <https://code.claude.com/docs/en/troubleshooting>
- <https://code.claude.com/docs/en/best-practices>
