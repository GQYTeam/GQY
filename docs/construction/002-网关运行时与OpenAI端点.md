# 002 · 网关运行时与 OpenAI 兼容端点

日期：2026-11-02
状态：完成（2026-11-02）

## 目标

把 001 的配置骨架接成可用的网关：provider 完全由配置决定，协议适配器可注册；`POST /v1/chat/completions` 支持非流式与 SSE 流式，失败转移、冷却、配额、并发与会话亲和按 02 的规则工作。同时建立大型工程化的仓库基线（CI、发布、模板、构建信息）。

## 范围

**做：**

- `internal/provider`：统一请求/响应事件模型、错误归一、适配器注册表（C1、C2、C9、C10）。
- `internal/provider/openai`：OpenAI Chat Completions 的编码、非流式解码、SSE 流式解码（C3、C4、C7）。
- `internal/gateway`：候选展开到账号、order / rotate / usage、冷却、重试、账号并发与有界队列、日 token 配额、会话亲和、`requests.jsonl`（R1–R7、A5–A7）。
- `internal/httpapi`：`POST /v1/chat/completions`、`X-GQY-Provider`、运行时状态（R8、A9）。
- `internal/buildinfo` + `ldflags` 版本注入。
- 仓库工程化：Makefile、CI、CodeQL、GoReleaser、golangci-lint、Dependabot、Issue/PR 模板、CONTRIBUTING、SECURITY、CHANGELOG、EditorConfig。

**不做（后续施工单）：**

- Anthropic Messages / Gemini 适配器（C5、C6）；本单只落地适配器接口与 OpenAI。
- `/api/chat` 的内部五事件协议、agent loop、会话落盘、工具与插件（04–06）。
- 文件监视 fsnotify（CF6）；仍用手动重载与 SIGHUP。
- 远端多实例、配额持久化、订阅 OAuth。

## 关键决定

- **provider 不写死**：代码中不出现任何具体厂商分支；provider id、base_url、模型、账号全部来自 `providers.json`，协议通过注册表按 `protocol` 查适配器。
- **适配器边界**：核心层只认 `provider.Request` / `provider.Event` / `provider.Response` / `provider.Error`；OpenAI 专有字段只在 `internal/provider/openai`。
- **流式切换边界**：适配器收到首个事件前可重试；首个事件交给客户端后不再切换（R1、R3）。
- **账号状态内存态**：冷却、并发、配额、亲和都在 `gateway.Router` 内存里；重启清零（G6、A6）。
- **状态与密钥分离**：运行时状态只输出 `api_key_set`，不输出明文（CF8、A8）。

## 落点

```
app/internal/provider/{types,errors,adapter}.go
app/internal/provider/openai/{adapter,wire,adapter_test}.go
app/internal/gateway/{router,state,router_test}.go
app/internal/httpapi/chat.go
app/internal/buildinfo/buildinfo.go
app/cmd/gqy-server/main.go
.github/workflows/{ci,codeql,release}.yml
.github/ISSUE_TEMPLATE/*.yml
.github/pull_request_template.md
.github/dependabot.yml
.goreleaser.yaml
app/.golangci.yml
Makefile
.editorconfig
CONTRIBUTING.md
SECURITY.md
CHANGELOG.md
```

## 验收

```bash
make check          # fmt + vet + test + build
cd app && go test -race ./...
```

行为验收：

1. 两个任意 id 的 provider 配置后都能被选中；新增 provider 只改 JSON。
2. 首选账号 429 → 冷却 → 下一次请求转移到第二个账号；4xx 不重试。
3. `rotate` 连续两次落在不同成员；`usage` 优先最久未使用的成员。
4. 同一会话两次请求固定同一 provider/账号，响应头 `X-GQY-Provider` 一致。
5. 流式在首帧前失败可转移；已输出后失败不再切换，补错误帧。
6. `requests.jsonl` 每次请求一行，字段完整且不含正文。
7. `/v1/chat/completions` 流式末帧有 `finish_reason`，之后是 `[DONE]`；非流式形状与 OpenAI 兼容。
8. CI 在 Go 1.22 与 stable 上通过；`goreleaser check` 配置有效。

## 留底

完成（2026-11-02）。`app/` 下 `gofmt -l ./cmd ./internal` 无输出、`go vet ./...` 通过、`go test ./...` 全绿（config 7 / storage 2 / gateway 15 / httpapi 13 / provider/openai 10）、`go build ./cmd/gqy-server` 成功。

实际落点与偏差：

- 本单把「网关运行时」与「openai-chat 协议翻译与上游调用」合并交付：`internal/provider`（types/errors/adapter + openai/adapter + openai/wire）、`internal/gateway`（router/state/log + resolve/catalog）、`internal/httpapi/chat.go`（/v1/chat/completions 非流式 + SSE）。
- 工程化基线随本单交付：Makefile、CI、CodeQL、GoReleaser、golangci-lint、Dependabot、Issue/PR 模板、`CONTRIBUTING` / `SECURITY` / `CHANGELOG`、`buildinfo` 版本注入。工作流配置已写入，但本机未执行 GitHub Actions；`goreleaser` 未安装，发布配置按 v2 语法编写，待首次打 tag 验证。
- 行为验收 1–7 由 gateway/httpapi/provider 测试覆盖；验收 8 的 CI 与 goreleaser 需在 GitHub 上首次运行确认。
- 构建产物 `app/gqy-server` 已删除并加入 `.gitignore`。
