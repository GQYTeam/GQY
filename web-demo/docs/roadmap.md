# 路线图

按优先级排序；每项写清“为什么”和“完成的定义”。

## P0 · 结构性

1. **app.js 拆分（store + feature 模块）**
   为什么：控制器 ~100KB，改一处要读全文件。
   做法：把 `state`/`el`/`save`/`setView` 收进 `js/store.js`；`chat/sidebar/settings/files/board` 各自 `init(store)`。
   完成：`app.js` < 300 行，模块可独立加载，行为不变。

2. **回归脚本**
   为什么：现在靠手写临时探针。
   做法：`scripts/smoke.mjs`（Node + 本机 Chrome headless，无依赖）检查首屏/发送/面板/设置/看板。
   完成：`npm run smoke` 一条命令出结果。

3. **真实后端适配层**
   为什么：演示回复是写死的文案。
   做法：`generateReply()` 换成可注入的 `chat({ messages, signal })`，接口返回流式 chunk（thinking / tool / text）。
   完成：新接口不改渲染层；断网有明确错误态。

## P1 · 功能

4. **初始化引导页**（`docs/AboutUiPages.md`）：首次进入时选择工作区、模型、动效偏好；写入同一份 `state`，之后不再出现。
5. **关键词浮窗**：正文里的专业名词（如 RAG / collate_fn）悬停或点击，用上下文里的定义解释；数据来自消息内的术语表，不联网。
6. **多文件产物与目录**：`artifact` 支持 `files: [{path, content}]`，文件面板展示真实目录；代码灯箱支持多标签。
7. **作品预览**：HTML/SVG/Markdown 产物可切换「预览 / 源码」，预览用 sandbox iframe。
8. **看板真实数据**：天气/日程/任务读取本机配置或接口；任务与对话里的 todo 双向同步。
9. **会话管理**：重命名、置顶、批量删除、导出单条对话、导入 JSON。
10. **快捷键**：Cmd/Ctrl+K 搜索、Cmd/Ctrl+N 新会话、Cmd/Ctrl+Enter 发送。

## P2 · 体验与质量

9. **暗色主题**：令牌已集中在 `styles/base.css`，补一组 `[data-theme=dark]` 变量即可。
10. **i18n**：文案抽到 `locales/`，中英切换；当前为中文硬编码。
11. **虚拟滚动**：超长会话时只渲染可视区域。
12. **无障碍**：菜单/树/灯箱的焦点陷阱补齐；为流式输出增加 `aria-live` 摘要。
13. **打包（可选）**：如果不再坚持 file://，用 esbuild 产出单文件 dist，同时保留无构建版本。

## 非目标（先不做）

- 账号体系、云同步。
- 真实模型密钥进前端。
- 复杂富文本编辑器。
