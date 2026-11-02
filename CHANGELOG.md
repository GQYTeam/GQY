# Changelog

格式参考 [Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.0/)，版本号遵循 [Semantic Versioning](https://semver.org/lang/zh-CN/)。

## [Unreleased]

### Added

- Go 后端骨架：`app/` module、数据根布局、原子写。
- 配置快照：`providers.json` / `routing.json` / `settings.json` 校验、原子重载、失败保留旧快照、SIGHUP 与 `POST /api/config/reload`。
- 模型目录与解析：`/v1/models`、`group/<id>` / `provider/model` / 裸模型名。
- 鉴权与限流：Bearer / cookie、常量时间比较、按客户端 IP 的分钟固定窗口。
- 网关运行时：候选展开到账号、order / rotate / usage、冷却、失败转移、并发队列、日 token 配额、会话亲和、`requests.jsonl`。
- OpenAI Chat Completions 适配器：统一请求/事件模型、非流式与 SSE 流式、错误归一。
- Anthropic Messages 适配器与 `POST /v1/messages`：thinking、tool_use / tool_result、流式事件族。
- Gemini generateContent 适配器与 `POST /v1beta/models/{model}:generateContent`（含 `:streamGenerateContent`）。
- `POST /v1/chat/completions`：非流式与流式，响应头 `X-GQY-Provider`。
- 会话与产物：会话 CRUD、`messages.jsonl` 事件流、`/api/chat` 五事件 SSE、草稿、附件上传（O_EXCL）、产物索引、`gqy-clean`。
- 运行时状态：`GET /api/gateway/status` 暴露账号健康、调用、并发、冷却与配额。
- 工程化基线：CI、CodeQL、GoReleaser、golangci-lint、Makefile、Dependabot、Issue/PR 模板、`CONTRIBUTING`、`SECURITY`。
- `make smoke-live`：对任意 OpenAI 兼容上游（默认本机 Magpie）做三协议端到端冒烟。
- Web UI：零构建对话页、会话搜索与草稿、工具/思考/问题卡片、附件、控制台（用量/模型/网关/插件/外观/关于）、模型选择器、上下文抽屉、六套主题与 `demos.html` 对照页。
- `make demo`：一键以本机 Magpie 配置启动 UI。
- 静态托管：`internal/httpapi/static.go`，支持 `GQY_WEB_DIR`。
- 会话体验修复：`X-GQY-Session` 响应头、会话标题、问题文本、只落本轮用户事件、空草稿、工具轨迹重建。
- 控制台新增「供应商」与「工具」面板；新增 `GET /api/tools` 只读工具契约。
- 工程化：`make js-check`、`make ui-smoke`（headless Chrome），CI 增加 Node 语法检查与 UI 冒烟任务。
- 桌面化：⌘K 命令面板、快捷键、会话日期分组、侧边栏收起、回到底部、自定义确认对话框、PWA manifest/图标。
- 调研：`docs/reviews/2026-11-02-桌面AgentUI调研.md` 记录桌面 Agent UI 模式与生产化清单。
- 第三方声明：视觉方向参考 `yxxbc/gqy-agent`，许可证与 Notice 见 `docs/third-party/`。
- 许可与署名：`LICENSE`（PolyForm Noncommercial License 1.0.0 + Required Notice）与 `NOTICE`，`README.md` 增加「许可与署名」小节。

- 记忆子系统：`app/internal/memory`（JSONL 追加存储 + 内存索引 + 词法/重要度/衰减召回打分）、`/api/memory*` 九个端点、`memory_recall` / `memory_remember` 两个核心工具；迁移来的 347 facts / 2054 episodes 可直接召回。
- 迁移器一次性插件：`GQYTeam/gqy-plugins-Migrator-default`（`mode: oneshot`，默认 dry-run、幂等、脱敏、可回滚），把旧版 GQY v0.7.0 的 `GQY_HOME` 导出迁到新数据根；旧版结构写成 `docs/reference/legacy-home.md`。

### Security

- 读接口只返回 `api_key_set`，不返回密钥明文。
- 请求日志只记元数据，不记 prompt、回复正文与密钥。
- 未设置 `GQY_AUTH_TOKEN` 时只允许本机来源。
- 统一安全响应头：CSP、`nosniff`、`X-Frame-Options`、`Referrer-Policy`、`Permissions-Policy`。
