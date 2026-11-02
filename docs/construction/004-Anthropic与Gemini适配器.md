# 004 · Anthropic Messages 与 Gemini 适配器

日期：2026-11-02
状态：进行中

## 目标

在 002 的 provider 注册表与网关运行时之上，新增两个协议适配器：Anthropic Messages 与 Gemini generateContent。`/v1/messages` 与 `/v1beta/models/{model}:generateContent` 走同一套「解析请求 → 网关路由 → 真实上游 → 流式输出」，非流式与流式等价，错误归一喂给 R3。

## 范围

**做：**

- `internal/provider/anthropic`：Messages 编码/解码（C5）——thinking 块、tool_use/tool_result、stop_reason、流式事件族；thinking signature 原样透传不落日志（C3）。
- `internal/provider/gemini`：generateContent 编码/解码（C6）——generationConfig、functionCall/functionResponse、finishReason/usageMetadata。
- `internal/httpapi`：`POST /v1/messages`（Anthropic 形状）与 `POST /v1beta/models/{model}:generateContent`（Gemini 形状，`alt=sse` 流式）。
- `cmd/gqy-server`：注册 anthropic-messages 与 gemini 两个适配器。
- 非流式 = 流式事件按序拼接；上游只流式时聚合回整段；流中断返回 502，不许空 200（C7）。
- 错误归一复用 `provider.FromStatus` / `NetworkError`（C9/C10）。

**不做（后续施工单）：**

- OpenAI Responses 端点、embedding、图片/视频。
- 流式边界样本的落地（随 005 一并补 `docs/designs/samples/03-协议翻译/`，本单只保证解析正确）。
- Anthropic/Gemini 的订阅账号 OAuth。

## 关键决定

- 适配器只新增、不改核心：provider 包与网关运行时零改动；新增协议 = 新增包 + 注册（G4）。
- **thinking 独立于 text**（C3）：Anthropic 的 `thinking` 块与 `text` 块分开进事件流；`signature` 只在适配器内部透传，不进日志、不进统一模型。
- **端点与模型解析解耦**：`/v1beta/models/{model}:generateContent` 的 `{model}` 照常交给网关四级解析（R6），不因为路径里有 model 就绕过路由组。
- **错误归一**：Anthropic/Gemini 的错误体各自解析后归一成 `provider.Error`，R3 只消费 `Retryable` 与 `Code`。

## 落点

```
app/internal/provider/anthropic/adapter.go
app/internal/provider/anthropic/wire.go
app/internal/provider/anthropic/adapter_test.go
app/internal/provider/gemini/adapter.go
app/internal/provider/gemini/wire.go
app/internal/provider/gemini/adapter_test.go
app/internal/httpapi/anthropic.go
app/internal/httpapi/gemini.go
app/internal/httpapi/server.go        # 仅加两条路由
app/cmd/gqy-server/main.go            # 注册两个适配器
```

## 验收

```bash
cd app
gofmt -l ./cmd ./internal
go vet ./...
go test ./...
go build ./cmd/gqy-server
```

行为验收：

1. Anthropic 非流式请求 → 统一响应再译回 Messages 形状；thinking/text/tool_use/stop_reason 字段一一对应。
2. Anthropic 流式事件族（message_start/content_block_delta/message_delta/message_stop）正确解析为统一事件流。
3. Gemini 非流式与 `alt=sse` 流式都正确解析；functionCall/functionResponse、finishReason、usageMetadata 映射正确。
4. 上游只流式时聚合回整段；流中断返回 502 且请求日志 `stream_interrupted`。
5. 错误归一：Anthropic 429 → rate_limited 可重试；Gemini 400 无效参数 → invalid_request 不重试。
6. 两个适配器注册后，`/v1/models` 不变；provider 配置 `protocol` 写 `anthropic-messages` / `gemini` 可被网关选中。

## 留底

完成（2026-11-02）。四条验收命令在 `app/` 下全部通过：`gofmt -l ./cmd ./internal` 无输出、`go vet ./...` 通过、`go test ./...` 全绿、`go build ./cmd/gqy-server` 成功。

实际落点与偏差：

- 落点文件全部建成：`provider/anthropic/{adapter,wire}.go`、`provider/gemini/{adapter,wire}.go`、`httpapi/anthropic.go`、`httpapi/gemini.go`、`server.go` 加两条路由、`main.go` 注册两个适配器；测试 `provider/anthropic/adapter_test.go`（6）与 `provider/gemini/adapter_test.go`（6）。
- 行为验收 1–6 由上述测试覆盖；错误归一复用 `provider.FromStatus` / `NetworkError`，未新增错误码。
- 偏差：Anthropic/Gemini 的「流式已开始后补协议错误帧」的客户端细节未单测，只保证适配器层解析正确；样本文件按 004 计划后置到 005。
- `protocol` 白名单已由 `config` 层包含 `anthropic-messages` / `gemini`，本单无需改动。
