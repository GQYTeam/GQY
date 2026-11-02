# frontend · 前端界面

## 1. 状态

部分实现（2026-11-02）：零构建界面、会话/流式、控制台、模型选择器、上下文抽屉、六套主题。

## 2. 对应设计决定

兑现 `designs/10-前端界面.md` 的 FE1–FE9：

- FE1 零构建形态：index.html + 原生 JS 模块 + CSS，Go 静态托管；TypeScript 化只指一句。
- FE2 视图与浮层：对话 / 看板两个视图，设置 / 用量 / 扩展 / 灯箱 / 引导为浮层，Esc 关闭顺序固定。
- FE3 页面清单与职责：对话、灯箱、设置、看板、用量、引导、关键词浮窗，各带数据来源。
- FE4 消息结构：五种 SSE 事件（thinking / tool / text / question / done）按序重建消息块。
- FE5 渲染细节：思考默认折叠、工具轨迹带参数 / 输出 / 耗时 / 状态、ask_user 卡片、产物灯箱。
- FE6 草稿 / 重试 / 停止：草稿按会话存服务端，停止保留增量，重试旧回复折叠。
- FE7 附件上传：拖拽 + 选择，文本注入 / 图片多模态 / 二进制落盘，前端预校验。
- FE8 状态与本地存储：会话数据服务端为真相，localStorage 只放 UI 偏好；密钥只存服务端。
- FE9 鉴权与安全：`/api`、`/v1` 带 cookie / Bearer，前端不存密钥不落正文。

## 3. 具体形状

### 3.1 静态文件布局

由 Go 进程静态托管（01 D2），无构建链：

```
app/
  index.html        # 单页入口
  js/               # 原生 ES 模块
    api.js          # 网络与 SSE 消费
    store.js        # 状态与 localStorage
    render.js       # 消息块渲染
    chat.js         # 对话与输入框
    settings.js     # 设置页
    board.js        # 看板
    glossary.js     # 关键词浮窗
    markdown.js     # Markdown 渲染
    i18n.js         # 语言
    menu.js         # 侧栏 / 浮层
    onboarding.js   # 初始化引导
    files.js        # 附件与灯箱
  styles/           # CSS，theme.css 承载暗色令牌
```

- `index.html` 引用上述模块与样式；`data-theme=dark` 切换暗色。
- 旧实现 `app/js` 的模块清单只作线索，以本页为准。

### 3.2 页面 / 浮层清单与数据来源

| 页面 / 浮层 | 职责 | 数据来源 |
| --- | --- | --- |
| 对话 | 输入框 + 消息流；发送、停止、重试、附件、消息块 | `/api/chat`（SSE）、`/api/sessions` |
| 灯箱 | 文件面板、代码高亮、图片预览 | `/api` 产物接口 |
| 设置 | 模型 & 供应商、路由组、工具开关、人设、记忆、知识库、用量、软件包、扩展、账户、语音 | `/api/models`、`/api/tools`、`/api/plugins`、`/api/usage`、`/api/gateway/status` 等 |
| 看板 | 本机任务卡 todo / doing / done | localStorage |
| 用量 | 本地占用、token、7 天趋势、各 provider 调用；⚡ 浮层保留 | `/api/usage`、`/api/gateway/status` |
| 初始化引导 | 首次三步：工作区名称 → 模型（可空）→ 动效偏好；可跳过 | `/api/models` + localStorage |
| 关键词浮窗 | 正文术语点按：术语、定义、来源 | localStorage 术语表 |

### 3.3 五种 SSE 事件到 DOM 行为

前端只认 05 S2 的五种事件：

| 事件 | DOM 行为 |
| --- | --- |
| `thinking` | 追加 / 更新思考块，默认折叠，可展开 |
| `tool` | 新建或更新工具轨迹块：参数 / 输出 / 耗时 / 状态 |
| `text` | 追加正文文本节点 |
| `question` | 渲染 ask_user 卡片，锁定输入区等待作答 |
| `done` | 标记回复结束，显示用量与停止原因，解除输入锁定 |

- 事件是追加投影：按序到达就按序渲染；非流式请求返回同样序列，一次渲染完。
- 断开但未收到 `done`：保留已渲染增量，标记回复不完整，可重试。

### 3.4 ask_user 卡片交互

- 参数形状（06 T7）：

```json
{"question": "要继续吗？", "options": ["继续", "取消"]}
```

- 选项渲染为 1..n 按钮；数字键直接选中；另有自定义答案输入框。
- 提交后回填模型，卡片转为已答状态；Esc 只关自定义输入，卡片保留。
- 作答前正文流暂停，输入框锁定，不发送新消息。

### 3.5 草稿 / 停止 / 重试

| 交互 | 行为 |
| --- | --- |
| 草稿 | 输入框内容随会话保存 / 恢复，存服务端会话数据；切换会话前保存 |
| 停止 | 发停止信号（AbortController 语义）；已产出增量保留；进行中工具标 `canceled` |
| 重试 | 以当前消息为界重新发起；旧回复折叠标记「被替代」 |

### 3.6 附件上传预校验

- 入口：拖拽到输入区 + 点击选择。
- 前端预校验：类型（文本 / 图片 / 其他二进制）与大小；不通过直接提示，不上传。
- 上传中显示进度，失败显示原因；图片上传后可预览、移除。
- 同名并发不覆盖由服务端 O_EXCL 保证（05 S3）。

### 3.7 localStorage 键清单与服务端真相边界

localStorage 只放 UI 偏好与轻量状态：

