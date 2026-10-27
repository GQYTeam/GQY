# 验证记录

本文记录当前实现实际跑过的检查，以及没有条件验证的部分。命令都在 `app/` 下执行。

## 自动化

| 检查 | 命令 | 结果 |
| --- | --- | --- |
| Go 单测 | `go test ./...` | 全绿；gateway / plugin / eval 各包含新增测试 |
| 竞态 | `go test -race ./internal/plugin ./internal/gateway ./internal/eval/host` | 全绿 |
| vet / JS 语法 | `npm run check` | 全绿 |
| 构建 | `go build ./cmd/gqy-server ./cmd/gqy-eval ./cmd/gqy-import` | 通过 |
| 端到端 | `npm run smoke` | 50 项全绿 |

冒烟覆盖（摘要）：

- 服务端：health、models、usage、静态白名单、405。
- 对话：首屏、SSE 正文/思考/工具、停止、落盘、XSS。
- 会话：重命名、置顶、导出、批量删除、草稿、导入。
- 产物：预览/源码、HTML iframe sandbox + CSP、SVG 无脚本、2 MB 降级、390/600/1100。
- 新页面：首次引导、看板、术语浮窗、人设/记忆/知识库 CRUD。
- 工具/插件：工具清单、外部注入开关、示例插件发现/加载/卸载、安装示例入口。
- 设置：10 tab、搜索、用量、账户、provider/路由组增删、清空对话保留人设/记忆。
- 上下文：人设/记忆/知识文档注入 system，并与工具系统提示合并。
- 附件：文本文件上传落盘、文本注入；图片返回 data URL。
- 网关：provider 账号 / 配额、Anthropic / Gemini 翻译、访问令牌、限流。
- 插件：Unix 远程适配器、provides/requires 依赖、MCP 示例。
- 评测：模型裁判、真实插件宿主。
- 前端：暗色主题、中文/English、长会话分页、多文件产物。
- 运维：`gqy-clean` TTL 清理。
- agent loop：假上游两轮工具调用 + 最终答案 + 累计 usage。

## 手工验证

| 场景 | 结果 |
| --- | --- |
| 本地网关：`GET /api/health`、`/api/gateway/status`、`/v1/models` | 正常；provider 状态、路由组、keySet 正确 |
| `/v1/chat/completions` 非流式 | 正常返回 OpenAI 风格 JSON + usage |
| `/v1/chat/completions` `stream:true` | SSE chunk + usage + `[DONE]`（当前按最终文本分块，不是逐 token 透传） |
| 插件：加载 echo、`/api/plugins/echo/call`、`/api/tools`、卸载 | 正常；工具名 `echo__echo` 出现和消失 |
| 插件：加载 mcp-echo、调用 add | 正常；`tools/list` 发现 add，`2+3` 返回 5 |
| 远程插件：Unix socket JSON-RPC | 单测通过 |
| 协议：Anthropic / Gemini 非流式 + 流式 | 单测通过（假上游） |
| 鉴权：无 token / Bearer / cookie | 单测 + 手工验证：401 / 200 / cookie 200；静态页 200 |
| 清理：`gqy-clean -dry-run` / 实际删除 | 手工验证：旧会话删除、新会话保留 |
| 插件：安装示例接口 | 首次复制 echo + mcp-echo；再次返回 409 |
| 评测：假上游 + 真实 echo 插件 | `gqy-eval` 1/1 通过，报告 / runs / trace 落盘正确 |
| 评测 dry-run | 6 配置 × 3 用例矩阵正确打印 |

## 未在本机验证

以下需要真实凭据或外部服务，代码路径已存在但没有实跑：

- 真实模型上游（DeepSeek / OpenAI / 中转）的完整对话与工具调用。
- GitHub OAuth 全流程（需要 `GITHUB_CLIENT_ID/SECRET`）。
- 真实 MCP 服务端（测试用的是本地 Node MCP 夹具）。
- 真实远程插件服务、真实 Anthropic / Gemini 客户端。
- 订阅账号 OAuth（当前是通用 API key 账号，不是各厂商订阅登录）。
- 模型裁判接真实模型、跨轮比较、`best/current` 自动写回。

## 安全边界

- 管理接口（provider / routing / plugins / install-example）在 `GQY_AUTH_TOKEN` 为空时无鉴权，默认只监听 `127.0.0.1`；公网部署必须设置 token 或加反向代理鉴权。
- 插件以服务端用户权限运行，安装插件 = 信任它。
- `shell` 空白名单时仍可执行任意命令；需要强约束时设置 `GQY_SHELL_ALLOWLIST`。
- 文件工具受 workspace + `$GQY_HOME/sessions` 沙箱限制；硬链接和理论 TOCTOU 仍是已知边界。
- 密钥只写不读；`providers.json` / GitHub token 0600。
