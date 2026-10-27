# 项目文档索引

仓库根的 `docs/` 放产品与架构约定；`app/docs/` 放当前实现说明。改代码前先看对应文档，文档与代码不一致时以代码为准并顺手更正。

## 产品与设计约定

| 文档 | 内容 | 落地状态 |
| --- | --- | --- |
| `AboutProjectFile.md` | Artifact / Runtime Files / Workspace / Session 边界，`$GQY_HOME/sessions/$SESSION_ID/{tmp,logs,io}` 目录规范 | 会话落盘已按此实现；上传、日志轮转、清理命令未做 |
| `AboutUiPages.md` | 页面清单：灯箱、设置、看板、用量、初始化引导、关键词浮窗 | 灯箱 / 设置 / 用量已做；看板、引导、关键词浮窗未做 |
| `AboutAiOtput.md` | 输出与代码风格红线 | 持续遵守 |
| `AboutPlugin.md` | 插件运行时 + 多社区适配器：分层、并发、远程、进程回收、契约测试 | 只落地统一 `Tool` 契约与进程内注册表 |
| `AboutAitestCallToolOrPlugin.md` | 工具/插件调用评测网关：三维度实验矩阵、判定、落盘 | 未实现 |

## 当前实现文档

| 文档 | 内容 |
| --- | --- |
| `../app/README.md` | 运行、部署、环境变量、接口、安全边界 |
| `../app/AGENTS.md` | 协作者约束、目录、改动指南、已知边界 |
| `../app/docs/architecture.md` | 前后端模块、状态、流式管线、agent loop |
| `../app/docs/tools.md` | 工具契约、核心/外部工具、ask_user、与插件设计的关系 |
| `../app/docs/pages.md` | 页面、浮层、设置 tab、消息结构 |
| `../app/docs/data-model.md` | state、Message、localStorage、会话镜像 |
| `../app/docs/testing.md` | 手动验收与自动冒烟覆盖 |
| `../app/docs/roadmap.md` | 已完成、P0/P1/P2 计划 |
