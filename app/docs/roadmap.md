# 路线图

按优先级排序；每项写清“为什么”和“完成的定义”。

## 已完成（v1.2）

- **真实后端适配层**：Go 标准库 SSE 转发 + `js/api.js`（探测 / 流式 / 会话镜像）。
  无密钥或探测失败时前端显示「未连接服务端」；失败有错误卡片与重试。
- **模型目录**：`GET /api/models` 从 models.dev 拉取聊天模型（缓存 6h），前端可搜索选择。
- **回归脚本**：`scripts/smoke.mjs`，`npm run smoke` 一条命令编译后端并跑端到端。
- **会话落盘**：按 `docs/AboutProjectFile.md` 写 manifest / context / messages.jsonl / artifacts / tool logs。
- **安全修复**：Markdown URL 协议白名单、静态托管扩展名白名单、工具日志参数脱敏、会话目录 0700 / 文件 0600。
- **数据可靠性**：localStorage 配额失败不再静默；导入 JSON（覆盖前二次确认）；会话草稿持久化。
- **现代交互**：⌘/Ctrl+K 搜索、Alt+N 新会话、⌘/Ctrl+Enter 发送、Esc 关闭搜索、回到最新按钮、流式完成/失败 aria-live 播报、跟随系统减少动效。

## 已完成（当前工作区，未发版）

- **`app.js` 拆分**：状态/DOM 进 `js/store.js`，渲染、对话、文件、菜单、设置各成 service；`app.js` 只保留事件接线与 boot。
- **agent loop**：`internal/agent` 支持模型 ↔ 工具多轮、usage 累加、20 轮上限；`POST /api/chat` 增加 `tool` / `question` 事件。
- **工具系统**：`internal/tool` 统一契约 + 注册表；核心工具 `read` / `edit` / `shell` / `list_dir` / `websearch`（占位） / `ask_user`；外部工具 `provider_usage`，由 `GQY_ENABLE_EXTERNAL` 控制注入。
- **`ask_user`**：服务端返回 `question`，前端复用提问卡片；回答后继续发起请求。
- **GitHub 只读账户**：OAuth 授权、token 服务端落盘、扩展浮层连接/断开。
- **设置扩展**：10 个 tab + 搜索过滤；用量浮层；`skills/gh.md`。
- **冒烟扩展**：设置搜索、用量/扩展浮层、真实上游 agent loop（工具调用 + 最终答案 + usage）。
- **工具权限与超时**：文件工具走路径沙箱（workspace + `$GQY_HOME/sessions`，符号链接/`..` 越界拒绝，token 文件显式拒绝）；`shell` 有超时、进程组回收、输出上限与可选白名单；`agent.Loop.ToolTimeout` 控制单工具超时。
- **会话管理**：重命名、置顶、批量删除、导出 JSON / Markdown；`pinned` 持久化并对旧数据迁移。
- **作品预览**：HTML / SVG / Markdown 在灯箱内切换预览与源码；sandbox iframe + CSP，超过 2 MB 或二进制自动降级。
- **插件运行时**：`internal/plugin` 支持原生 stdio JSON-RPC 与 MCP stdio 两种适配器，加载 / 调用 / 卸载 / 超时 / 进程组回收；`/api/plugins` 与 `/api/tools` 暴露状态，设置页可查看软件包。
- **评测网关**：`cmd/gqy-eval` + `internal/eval`；JSON 配置、单因素/两两/全组合矩阵、用例版本哈希、多轮工具循环、重试/超时/故障注入、规则判定、trace 回放、原子落盘与脱敏、报告。
- **初始化引导页**：首次进入选择工作区、模型、动效偏好，写入同一份 `state`。
- **关键词浮窗**：消息正文里的术语用本地词表解释，不联网。
- **看板与人设/记忆/知识库**：设置页的人设、记忆、知识库、软件包、工具、用量、账户从占位转为可用；新增看板、引导、术语浮窗；语音开关已接线（唤醒词未做）。
- **本地网关**：多 provider、路由组、失败转移、冷却、并发、亲和、请求日志、`/v1/models` 与 `/v1/chat/completions`。
- **评测宿主接真实插件**：`cmd/gqy-eval` 按 `tool` 前缀加载 `$GQY_HOME/plugins` 下的插件，不再只依赖占位工具。
- **附件上传通道**：`POST /api/files` 写入 `sessions/<id>/tmp/uploads/`；文本附件注入 user 消息，图片以 data URL 走多模态 content。
- **多文件产物**：`write_artifact` 工具写工作区并生成 `artifact.files`，文件面板按真实路径建树。
- **网关扩展**：provider 账号与日 token 配额、Anthropic Messages / Gemini 翻译（含兼容流式）、Unix/TCP 远程插件、插件 `provides/requires` 服务依赖、鉴权与限流。
- **评测模型裁判**：`judge_model` 可选，规则判定仍是主判定。
- **暗色主题 / i18n / 分页渲染**：暗色令牌与 `data-theme`；中文 / English 导航与设置标签；长会话只渲染最近 80 条。
- **可选访问令牌**：`GQY_AUTH_TOKEN` 保护 `/api/*`、`/v1/*` 与 `/v1beta/*`；前端 401 时换 cookie。

## P0 · 结构性

1. **`ask_user` 有状态续跑**
   为什么：回答作为新请求发出，工具调用上下文丢失，模型可能重复提问或重复执行。
   做法：服务端保留 loop 状态（或把工具调用与问题一起回传），回答直接续跑同一轮。
   完成：一次提问后的回答不会触发重复工具调用。

## P1 · 功能

2. **服务端会话回灌**：启动时 `GET /api/sessions` 合并较新的会话，支持多设备。
3. **插件权限与崩溃恢复**：交互式审批、崩溃自动重启、跨社区契约测试矩阵。

## P2 · 体验与质量

4. **无障碍**：文件面板与菜单的焦点陷阱补齐（设置/灯箱/图片/用量/扩展已用 `trapTab`）。
5. **工具日志清理**：按 `manifest.cleanup_policy` 落 `gqy clean --expired`，现在只写不删。
6. **完整 i18n**：当前覆盖导航、看板、引导与设置标签；正文/提示文案仍是中文硬编码。

## 非目标（先不做）

- 账号体系、云同步。
- 真实模型密钥进前端。
- 复杂富文本编辑器。
