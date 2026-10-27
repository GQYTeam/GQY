# 路线图

按优先级排序；每项写清“为什么”和“完成的定义”。

## 已完成（v1.2）

- **真实后端适配层**：`server.go`（Go 标准库 SSE 转发）+ `js/api.js`（探测 / 流式 / 会话镜像）。
  无密钥或探测失败时前端显示「未连接服务端」；失败有错误卡片与重试。
- **模型目录**：`GET /api/models` 从 models.dev 拉取聊天模型（缓存 6h），前端可搜索选择。
- **回归脚本**：`scripts/smoke.mjs`，`npm run smoke` 一条命令编译后端并跑端到端。
- **会话落盘**：按 `docs/AboutProjectFile.md` 写 manifest / context / messages.jsonl / artifacts / tool logs。
- **安全修复**：Markdown URL 协议白名单、静态托管扩展名白名单、工具日志参数脱敏、会话目录 0700 / 文件 0600。
- **数据可靠性**：localStorage 配额失败不再静默；新增导入 JSON（覆盖前二次确认）；会话草稿持久化。
- **现代交互**：⌘/Ctrl+K 搜索、Alt+N 新会话、⌘/Ctrl+Enter 发送、Esc 关闭搜索、回到最新按钮、流式完成/失败 aria-live 播报、跟随系统减少动效。

## P0 · 结构性

1. **app.js 拆分（store + feature 模块）**
   为什么：控制器偏大，改一处要读全文件。
   做法：把 `state`/`el`/`save`/`setView` 收进 `js/store.js`；`chat/sidebar/settings/files` 各自 `init(store)`。
   完成：`app.js` < 300 行，模块可独立加载，行为不变。

## P1 · 功能

2. **初始化引导页**（`docs/AboutUiPages.md`）：首次进入时选择工作区、模型、动效偏好；写入同一份 `state`，之后不再出现。
3. **关键词浮窗**：正文里的专业名词（如 RAG / collate_fn）悬停或点击，用上下文里的定义解释；数据来自消息内的术语表，不联网。
4. **多文件产物与目录**：`artifact` 支持 `files: [{path, content}]`，文件面板展示真实目录；代码灯箱支持多标签。
5. **作品预览**：HTML/SVG/Markdown 产物可切换「预览 / 源码」，预览用 sandbox iframe。
7. **会话管理**：重命名、置顶、批量删除、导出单条对话（导入 JSON 已完成）。
8. **附件上传通道**：`POST /api/files` + 服务端写入 `sessions/<id>/tmp/uploads/`；完成后带附件的消息也能走真实模型。
9. **服务端会话回灌**：启动时 `GET /api/sessions` 合并较新的会话，支持多设备。

## P2 · 体验与质量

10. **暗色主题**：令牌集中在 `styles/base.css`，但 172 处硬编码颜色需要先收敛成令牌，再补 `[data-theme=dark]`。
11. **i18n**：文案抽到 `locales/`，中英切换；当前为中文硬编码。
12. **虚拟滚动**：超长会话时只渲染可视区域。
13. **无障碍**：文件面板与菜单的焦点陷阱补齐（设置/灯箱/图片已用 `trapTab`）。
14. **服务端鉴权**：公网部署前加 token 或反向代理鉴权；当前只允许本机。

## 非目标（先不做）

- 账号体系、云同步。
- 真实模型密钥进前端。
- 复杂富文本编辑器。
