# 014 · Web UI 体验版

日期：2026-11-02
状态：完成

## 目标

把已有的网关、会话与工具能力接到一个可操作的零构建 Web UI：打开浏览器就能对话、切会话、看思考与工具轨迹、用控制台看用量/模型/网关/插件，并提供多套视觉方向供挑选。

## 范围

**做：**

- 零构建页面与 Go 静态托管（`internal/httpapi/static.go`）。
- 对话视图：`/api/chat` 五事件、Markdown-lite、思考折叠、工具轨迹、问题卡片、停止/重试。
- 会话：列表、搜索、切换、删除、服务端草稿、URL `?session=`。
- 附件：图片 data URL、文本注入、拖拽/粘贴、预校验。
- 控制台：用量、模型、网关、插件、外观、关于。
- 模型选择器：搜索、能力/供应商筛选、最近使用、路由组。
- 上下文抽屉：会话信息、上下文估算、工具轨迹、产物索引。
- 主题：paper / midnight / editorial / moss / dawn / dusk；`demos.html` 对照页。
- 控制台补充「供应商」只读面板、「工具」契约面板与 `GET /api/tools`。
- `scripts/check-js.sh` 与 `scripts/ui-smoke.sh`，CI 增加 Node 语法与 headless UI 冒烟。
- 桌面化：⌘K 命令面板、快捷键、会话日期分组、侧边栏收起、回到底部、自定义确认对话框、PWA manifest/图标。
- 安全：统一 CSP / nosniff / frame / referrer / permissions 响应头。
- 后端配合：静态托管、`X-GQY-Session` 响应头、会话标题、问题文本、只记录本轮用户事件、空草稿修复、工具轨迹重建。

**不做：**

- 参考仓库的人格、记忆、知识库、图库、记账、QQ/iMessage、语音与多用户体系。
- 产物预览器、代码高亮、多标签灯箱。
- 二进制附件上传与文本提取。
- 前端 DOM 自动化测试（本单用 headless Chrome 做人工视觉冒烟）。

## 关键决定

- 视觉方向参考 `yxxbc/gqy-agent`（PolyForm Noncommercial 1.0.0），只借鉴信息架构与 dawn/dusk 配色；不复制其代码、品牌或角色素材。许可证与 Notice 放 `docs/third-party/`。
- UI 不引入构建链与框架；全部原生 ES modules + CSS 变量。
- 服务端为真相：消息/草稿/产物在 `$GQY_HOME`；localStorage 只存主题、模型、动效与看板。
- 静态目录由 `GQY_WEB_DIR` 覆盖；默认在 `app/` 或可执行文件附近查找。

## 落点

```
app/index.html
app/demos.html
app/styles/{tokens,base,app,themes,demos}.css
app/js/{app,api,store,themes,render,markdown,chat,sessions,settings,context,picker,board,onboarding,ui}.js
app/internal/httpapi/static.go
app/internal/httpapi/server.go
app/internal/httpapi/chat.go
app/internal/httpapi/session.go
app/internal/session/{session,events}.go
scripts/demo.sh
```

## 验收

```bash
make demo
# http://127.0.0.1:5173/ 与 /demos.html
make check
go test ./internal/httpapi ./internal/session
```

## 留底

完成（2026-11-02）。`make check`、`make race`、`golangci-lint`（0 issues）、`actionlint` 全部通过；用本机 Magpie 做了真实对话、工具调用、会话切换与控制台截图冒烟。已知偏差见 `docs/blueprint/frontend.md` 第 7 节。
