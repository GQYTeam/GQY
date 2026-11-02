# GQY app

GQY 的 Go 后端。当前覆盖施工单 001（骨架、配置、鉴权）与 002（provider 适配器、网关运行时、OpenAI Chat 端点）。

## 要求

- Go 1.22+
- 标准库，零第三方运行时依赖

## 运行

```bash
cd app
cp .env.example .env    # 按需修改
go run ./cmd/gqy-server
```

默认监听 `127.0.0.1:5173`，数据根 `~/.gqy`。真实环境变量优先于 `.env`。

## Web UI

```bash
cd ..
make demo       # 构建并用本机 Magpie 配置启动，打开 http://127.0.0.1:5173/
```

- 对话、会话搜索、草稿、附件、思考/工具/问题卡片。
- 控制台：用量、模型、供应商、网关、插件、工具、外观、关于。
- 模型选择器（能力/供应商筛选、最近使用）与会话信息抽屉。
- 主题：paper / midnight / editorial / moss / dawn / dusk；对照页 `http://127.0.0.1:5173/demos.html`。
- 静态文件在 `app/index.html`、`app/js/`、`app/styles/`；Go 进程通过 `GQY_WEB_DIR` 定位，默认在 `app/` 或可执行文件附近查找。

## 检查

```bash
gofmt -l ./cmd ./internal
go vet ./...
go test ./...
go test -race ./...
go build ./cmd/gqy-server
```

或在仓库根 `make check` / `make race` / `make build` / `make smoke-live`（后者对本机 Magpie 或 `UPSTREAM_BASE_URL` 指定的 OpenAI 兼容上游做端到端冒烟）。

## 维护命令

```bash
go run ./cmd/gqy-clean --expired          # 按 TTL 清理过期会话
go run ./cmd/gqy-clean --session <id>     # 删除单个会话
```

## 包结构

```
cmd/gqy-server        服务入口：装配、信号、优雅关闭
internal/buildinfo    ldflags 注入的版本信息
internal/config       环境变量、三份 JSON、快照、校验、重载
internal/storage      数据根、原子写
internal/provider     统一请求/事件/错误、适配器注册表
internal/provider/openai  OpenAI Chat 编码、非流式与 SSE 解码
internal/provider/anthropic  Anthropic Messages 编码、事件族解码
internal/provider/gemini  Gemini generateContent 编码、流式解码
internal/gateway      候选展开、策略、失败转移、冷却、配额、亲和、请求日志
internal/session      会话、事件流、草稿、附件、产物索引
internal/httpapi      路由、鉴权、限流、/v1 端点、状态、会话端点
```

依赖方向：`httpapi → gateway → provider`，下层不 import 上层。

## 配置

三份 JSON 配置支持手动重载：

```bash
curl -X POST http://127.0.0.1:5173/api/config/reload
```

或向进程发 `SIGHUP`。配置文件缺失时使用内置默认；校验失败时保留旧快照。

- `providers.json`：多 provider、多账号、模型。provider id 任意，`protocol` 支持 `openai-chat` / `anthropic-messages` / `gemini`；新增供应商只改配置。
- `routing.json`：默认模型、路由组、重试与冷却上限。
- `settings.json`：限流、工具超时、文件监视开关。

样本见 `../docs/designs/02-网关与路由.md` 与 `../docs/designs/12-配置与热更新.md`。

## 端点

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/healthz` | 健康检查，不需要鉴权 |
| GET | `/metrics` | Prometheus 文本指标；设令牌后需鉴权 |
| GET | `/v1/models` | OpenAI 兼容模型列表（C11） |
| POST | `/v1/chat/completions` | OpenAI 兼容对话；`stream:true` 返回 SSE |
| POST | `/v1/messages` | Anthropic Messages 兼容；流式事件族 |
| POST | `/v1beta/models/{model}:generateContent` | Gemini 兼容；`:streamGenerateContent` 走 SSE |
| POST | `/api/chat` | 内部五事件 SSE（thinking / tool / text / question / done），事件落盘 |
| POST | `/api/sessions` | 创建会话 |
| GET | `/api/sessions` | 会话列表 |
| GET / DELETE | `/api/sessions/{id}` | 会话详情 / 删除 |
| GET / PUT | `/api/sessions/{id}/draft` | 草稿读取 / 保存 |
| POST | `/api/sessions/{id}/uploads` | multipart 附件上传 |
| POST | `/api/sessions/{id}/artifacts` | 追加产物索引 |
| POST | `/api/auth` | 用 `{"token":"…"}` 换取 HttpOnly cookie |
| GET | `/api/config` | 当前配置的安全视图 |
| POST | `/api/config/reload` | 同步重载配置 |
| GET | `/api/gateway/status` | 配置、鉴权、限流与账号运行时状态 |
| GET | `/api/plugins` | 本机插件与运行时状态 |
| POST | `/api/plugins/install` | 从 https Git 插件仓库安装（`git clone`） |
| GET/PUT | `/api/plugins/{id}/settings` | 插件设置 schema / 保存 |
| DELETE | `/api/plugins/{id}` | 卸载并删除插件与设置 |
| GET/PUT | `/api/persona` | 人格、头像与系统提示词 |
| GET | `/api/tools` | 核心/外部工具契约清单 |

请求头：

- `Authorization: Bearer <token>` 或 cookie `gqy_token`
- `X-GQY-Session`：会话 id；同一会话固定 provider/账号（R4）
- 响应头 `X-GQY-Provider`：实际回答的 provider

设置 `GQY_AUTH_TOKEN` 后，`/api`、`/v1`、`/v1beta` 需要令牌；未设置时只允许本机请求。
