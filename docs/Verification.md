# 验证记录

本文记录当前实现实际跑过的检查，以及没有条件验证的部分。命令都在 `app/` 下执行。

## 自动化

| 检查 | 命令 | 结果 |
| --- | --- | --- |
| Go 单测 | `go test ./...` | 全绿；gateway / plugin / eval 各包含新增测试 |
| 竞态 | `go test -race ./internal/plugin ./internal/gateway ./internal/eval/host` | 全绿 |
| vet / JS 语法 | `npm run check` | 全绿 |
| 构建 | `go build ./cmd/gqy-server ./cmd/gqy-eval ./cmd/gqy-import` | 通过 |
| 端到端 | `npm run smoke` | 41 项全绿 |

冒烟覆盖（摘要）：

- 服务端：health、models、usage、静态白名单、405。
- 对话：首屏、SSE 正文/思考/工具、停止、落盘、XSS。
- 会话：重命名、置顶、导出、批量删除、草稿、导入。
- 产物：预览/源码、HTML iframe sandbox + CSP、SVG 无脚本、2 MB 降级、390/600/1100。
- 新页面：首次引导、看板、术语浮窗、人设/记忆/知识库 CRUD。
- 工具/插件：工具清单、外部注入开关、示例插件发现/加载/卸载、安装示例入口。
- 设置：10 tab、搜索、用量、账户、provider/路由组增删。
- agent loop：假上游两轮工具调用 + 最终答案 + 累计 usage。

## 手工验证

| 场景 | 结果 |
| --- | --- |
| 本地网关：`GET /api/health`、`/api/gateway/status`、`/v1/models` | 正常；provider 状态、路由组、keySet 正确 |
| `/v1/chat/completions` 非流式 | 正常返回 OpenAI 风格 JSON + usage |
| `/v1/chat/completions` `stream:true` | SSE chunk + usage + `[DONE]`（当前按最终文本分块，不是逐 token 透传） |
| 插件：加载 echo、`/api/plugins/echo/call`、`/api/tools`、卸载 | 正常；工具名 `echo__echo` 出现和消失 |
| 插件：安装示例接口 | 首次复制成功；再次返回 409 |
| 评测：假上游 + 真实 echo 插件 | `gqy-eval` 1/1 通过，报告 / runs / trace 落盘正确 |
| 评测 dry-run | 6 配置 × 3 用例矩阵正确打印 |

## 未在本机验证

以下需要真实凭据或外部服务，代码路径已存在但没有实跑：

- 真实模型上游（DeepSeek / OpenAI / 中转）的完整对话与工具调用。
- GitHub OAuth 全流程（需要 `GITHUB_CLIENT_ID/SECRET`）。
- 真实 MCP 服务端（测试用的是本地 Node MCP 夹具）。
- 远程插件、插件服务依赖、Anthropic / Gemini 协议翻译、订阅账号与配额窗口。
- 评测模型裁判、跨轮比较、`best/current` 自动写回。

## 安全边界

- 管理接口（provider / routing / plugins）无鉴权，默认只监听 `127.0.0.1`；公网部署必须加反向代理鉴权。
- 插件以服务端用户权限运行，安装插件 = 信任它。
- `shell` 空白名单时仍可执行任意命令；需要强约束时设置 `GQY_SHELL_ALLOWLIST`。
- 文件工具受 workspace + `$GQY_HOME/sessions` 沙箱限制；硬链接和理论 TOCTOU 仍是已知边界。
- 密钥只写不读；`providers.json` / GitHub token 0600。
