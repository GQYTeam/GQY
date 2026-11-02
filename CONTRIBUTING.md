# 贡献指南

GQY 是本地优先的 AI 助手网关。动手前先读文档，代码改动要和设计决定对得上。

## 开始之前

按顺序读：

1. `docs/designs/00-设计理念.md`
2. `docs/designs/01-架构.md`
3. `docs/designs/02-网关与路由.md`
4. `docs/designs/03-协议翻译.md`
5. `docs/designs/12-配置与热更新.md`
6. `docs/construction/` 里最新的施工单

设计文档与代码不一致时，以代码为准，并在同一个 PR 里修正文档。

## 环境

- Go 1.22+
- 后端只用标准库；新增第三方依赖必须先在设计层达成一致
- 前端保持零构建（当前阶段尚未开始）

## 常用命令

```bash
make check      # gofmt + vet + test + build
make test
make race
make lint       # 需要本机安装 golangci-lint
make build      # 产物在 app/bin/
make run
make smoke-live # 对上游做端到端冒烟（默认本机 Magpie）
```

`make smoke-live` 支持 `UPSTREAM_BASE_URL`、`UPSTREAM_MODEL`、`UPSTREAM_API_KEY` 覆盖。不要在仓库里写真实密钥。

## 改动流程

1. 设计有变化：先改 `docs/designs/`，保留决定编号。
2. 写施工单到 `docs/construction/`，写明范围、落点、验收。
3. 写代码与测试；测试不依赖真实网络与真实密钥。
4. 更新蓝图（`docs/blueprint/`）与 `CHANGELOG.md`。
5. 跑 `make check` 与 `make race`。

## 代码边界

- 依赖只允许自上而下：`httpapi → gateway → provider`，下层不得 import 上层。
- 厂商与协议差异只能出现在 `internal/provider/<protocol>`；核心层不出现供应商专有字段。
- 新增 provider 只改 `providers.json`；新增协议 = 新增适配器 + 注册 + 契约测试。
- 密钥只写不读；日志不落正文、密钥、完整环境变量。
- 所有对外端点必须经过 `/api`、`/v1`、`/v1beta` 的统一鉴权与限流。

## 提交与 PR

- 提交信息用 Conventional Commits：`feat(gateway): ...`、`fix(config): ...`、`docs: ...`、`test: ...`、`ci: ...`。
- 一个 PR 只做一件事；行为变化必须带测试。
- PR 描述写清改了什么、为什么、怎么验证。
- 不要提交 `.env`、密钥、会话数据或本地构建产物。

## 测试约定

- 表驱动优先；HTTP 上游用 `httptest` 假服务，不访问外网。
- 改动配置、路由、协议翻译时，补一个失败路径用例。
- 覆盖流式与非流式等价、错误分类、重试边界。
