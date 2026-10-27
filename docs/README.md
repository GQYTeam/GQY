# 项目文档索引

仓库根的 `docs/` 放产品与架构约定；`app/docs/` 放当前实现说明。改代码前先看对应文档，文档与代码不一致时以代码为准并顺手更正。

## 产品与设计约定

| 文档 | 内容 | 落地状态 |
| --- | --- | --- |
| `AboutProjectFile.md` | Artifact / Runtime Files / Workspace / Session 边界，`$GQY_HOME/sessions/$SESSION_ID/{tmp,logs,io}` 目录规范 | 会话落盘已按此实现；上传、日志轮转、清理命令未做 |
| `AboutUiPages.md` | 页面清单：灯箱、设置、看板、用量、初始化引导、关键词浮窗 | 全部已做；看板为本机任务卡 |
| `AboutAiOtput.md` | 输出与代码风格红线 | 持续遵守 |
| `AboutPlugin.md` | 插件运行时 + 多社区适配器：分层、并发、远程、进程回收、契约测试 | 已落地统一模型、原生/MCP 适配器、进程回收与单测；远程与服务依赖未做 |
| `AboutAitestCallToolOrPlugin.md` | 工具/插件调用评测网关：三维度实验矩阵、判定、落盘 | 已落地 `internal/eval` + `cmd/gqy-eval`，见 `app/docs/eval.md` |

## 调研与设计

| 文档 | 内容 |
| --- | --- |
| `research/MagpieGateway.md` | Magpie 网关调研：候选、路由、冷却、亲和、插件 |
| `design/Gateway.md` | 本地多 provider 网关设计 |
| `design/PluginRuntime.md` | 原生 stdio / MCP 插件运行时设计 |
| `design/EvalGateway.md` | 三维评测网关设计 |
| `design/FrontendCapabilities.md` | 引导、看板、术语、设置页设计 |
| `CapabilityMatrix.md` | 能力分类与状态 |

## 当前实现文档

| 文档 | 内容 |
| --- | --- |
| `../app/README.md` | 运行、部署、环境变量、接口、安全边界 |
| `../app/AGENTS.md` | 协作者约束、目录、改动指南、已知边界 |
| `../app/docs/architecture.md` | 前后端模块、状态、流式管线、网关、插件、agent loop |
| `../app/docs/tools.md` | 工具契约、沙箱、核心/外部/插件工具、ask_user |
| `../app/docs/pages.md` | 页面、浮层、设置 tab、消息结构 |
| `../app/docs/data-model.md` | state、Message、localStorage、会话镜像 |
| `../app/docs/eval.md` | 评测网关配置、命令、落盘与回放 |
| `../app/docs/testing.md` | 手动验收与自动冒烟覆盖 |
| `../app/docs/roadmap.md` | 已完成、P0/P1/P2 计划 |
