# NapCat（QQ 桥）接入指南

顾清影的 QQ 功能需要 **NapCat**（无头 QQ 客户端）作为桥：它连入 GQY 的反向 WebSocket 服务（`gqy qq`，默认 8300 端口），把 QQ 消息转给顾清影，并把她的回复发回 QQ。

## 安装状态

- **GQY 侧**：已就绪。`gqy qq` 监听 ws://0.0.0.0:8300，配置 `config qq.*`。
- **NapCat 本体**：已下载到 `~/Library/Application Support/gqy/napcat/`（NapCat.Shell v4.18.18，跨平台，含 darwin.arm64 native 模块）。
- **macOS 原生运行**：本机实测 QQ 协议核心（major/wrapper.node）在 macOS 上 SIGSEGV —— **官方文档也明确 macOS 仅支持 Docker 方式**。Windows/Linux 可原生运行。

## macOS 推荐方式：Docker 运行 NapCat

1. 安装 Docker Desktop（macOS）
2. 在终端执行：

```bash
docker run -d --name napcat \
  -e NAPCAT_UID=1000 -e NAPCAT_GID=1000 \
  -p 3001:3001 -p 6099:6099 \
  -e ACCOUNT=<机器人QQ号> \
  -e WS_ENABLE=true -e WS_SERVER=ws://host.docker.internal:8300 \
  -e WS_TOKEN=你的access_token \
  -v ~/Library/Application\ Support/gqy/napcat-docker:/app/napcat \
  --restart=always \
  mlikiowa/napcat-docker
```

3. 首次启动日志会出现登录二维码，用**机器人 QQ 的账号**扫码登录一次即可。
4. 登录后 NapCat 自动连上本机 8300，顾清影即可收发 QQ。

## Windows / Linux

- Windows：下载 `NapCat.Shell.Windows.Node.zip`（自含 QQ 与 Node，免安装）。
- Linux：`NapCat.Shell.zip` + 系统已有 QQNT 资源，或 NapCat-Docker。

## GQY 侧配置

```bash
gqy config set qq.enabled true
gqy config set qq.access_token <与 NapCat WS_TOKEN 一致>
gqy config set qq.owner_qq 1950930166   # 主人 QQ（默认已是）
gqy config set qq.forward_events true   # 重要事件转告主人
gqy qq                                 # 启动监听（App 设置里开「QQ 机器人」亦可）
```

## 说明

- GQY 的 QQ 是无头桥接，不影响 macOS 桌面 QQ。
- 管理指令（/status /affection）仅主人与 admin_users 可用。
- 用量/转告/群名片等能力见 CHANGELOG。
