# QQ 平台接入指南（onebot / NapCat）

顾清影的 QQ 能力由 **无头桥接** 实现：GQY 运行一个反向 WebSocket 服务端（onebot v11），
NapCat（独立无头 QQ 客户端，自己的 QQ 号）连入后，顾清影即可收发 QQ 消息。
**不影响 macOS 桌面 QQ**。

## 一、启用

### 1. 配置（三选一）

- **WebUI**：设置 → 全局 → 「QQ 机器人」组（推荐，GUI 全可配）
- **App**：设置 → QQ 机器人开关（启动/停止 `gqy qq`，与 App 同启停）
- **CLI**：

```bash
gqy config set qq.enabled true
gqy config set qq.access_token <与 NapCat WS_TOKEN 一致>
gqy config set qq.owner_qq 1950930166   # 主人 QQ（默认已是）
gqy qq                                  # 启动监听（默认 8300）
```

### 2. 跑 NapCat（macOS 仅 Docker 方式）

macOS 上 NapCat 的 QQ 协议核心原生运行会 SIGSEGV，**官方只支持 Docker**。
NapCat.Shell v4.18.18 已装入 `~/Library/Application Support/gqy/napcat/`（含 darwin.arm64 native，已本地重签过 Gatekeeper），
Docker 运行命令见 [napcat-setup.md](../../gqy/src/scripts/napcat-setup.md)。

## 二、能力清单

| 类别 | 能力 |
|---|---|
| 消息 | 文本 / 图片（入站 get_image+出站 base64）/ 文件 / 视频 / 语音转写（macOS 原生 STT）/ 表情 emoji / CQ 字符串 / @全体 / 引用消息（文本+图片） |
| 社交 | 群名+群公告注入、群成员名片/昵称（TTL 缓存）、群管指令、好友/加群申请、进群欢迎、消息撤回通知、自身禁言感知 |
| 会话 | 每用户/每群独立记忆（会话隔离）、会话并发闸、中间消息流式、按会话模型路由、限流 |
| 管理 | `/status` `/affection` `/conversations` `/connections` `/help` `/mute <QQ> <分钟>` `/unmute` `/kick` `/quit` `/stop`（仅主人/管理员） |
| 转告 | `notify_owner` 工具（别人让带话→私信主人）、重要系统事件转发 |
| 其他 | 用量 `src=qq` 落账、WebUI 通道列表查看 QQ 会话、`qq_conversations` 工具 |

## 三、管理入口

| 层面 | 入口 |
|---|---|
| 配置 | WebUI 设置 → 全局 → QQ 机器人组（10 个字段全 GUI 可配） |
| 进程 | App 设置 → QQ 机器人开关（同启停） |
| 运行 | QQ 管理指令（见上表） |
| 查看 | WebUI 侧边栏 QQ 通道 / `qq_conversations` 工具 / QQ `/conversations` |

## 四、常见问题

- **问：会影响我桌面上的 QQ 吗？** 不会。NapCat 是无头桥接，与桌面 QQ 完全独立。
- **问：为什么收不到消息？** 检查 NapCat 是否连上（`/connections` 看账号）、token 是否一致、
  群聊需 @ 或呼名「清影」或命中唤醒关键词才回应。
- **问：如何让特定会话用不同模型？** 配置 `qq.conversations` 路由表（conversation_id 前缀 → 供应商+模型）。
