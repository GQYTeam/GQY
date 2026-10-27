# 顾清影 · AI 对话工作台

前端零构建、零依赖；后端是 Go module（标准库，零第三方依赖），按 `cmd` / `internal` 分包，负责静态托管、模型目录、SSE 转发、agent loop（模型 ↔ 工具多轮调用）、会话落盘、用量聚合，以及 GitHub 只读账户 OAuth。
前端通过 `npm start` 起的 Go 后端工作：模型列表来自 models.dev，聊天请求经 SSE 转发到 OpenAI 兼容网关；模型要调工具时由服务端执行并回填，前端只消费事件流。

需要 Go >= 1.22（只用标准库，`go.mod` 无第三方 require）。

## 命令

```bash
npm start        # http://127.0.0.1:5173 ，前端 + /api/*（go run ./cmd/gqy-server）
npm run dev:watch # 同上，用 air 监听 Go 源码自动重启（需自行安装 air）
npm run build    # 产出 bin/gqy-server（-trimpath -ldflags "-s -w"），部署用这个
npm run check    # JS 语法检查 + go vet ./...
npm run smoke    # 编译 + 无头 Chrome 跑端到端检查
npm run files    # 列出项目文件
```

`dev` 与 `start` 相同：Go 没有内置热重载，改后端后手动重启（前端改动刷新浏览器即可）。

### 导入旧备份

`cmd/gqy-import` 把旧架构备份目录（`gqy_backup_latest`）迁到 `$GQY_HOME`：

```bash
go run ./cmd/gqy-import -src /path/to/gqy_backup_latest [-dst ~/.gqy] [-dry-run]
```

依赖系统 `sqlite3` 命令。会话从 `home/mac/conversation.db` 读出后按 `session.Store` 规范落盘；`personas` / `extensions/skills` / `data/kb` / `data/prompts` 分别复制到 `$GQY_HOME/{personas,skills,kb,prompts}`；`state/usage.json` 与 `config/config.jsonc` 里的 providers 分别写成 `usage.json`、`providers.json`（`usage.json` 目前只导入，服务端用量仍按会话聚合）。`-dry-run` 只打印将要导入的内容。

## 目录结构

```
cmd/gqy-server/    入口：装配 + 优雅关闭
cmd/gqy-import/    旧备份导入器（sqlite3 → $GQY_HOME）
internal/
  config/          环境变量解析（.env / 密钥）
  catalog/         models.dev 拉取、缓存、过滤、元数据
  provider/        SSE 转发：chat / responses 协议
  agent/           agent loop：模型 ↔ 工具多轮调用
  tool/            工具契约、注册表、核心工具、外部工具
  session/         会话落盘 + 用量聚合
  github/          GitHub OAuth 只读账户
  httpapi/         路由、中间件、JSON、静态白名单
index.html         应用外壳
app.js             事件接线 + boot
js/                前端模块（core/render/chat/files/menu/settings 等）
```

## 工具与 agent loop

服务端启动时把工具注册进 `tool.Registry`（`internal/httpapi.New`），重名直接报错。
agent loop 在 `internal/agent`：发请求 → 若模型返回工具调用则执行 → 结果回填为 `tool` 消息 → 继续请求，直到最终答案、`ask_user` 中断或超过 20 轮。每轮 usage 累加后随 `done` 事件返回。

**核心工具**（默认注入，`KindCore`）：

| 工具 | 行为 |
| --- | --- |
| `read` | 读文件；png/jpg/jpeg/gif/webp 返回 base64 data URI |
| `edit` | 覆盖写入文件，自动建父目录 |
| `shell` | `sh -c` 执行命令，合并 stdout+stderr，最多 16 KB |
| `list_dir` | 列目录，目录名以 `/` 结尾 |
| `websearch` | 未接搜索后端，调用直接返回错误，避免模型编造 |
| `ask_user` | 中断 loop，向前端发 `question` 事件 |

