# 顾清影 · AI 对话工作台

前端零构建、零依赖；后端是 Go module（标准库，零第三方依赖），按 `cmd` / `internal` 分包，负责模型目录、流式转发、会话落盘与用量聚合。
前端通过 `npm start` 起的 Go 后端工作：模型列表来自 models.dev，聊天请求经 SSE 转发到 OpenAI 兼容网关。

需要 Go >= 1.22（只用标准库，`go.mod` 无第三方 require）。

## 命令

```bash
npm start        # http://127.0.0.1:5173 ，前端 + /api/*（go run ./cmd/gqy-server）
npm run build    # 产出 bin/gqy-server（-trimpath -ldflags "-s -w"），部署用这个
npm run check    # JS 语法检查 + go vet ./...
npm run smoke    # 编译 + 无头 Chrome 跑端到端检查
```

`dev` 与 `start` 相同：Go 没有内置热重载，改后端后手动重启（前端改动刷新浏览器即可）。

## 目录结构

```
cmd/gqy-server/    入口：装配 + 优雅关闭
internal/
  config/          环境变量解析（.env / 密钥）
  catalog/         models.dev 拉取、缓存、过滤、元数据
  provider/        SSE 转发：chat / responses 协议
  session/         会话落盘 + 用量聚合
  httpapi/         路由、中间件、JSON、静态白名单
index.html         应用外壳
app.js             前端控制器
js/                前端模块（core/markdown/clawd/api）
```

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
npm start                 # server.go 自动读取 .env，真实环境变量优先
```

| 变量 | 默认 | 说明 |
| --- | --- | --- |
| `GQY_API_KEY` | 空 | OpenAI 兼容接口的密钥 |
| `GQY_API_BASE` | `https://api.deepseek.com/v1` | 任何兼容 `/chat/completions` 的网关 |
| `GQY_MODEL` | `deepseek-chat` | 默认模型；前端未选择时用这个值 |
| `GQY_API_WIRE` | `auto` | 请求协议：`auto` / `chat` / `responses`。`auto` 先试 `/chat/completions`，上游说只支持 Responses 时自动改走 `/responses` |
| `GQY_API_UA` | 空 | 上游 `User-Agent`。部分中转站（如 agentrouter.org）按 UA 拦截非官方客户端 |
| `GQY_MOCK` | 空 | `1` 时用本地模拟流，用于没有密钥时验证链路 |
| `GQY_HOME` | `~/.gqy` | 会话落盘根目录 |
| `HOST` / `PORT` | `127.0.0.1` / `5173` | 默认只监听本机，生产对外设 `HOST=0.0.0.0` |

## 接口

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| `GET` | `/api/health` | `{ ok, provider: 'openai-compatible' \| 'mock' \| 'none', model }` |
| `GET` | `/api/models` | models.dev 精简聊天模型列表（服务端缓存 6 小时），含 context / 模态 / 成本 |
| `GET` | `/api/usage` | 服务端聚合的真实用量：会话数、消息数、产物数、token、近 7 天消息量 |
| `POST` | `/api/chat` | 请求 `{ messages, model, effort }`，返回 SSE：`thinking` / `tool` / `text` / `done` / `error` / `end` |
| `GET` | `/api/sessions` | 会话 manifest 列表 |
| `GET` | `/api/sessions/:id` | 读回 `messages.jsonl` |
| `PUT` | `/api/sessions/:id` | 写入会话（按 `docs/AboutProjectFile.md` 的目录规范） |
| `DELETE` | `/api/sessions/:id` | 删除会话目录 |

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
```

## 目录

```
index.html              唯一入口：应用外壳 + 各页面 <section class="view">
app.js                  控制器：状态、视图、流式管线、事件接线
cmd/gqy-server/         服务端入口
internal/               config / catalog / provider / session / httpapi
js/
  core.js               工具与常量（esc / uid / 时间 / 格式化 / token 估算）
  api.js                后端适配层（探测 / 模型目录 / 用量 / 流式聊天 / 会话镜像）
  markdown.js           Markdown、代码高亮、链接卡片、URL 协议白名单
  clawd.js              吉祥物精灵播放 + 活动状态机
scripts/smoke.mjs       端到端冒烟（CDP，无依赖）
styles/                 base → shell → chat → ui → pages → panels → media → settings → responsive
assets/                 头像、字标、favicon、封面、Clawd 精灵图
docs/                   架构、页面、数据、测试、路线图
```

加载顺序固定：`core → markdown → clawd → api → app`。
共享能力挂在 `window.GQY`，模块之间不互相 import。

## 数据

- 前端以 `localStorage['claude-webchat:v7']` 为准，刷新不丢；配额写满会明确提示，不再静默失败。
- 服务端可用时，每次保存会把当前会话镜像到 `$GQY_HOME/sessions/<id>/`，日志与产物落盘。
- 附件目前只在本地：图片用 `blob:` 预览，序列化前剔除，不写入磁盘。

## 安全边界

- 服务端默认只绑定 `127.0.0.1`；没有账号体系，不要直接暴露到公网。
- 模型密钥只存在于服务端环境变量；前端只会看到模型名。
- 模型目录由服务端向 models.dev 拉取并缓存，前端只消费精简结果，不直接访问 models.dev。
- Markdown 只放行 `http(s)` / `mailto` / 相对路径，`javascript:` / `data:` 降级成纯文本。
- 静态托管走扩展名白名单，`*.go`、`package.json`、`go.mod`、`scripts/`、`bin/`、点文件都不可访问。
- 工具日志写入前对 `key` / `token` / `secret` / `authorization` 类参数脱敏。
- 会话目录 0700、文件 0600，写入走临时文件 + rename；越界路径与非法会话 id 直接拒绝。
