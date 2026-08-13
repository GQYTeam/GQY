# 事件内核 + 双插件面 + 双人格驱动架构

> 本文档描述 GQY 重构后的骨架设计：把「以对话为中心」翻成「事件内核 + 双插件面 + 双人格驱动」。
> 目标：贾维斯（干活）与陪伴（情感）两个身份在同一副骨架上各自成立，
> 且新增任何主动提醒 / 工具 / 人格都不再改对话循环。

## 一、为什么重构

旧骨架一切能力都挂在「对话循环」上：watch / alarm 这类主动能力只能以旁路命令存在，
加一个新能力要找到地方塞进循环；换模型要改多处；人格噪音（表情包、玩梗）在干活时干扰效率。

重构不改语言（仍是 Rust 单体）、不动灵魂文件（gqy.md / chat.md 等一行未改），
只换骨架方向：**从「等人来问」翻成「看着这个系统」**。

## 二、骨架：三层

```
内核（编译进二进制，不做插件）
├── 事件流（UserMessage / SystemEvent / AlarmFired ...）
├── agent 状态机（chat_stream_with_tools 循环）
├── LlmClient（OpenAi | Pi 枚举式 provider 抽象，天然插件点）
└── 权限策略（ToolPermission + path_guard + 高危命令确认，代码级）

插件面 A：能力（一个注册表，两个来源）
├── 原生 Tool trait（ToolRegistry + lazy loading）
└── MCP 服务器（JSON-RPC over stdio，mcp.rs）

插件面 B：事件源（EventSource trait，src/events.rs）
├── WatchSource（进程/内存管家，原 gqy watch）
├── DiskSpaceSource（磁盘水位，默认 90%）
└── （新增：实现 trait + 注册一行）

人格驱动（纯数据，GQY_HOME/config/prompts/*.md）
├── Normal 工作模式：base gqy.md + work-mode.md 纪律块
├── Plan 模式：base + plan.md
└── Chat 陪伴模式：base + chat.md（可运行时覆盖），零工具、拒系统事件
```

## 三、工作/情感硬隔离

模式不再是「字符串提醒」，而是三层隔离：

1. **提示词层**：Normal 挂 work-mode.md 工作纪律块（简洁直接、禁 emoji/表情包、
   把握程度只在低把握时说明）；Chat 挂陪伴人格。人格文件本体不动，纪律是增量。
2. **工具层**：Chat 模式零工具（chat_pure_text 默认开）；Plan 只读；
   已有 Plan/Chat 拒绝非只读工具的硬拦截保留。
3. **事件层（本次新增）**：queued_prompts.source 标记消息来源
   （user / system / app）。watch / disk 等系统主动事件以 source=system 入队，
   **Chat 模式消费时按来源丢弃 system**（agent::consume_queued_prompts →
   filter_queued_for_mode），代码级保证：磁盘告警永不侵入深夜闲聊。

## 四、事件源插件面

```
pub trait EventSource: Send + Sync {
    fn name(&self) -> &'static str;                    // 唯一，用于冷却 stamp
    fn sample(&self, paths: &GqyPaths) -> Result<Option<SystemEvent>>;
}
```

约定：sample 只做本地采样与规则判断，绝不直接打扰用户；是否值得打扰由本地门槛决定，
LLM 只在事件入队后介入判断。冷却按事件源名分文件（last_watch_alert / last_disk_alert），
事件经 WebUI /api/queue（source=system）投递给运行中的会话。

gqy watch --every 30s（或 LaunchAgent 托管）跑 events::poll_all 循环。

## 五、安全（代码级，不依赖提示词）

| 防线 | 位置 | 说明 |
|---|---|---|
| path_guard | tools/path_guard.rs | 写文件工具落盘前拦截项目源码目录写入 |
| Plan/Chat 只读 | agent 循环 | 非只读工具在 Plan/Chat 模式直接拒绝 |
| 高危命令确认 | agent 循环 + run_command | rm -rf /、sudo rm、diskutil erase、mkfs、launchctl unload/bootout 命中即弹确认，拒绝即拦截 |
| 工具输出消毒 | agent 循环 | 外部内容（网页/文件/命令输出）回注前剥离 system-reminder 伪造标签 |
| 消息来源校验 | web.rs normalize_prompt_source | 只接受 user/system/app，其他拒绝 |

## 六、待办（下一步，不在本次范围）

- alarm / 定时简报接入 EventSource（当前仍是独立 worker）
- 状态层事件溯源化（conversation / memory / evicted_context 三库合一投影）
- 工具权限升级为确认分级（Confirm tier），并做成配置驱动