**外部工具**（`KindExternal`，默认不注入，`GQY_ENABLE_EXTERNAL=1` 才随请求下发）：

| 工具 | 说明 |
| --- | --- |
| `provider_usage` | 查 OpenAI 兼容供应商余额（DeepSeek `/user/balance`）；低于 `GQY_LOW_BALANCE` 时提示模型收尾 |

`GQY_MOCK=1` 时 `/api/chat` 走本地模拟流，**不经过 agent loop**，工具不会执行；验证工具链路要用假上游或真实模型。

`ask_user` 的当前实现：服务端发 `question` 后结束本轮；前端渲染提问卡片，用户回答作为新 user 消息再发一次请求。工具调用上下文不随回答回传，服务端每轮请求都是无状态的。

新增工具：实现 `tool.Tool`（`Name/Description/Schema/Execute/Kind`）→ 在 `internal/httpapi.New` 注册 → 需要默认注入就标 `KindCore`。详见 `docs/tools.md`。

## 生产部署

后端是单个静态二进制，部署就是把二进制和前端目录放在一起：

```bash
npm run build            # 产出 bin/gqy-server
mkdir -p dist && cd dist
cp ../bin/gqy-server .
cp -R ../assets ../js ../styles .
cp ../index.html .
GQY_API_KEY=sk-xxx PORT=8080 ./gqy-server
```

二进制不再要求工作目录必须是 `app/`：它优先读当前目录的 `index.html`，找不到就退到可执行文件所在目录，所以 `gqy-server` 可以脱离源码树直接运行。`.env` 也优先读可执行文件旁的，再读当前目录的（真实环境变量始终最高优先）。

```bash
# systemd（示例 /etc/systemd/system/gqy.service）
[Service]
WorkingDirectory=/opt/gqy
ExecStart=/opt/gqy/gqy-server
EnvironmentFile=/opt/gqy/.env
Restart=on-failure
```

Docker 也可以直接跑（静态二进制，基础镜像最小）：

```dockerfile
# Dockerfile
FROM alpine:3.19
COPY gqy-server /gqy-server
COPY assets /assets
COPY js /js
COPY styles /styles
COPY index.html /index.html
ENV HOST=0.0.0.0 PORT=8080
EXPOSE 8080
CMD ["/gqy-server"]
```

服务端默认只监听 `127.0.0.1`；要对外提供服务，设 `HOST=0.0.0.0` 并**务必在前面加反向代理鉴权**（见「安全边界」）。生产日志里每个 `/api/*` 请求带 `X-Request-ID`，用 `-trimpath` 构建避免泄漏本地路径。

## 接真实模型

服务端只用环境变量，密钥不进入前端：

```bash
cp .env.example .env      # 填 GQY_API_KEY
npm start                 # cmd/gqy-server 自动读取 .env，真实环境变量优先
```

| 变量 | 默认 | 说明 |
| --- | --- | --- |
| `GQY_API_KEY` | 空 | OpenAI 兼容接口的密钥 |
| `GQY_API_BASE` | `https://api.deepseek.com/v1` | 任何兼容 `/chat/completions` 的网关 |
| `GQY_MODEL` | `deepseek-chat` | 默认模型；前端未选择时用这个值 |
| `GQY_API_WIRE` | `auto` | 请求协议：`auto` / `chat` / `responses`。`auto` 先试 `/chat/completions`，上游说只支持 Responses 时自动改走 `/responses` |
| `GQY_API_UA` | 空 | 上游 `User-Agent`。部分中转站（如 agentrouter.org）按 UA 拦截非官方客户端 |
| `GQY_MOCK` | 空 | `1` 时用本地模拟流，用于没有密钥时验证链路（不执行工具） |
| `GQY_HOME` | `~/.gqy` | 会话落盘根目录 |
| `GQY_ENABLE_EXTERNAL` | 空 | `1` 时把外部工具（当前为 `provider_usage`）也注入模型 |
| `GQY_LOW_BALANCE` | `0` | `provider_usage` 的余额提醒阈值（单位与供应商一致，如 CNY 元）；`0` 表示不提醒 |
| `GITHUB_CLIENT_ID` | 空 | GitHub App 的 Client ID，用于只读账户 OAuth |
| `GITHUB_CLIENT_SECRET` | 空 | GitHub App 的 Client Secret |
| `GITHUB_OAUTH_REDIRECT` | 空 | 回调地址；空时按请求 Host 推导 `/api/github/callback` |
| `HOST` / `PORT` | `127.0.0.1` / `5173` | 默认只监听本机，生产对外设 `HOST=0.0.0.0` |