| 键 | 内容 |
| --- | --- |
| `gqy.theme` | 主题 |
| `gqy.locale` | 语言 |
| `gqy.reduceMotion` | 动效偏好 |
| `gqy.lastView` | 上次视图 |
| `gqy.board` | 看板卡 |
| `gqy.glossary` | 术语表 |
| `gqy.voiceEnabled` | 语音开关 |
| `gqy.onboarded` | 引导完成标记 |

- localStorage 不存：消息正文、草稿、产物、密钥、token。
- 服务端为真相：会话列表、消息、草稿、产物走 `/api`（09 ST2）。
- 密钥只存服务端：设置页只显示 `api_key_set`，不回显明文（08 A8、09 ST6、12 CF8）。

### 3.8 鉴权

- 所有 `/api`、`/v1` 请求自动带 cookie（同源）；设置 `GQY_AUTH_TOKEN` 时同时支持 Bearer（02 R8）。
- 前端不保存任何供应商密钥，不落 prompt / 回复正文（G6）。

## 4. 边角情况

- **流中断**：服务端可能补错误帧或直接断开；前端未收到 `done` 时保留已渲染增量，标记回复不完整，可重试。
- **思考展开状态**：每个思考块独立折叠状态，存消息块内存，不持久化到 localStorage。
- **工具轨迹折叠**：长输出默认截断，展开查看全文；error / timeout / canceled 状态块标色。
- **图片灯箱大文件**：灯箱按需加载；超大图片提示下载而非内联预览，避免阻塞页面。
- **窄屏**：看板三列改纵向堆叠；浮层可滚动；Esc 先关浮层，再看板，最后回对话。

## 5. 由哪些测试守着

计划中的测试（旧 `app/js` 只作线索）：

- `TestFrontendRenderEvents`：五种事件各渲染成对应消息块。
- `TestFrontendThinkingCollapse`：思考块默认折叠、可展开。
- `TestFrontendAskUser`：数字键、选项按钮、自定义答案都能回填。
- `TestFrontendDraftStopRetry`：草稿切会话保存 / 恢复；停止保留增量；重试折叠旧回复。
- `TestFrontendUpload`：拖拽 / 选择上传、预校验、进度与失败提示。
- `TestFrontendLocalStorageBoundary`：localStorage 只含 UI 偏好，无密钥与消息正文。
- `TestFrontendNoApiKeyLeak`：设置页只显示 `api_key_set`，页面不出现明文密钥。

## 6. 对应代码

待建。旧实现参考：`app/index.html`、`app/js`、`app/styles`，仅事实线索，不照抄。


## 7. 当前实现（2026-11-02）

### 文件

- `app/index.html`：单页入口（对话 + 控制台 + 上下文抽屉 + 引导 + 登录）。
- `app/demos.html`：六套视觉方向对照页。
- `app/styles/{tokens,base,app,themes,demos}.css`：令牌、结构、主题与 Demo 页。
- `app/js/{app,api,store,themes,render,markdown,chat,sessions,settings,context,picker,board,onboarding,ui}.js`：原生 ES 模块。
- Go 侧 `internal/httpapi/static.go` 提供静态托管；`GQY_WEB_DIR` 可覆盖目录。

### 已实现

- 对话：`/api/chat` SSE 五事件（thinking / tool / text / question / done）投影；Markdown-lite、代码块、工具轨迹、思考折叠、用量、停止、重试、ask_user 选项卡片。
- 会话：创建/切换/删除/搜索、服务端草稿、URL `?session=` 可回打开、产物索引读取。
- 附件：图片 data URL、文本文件注入、拖拽/粘贴、数量与大小预校验。
- 控制台：用量（每回合 bar + 账号配额）、模型、供应商（只读安全视图）、网关（健康/冷却/重载）、插件（扫描/加载/卸载）、工具（契约与 schema）、外观、关于。
- 模型选择器：搜索、能力筛选、供应商筛选、最近使用、路由组/模型分组；读取 `/v1/models` 的 `capabilities`。
- 上下文抽屉：会话 ID、模型、消息数、上下文估算、工具轨迹、产物索引。
- 桌面化：⌘K 命令面板、快捷键、会话日期分组、侧边栏收起、回到底部、自定义确认对话框、PWA manifest/图标。
- 主题：paper / midnight / editorial / moss / dawn / dusk；减少动效；引导与视觉 Demo 对照页。
- 来源：`/api/chat`、`/api/sessions`、`/api/config`、`/api/gateway/status`、`/api/plugins`、`/api/tools`、`/v1/models`、`/healthz`。

### 未实现 / 偏差

- 参考 `yxxbc/gqy-agent` 的人格、记忆、知识库、图库、记账、QQ/iMessage 等控制台页面未接入；本 UI 只做网关与会话。
- 产物只读索引，没有预览器/代码高亮/灯箱多标签；当前只有图片灯箱。
- 二进制附件只提示不支持，未做服务端上传与文本提取（图片/文本已可用）。
- 语音、唤醒词、多用户登录/注册、邀请码未实现；鉴权仍是单令牌 `GQY_AUTH_TOKEN`。
- 前端有 `scripts/check-js.sh` 语法检查与 `scripts/ui-smoke.sh`（headless Chrome 检查对话页/控制台/供应商/工具/Demo 页）；Go 侧有静态托管测试。
- 参考仓库为 PolyForm Noncommercial 1.0.0；本 UI 只借鉴信息架构与 dawn/dusk 配色，未复制其代码或品牌素材，见 `docs/third-party/`。

### 验收

```bash
make demo                       # 默认接本机 Magpie
# 打开 http://127.0.0.1:5173/ 与 /demos.html
make js-check                   # JS 语法
make ui-smoke                   # headless Chrome 页面冒烟
go test ./internal/httpapi      # 静态托管、会话、SSE 端点
```

