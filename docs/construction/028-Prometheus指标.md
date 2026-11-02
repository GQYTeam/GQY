# 028 · Prometheus 指标（待开工）

日期：2026-11-02
状态：完成

## 目标

给本地网关加一个 Prometheus 兼容的 `/metrics` 文本端点，覆盖请求、上游、账号、插件与进程健康，供本机 Prometheus/Grafana 抓取。不引入第三方 Go 依赖。

## 范围

**做：**

- `GET /metrics`：Prometheus text exposition format 0.0.4，`text/plain; version=0.0.4`。
- 指标：
  - `gqy_http_requests_total{method,path,status}`
  - `gqy_http_request_duration_seconds`（histogram，按 path 模板归一）
  - `gqy_gateway_calls_total{provider,model,account,status}`
  - `gqy_gateway_attempts_total{provider,account}`、`gqy_gateway_failures_total`
  - `gqy_gateway_tokens_total{provider,account,direction}`（prompt/completion）
  - `gqy_gateway_cooldown_seconds{provider,account}`（gauge）
  - `gqy_gateway_inflight{provider,account}`（gauge）
  - `gqy_plugin_calls_total{plugin,action,status}`、`gqy_plugin_ready{plugin}`、`gqy_plugin_failures_total`
  - `gqy_build_info{version,commit}`（gauge 1）
  - `gqy_config_version`
- 抓取端点纳入 A1 鉴权（`/api` 之外的新前缀需在 A1/鉴权表补一行），或只允许回环；默认要求 Bearer/cookie。
- 指标只在内存，重启清零；不落正文、不落 prompt。

**不做：**

- OpenTelemetry / tracing；远端 push gateway。
- 持久化指标、跨实例聚合。
- 把运营看板搬到 Prometheus；WebUI 控制台仍是主要视图。

## 落点（建议）

```
app/internal/metrics/metrics.go       # 无依赖的计数/直方图与 text 编码
app/internal/httpapi/metrics.go       # GET /metrics
app/internal/httpapi/middleware.go    # HTTP 请求指标
app/internal/gateway/{router,state}.go# 账号/尝试/token/冷却指标
app/internal/plugin/plugin.go         # 插件调用指标
app/internal/buildinfo/buildinfo.go   # build_info
docs/designs/14-可观测与指标.md        # 设计文档（待写）
```

## 验收

1. `curl /metrics` 返回 Prometheus 可解析文本；无密钥/正文。
2. 两次 chat 请求后 `gqy_gateway_calls_total` 与 token 计数增加。
3. 429/冷却后 `gqy_gateway_cooldown_seconds` 反映剩余时间。
4. `/metrics` 未带令牌时按 A1 拒绝（或仅回环，按最终设计定）。
5. `promtool check metrics`（可选，本机有 promtool 时）。

## 备注

先补 `docs/designs/14-可观测与指标.md` 与蓝图，再写代码；指标名与标签一旦被 Grafana 使用就保持稳定。


## 留底

完成（2026-11-02）。`internal/metrics` 无依赖实现 counter / gauge / histogram 与 text exposition；`GET /metrics` 已接 A1 鉴权、不进入口限流；HTTP、网关调用/尝试/失败/token、账号冷却/并发、插件调用、构建与配置版本均已有指标。`make check`、`go test -race ./...`、`golangci-lint`（0 issues）通过；本机 curl `/metrics` 冒烟正常。