## 接口

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| `GET` | `/api/health` | `{ ok, provider: 'openai-compatible' \| 'mock' \| 'none', model, home, version }` |
| `GET` | `/api/models` | models.dev 精简聊天模型列表（服务端缓存 6 小时），含 context / 模态 / 成本 / `toolCall` |
| `GET` | `/api/usage` | 服务端聚合的真实用量：会话数、消息数、产物数、token、近 7 天消息量 |
| `GET` | `/api/providers` | 读 `$GQY_HOME/providers.json`（`gqy-import` 导入），返回 `{ providers: [...] }`；文件不存在时返回空数组 |
| `POST` | `/api/chat` | 请求 `{ messages, model, effort }`，返回 SSE：`thinking` / `tool` / `text` / `question` / `done` / `error` / `end` |
| `GET` | `/api/github/auth` | `{ url }`：GitHub 授权页地址；未配置时 503 |
| `GET` | `/api/github/callback` | OAuth 回调：换 token、读账户、落盘，然后跳回 `/?github=connected&login=...` |
| `GET` | `/api/github/me` | `{ connected, account? }`；只返回账户信息，不返回 token |
| `DELETE` | `/api/github` | 删除服务端落盘的 token |
| `GET` | `/api/sessions` | 会话 manifest 列表 |
| `GET` | `/api/sessions/:id` | 读回 `messages.jsonl` |
| `PUT` | `/api/sessions/:id` | 写入会话（按 `docs/AboutProjectFile.md` 的目录规范） |
| `DELETE` | `/api/sessions/:id` | 删除会话目录 |

SSE 事件：

| 事件 | 载荷 | 说明 |
| --- | --- | --- |
| `thinking` | `{ delta }` | 思考增量 |
| `tool` | `{ tool: { name, detail, status, ms, output } }` | 每次工具执行后发一条；`detail` 是参数 JSON，`status` 为 `done` / `error` |
| `text` | `{ delta }` | 最终答案增量（服务端按 8 个字符一块发） |
| `question` | `{ question: { text, options } }` | `ask_user` 中断，前端渲染提问卡片 |
| `done` | `{ usage: { prompt_tokens, completion_tokens, total_tokens } }` | 本轮累计 usage |
| `error` | `{ code, message }` | 上游或 agent 失败 |
| `end` | — | 流结束 |

落盘结构：

```
$GQY_HOME/sessions/<session_id>/
  manifest.json
  io/context/context.md
  io/context/messages.jsonl
  io/output/artifacts/<产物文件名>
  io/output/index.json
  logs/tools/YYYY-MM-DD.jsonl
  tmp/                       # 预留给上传/中间产物
$GQY_HOME/github_token.json  # GitHub OAuth token，0600
$GQY_HOME/providers.json     # gqy-import 导入的供应商清单，0600
```

## 目录

