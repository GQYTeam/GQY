# 能力矩阵

按“用户能做什么”分类。状态：`可用` / `部分` / `占位` / `未开始`。实现变更后同步此表。

## 对话

| 能力 | 入口 | 状态 | 说明 |
| --- | --- | --- | --- |
| 流式回复 | 输入框 | 可用 | SSE：thinking / tool / text / question / done |
| 思考过程 | 消息块 | 可用 | 可设置回复后保持展开 |
| 工具轨迹 | 消息块 | 可用 | 参数、输出、耗时、状态 |
| ask_user 提问 | 消息卡片 | 可用 | 选项 / 数字键 / 自定义答案 |
| 产物灯箱 | 消息 → 查看 | 可用 | 文件面板、代码高亮、图片灯箱 |
| 附件上传 | 输入框 / 拖拽 | 可用 | 文本注入、图片多模态、其他二进制落盘 |
| 草稿 | 切换会话 | 可用 | 按会话保存 |
| 重试 / 停止 | 消息 / 输入框 | 可用 | AbortController |
| 人设 | 设置 → 人设 | 可用 | system prompt 注入 |
| 记忆 | 设置 → 记忆 | 可用 | 手动条目注入 |
| 知识库 | 设置 → 知识库 | 可用 | 关键词匹配注入，无向量检索 |
| 关键词浮窗 | 正文术语 | 可用 | 本地术语表 |

## 网关

| 能力 | 入口 | 状态 | 说明 |
| --- | --- | --- | --- |
| 多 provider | providers.json | 可用 | 环境变量 provider 兜底 |
| 模型解析 | `provider/model`、`group/id` | 可用 | 裸模型名按 provider 匹配 |
| Provider 管理 | 设置 → 模型 & 供应商 | 可用 | 增删改、密钥只写不读 |
| 路由组管理 | 设置 → 模型 & 供应商 | 可用 | order / rotate / usage |
| 路由组 | routing.json | 可用 | order / rotate / usage |
| 失败转移 | 自动 | 可用 | 响应开始前，最多 3 次 |
| 健康 / 冷却 | 自动 | 可用 | 429 / 5xx / 连接错误 |
| 并发限制 | 自动 | 可用 | 每 provider 信号量 + 有界等待 |
| 会话亲和 | 自动 | 可用 | 内存态 |
| 请求日志 | gateway/requests.jsonl | 可用 | 不落正文 |
| OpenAI 兼容端点 | `/v1/chat/completions` | 可用 | 非流式 + 兼容流式 |
| 模型列表 | `/v1/models` | 可用 | provider / group |
| 状态接口 | `/api/gateway/status` | 可用 | 健康、调用数、冷却 |
| Anthropic / Gemini 协议 | `/v1/messages`、`/v1beta/models/*` | 可用 | 非流式 + 兼容流式 |
| 多账号 / 配额 | provider `accounts` | 可用 | 账号级失败转移、日 token 上限（内存） |
| 访问令牌 | `GQY_AUTH_TOKEN` | 可用 | Bearer / cookie；覆盖 /api、/v1、/v1beta |

## 工具与插件

| 能力 | 入口 | 状态 | 说明 |
| --- | --- | --- | --- |
| 核心工具 | 自动注入 | 可用 | read / edit / shell / list_dir / websearch / ask_user |
| 外部工具 | `GQY_ENABLE_EXTERNAL` / 设置 | 可用 | provider_usage + 插件动作 |
| 运行时开关 | 设置 → 工具 | 可用 | `/api/tools/external` |
| 插件发现 | `$GQY_HOME/plugins/*/manifest.json` | 可用 | 原生 / MCP / unix / tcp |
| 插件加载 / 卸载 | 扩展 / 软件包 | 可用 | 状态机、幂等 |
| 插件动作调用 | HTTP / 工具 | 可用 | 原生 JSON-RPC / MCP |
| 进程组回收 | 自动 | 可用 | stdin EOF / SIGTERM / SIGKILL |
| 插件服务依赖 | manifest `provides/requires` | 可用 | 缺依赖拒绝加载、有依赖拒绝卸载 |
| 远程插件 | unix / tcp | 可用 | 换行 JSON-RPC，不拉起进程 |

## 评测

| 能力 | 入口 | 状态 | 说明 |
| --- | --- | --- | --- |
| 三维矩阵 | `cmd/gqy-eval` | 可用 | prompt / schema / return |
| 单次对话循环 | CLI | 可用 | 工具执行 + 回填 |
| 规则判定 | CLI | 可用 | 调用/参数/最终答案 |
| 录制 / 报告 | `docs/test_call_*` | 可用 | runs.jsonl / trace / report |
| 重放 | CLI `-replay` | 可用 | 不发真实请求 |
| 故障注入 | 用例配置 | 可用 | empty / error / timeout |
| 模型裁判 | `judge_model` | 可用 | 规则判定仍为主判定 |

## 页面

| 能力 | 入口 | 状态 | 说明 |
| --- | --- | --- | --- |
| 对话 | 侧栏 | 可用 | |
| 设置 | 账户按钮 | 可用 | 10 tab + 搜索 |
| 引导 | 首次打开 | 可用 | |
| 看板 | 侧栏 | 可用 | 本机任务卡 |
| 用量 | 顶栏 ⚡ / 设置 | 可用 | 本地 + 服务端聚合 |
| 模型 & 供应商 | 设置 | 可用 | provider / 路由组 / 默认模型 |
| 扩展 | 侧栏 | 可用 | 插件 / GitHub |
| 人设 | 设置 | 可用 | |
| 记忆 | 设置 | 可用 | |
| 知识库 | 设置 | 可用 | 术语 + 文档 |
| 软件包 | 设置 | 可用 | 插件清单 |
| 工具 | 设置 | 可用 | 核心 / 外部 / 插件 |
| 账户 | 设置 | 可用 | 本机单用户 + GitHub 状态 |
| 语音 | 设置 | 部分 | 开关 + 浏览器 SpeechRecognition；唤醒词未接 |
| 暗色主题 | 设置 | 可用 | `data-theme=dark` + 令牌覆盖 |
| 语言 | 设置 | 部分 | 中文 / English 导航、看板、引导、设置标签 |
| 长会话 | 对话 | 可用 | 最近 80 条分页加载 |
| 图片视觉 | 附件 | 可用 | OpenAI 多模态 content；取决于上游模型 |
| 多文件产物 | `write_artifact` / 文件面板 | 可用 | `artifact.files` 按真实路径建树 |
| 作品预览 | 代码灯箱 | 可用 | Markdown / HTML / SVG |

## 运维与安全

| 能力 | 状态 | 说明 |
| --- | --- | --- |
| 静态白名单 | 可用 | 源码/脚本/点文件不可访问 |
| 路径穿越防护 | 可用 | 会话 id 与静态路径 |
| 会话目录 0700 | 可用 | 文件 0600 |
| 工具日志脱敏 | 可用 | key/token/secret/authorization/password |
| 服务端鉴权 | 可用 | `GQY_AUTH_TOKEN`，默认空=仅本机 |
| 限流 | 可用 | `GQY_RATE_LIMIT` 每分钟/客户端 |
| 清理命令 | 可用 | `cmd/gqy-clean -ttl 720h` |
| 日志轮转 | 可用 | 插件 stderr 超过 1 MB 轮转 .1 |

## 非目标（当前不做）

- 厂商订阅 OAuth（当前是通用 API key 账号 + 日 token 配额）。
- 请求/响应隐私 masking（日志与工具参数已脱敏）。
- 插件市场、插件权限审批、崩溃自动重启。
- 多设备会话回灌。