```
index.html              唯一入口：应用外壳 + 各页面 <section class="view">
app.js                  事件接线 + boot（状态与服务在 js/store.js / js/*.js）
cmd/gqy-server/         服务端入口
cmd/gqy-import/         旧备份导入器
internal/               config / catalog / provider / agent / tool / session / github / httpapi
js/
  core.js               工具与常量（esc / uid / 时间 / 格式化 / token 估算）
  markdown.js           Markdown、代码高亮、链接卡片、URL 协议白名单
  clawd.js              吉祥物精灵播放 + 活动状态机
  api.js                后端适配层（探测 / 模型目录 / 用量 / 流式聊天 / 会话镜像 / GitHub）
  store.js              共享 store：状态、DOM 引用、常量、save/镜像入口
  render.js             消息 HTML、侧栏、线程、上下文用量浮层
  chat.js               发送、流式管线、会话切换与镜像
  files.js              文件面板、代码/图片灯箱、提问卡片
  menu.js               浮层菜单、推理滑块、模型选择、附件、语音输入
  settings.js           设置窗口、用量、导入导出、扩展与 GitHub
scripts/smoke.mjs       端到端冒烟（CDP，无依赖）
skills/gh.md            给 agent 的 gh 命令参考（通过 shell 使用）
styles/                 base → shell → chat → ui → pages → panels → media → settings → responsive
assets/                 头像、字标、favicon、封面、Clawd 精灵图
docs/                   架构、页面、数据、测试、路线图、工具
```

加载顺序固定：`core → markdown → clawd → api → store → render → chat → files → menu → settings → app`。
共享能力挂在 `window.GQY`，服务实例挂在 `window.GQY.app`，模块之间不互相 import。

## 数据

- 前端以 `localStorage['claude-webchat:v7']` 为准，刷新不丢；配额写满会明确提示，不再静默失败。
- 服务端可用时，每次保存会把当前会话镜像到 `$GQY_HOME/sessions/<id>/`，日志与产物落盘。镜像字段是 `text / thinking / tools / artifact / usage`；`question` 与附件不落服务端。
- `ask_user` 的回答会先写成本地 user 消息，再触发一次新的 `/api/chat` 请求。
- 附件目前只在本地：图片用 `blob:` 预览，序列化前剔除，不写入磁盘。

## 安全边界

- 服务端默认只绑定 `127.0.0.1`；没有账号体系，不要直接暴露到公网。
- 模型密钥只存在于服务端环境变量；前端只会看到模型名。
- 模型目录由服务端向 models.dev 拉取并缓存，前端只消费精简结果，不直接访问 models.dev。
- Markdown 只放行 `http(s)` / `mailto` / 相对路径，`javascript:` / `data:` 降级成纯文本。
- 静态托管走扩展名白名单，`*.go`、`package.json`、`go.mod`、`scripts/`、`bin/`、`cmd/`、`internal/`、`tmp/`、点文件都不可访问。
- **工具以服务端进程权限执行**：`read` / `edit` / `shell` / `list_dir` 没有沙箱和路径白名单。只在本机、可信工作区使用；`GQY_ENABLE_EXTERNAL` 只控制工具是否注入模型，不是权限开关。
- `ask_user` 是交互暂停，不是安全确认；不要把它当授权闸门。
- GitHub token 只存服务端 `$GQY_HOME/github_token.json`（0600），`/api/github/me` 不返回 token。
- 工具日志写入前对 `key` / `token` / `secret` / `authorization` / `password` 类参数脱敏。
- 会话目录 0700、文件 0600，写入走临时文件 + rename；越界路径与非法会话 id 直接拒绝。

## 文档

- `docs/architecture.md`：模块、状态流、流式管线
- `docs/pages.md`：页面与组件地图
- `docs/data-model.md`：state / message / localStorage
- `docs/tools.md`：工具契约、核心/外部工具、agent loop 与 ask_user
- `docs/testing.md`：手动与 headless 验证
- `docs/roadmap.md`：未来计划与优先级

产品与设计约定在仓库根 `docs/`，索引见 `docs/README.md`：`AboutUiPages.md`、`AboutProjectFile.md`、`AboutAiOtput.md`、`AboutPlugin.md`、`AboutAitestCallToolOrPlugin.md`。
