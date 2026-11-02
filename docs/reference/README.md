# 参考层（reference）

reference 放对外接口与协议字段的查询手册：给调用方与实现方查「某个端点 / 字段是什么、长什么样」。它不做决定、不写边角推理，只给形状；决定在 `designs/`，形状的权威在 `blueprint/`。

## 定位

- **blueprint 是权威**：形状的唯一事实来源是 `blueprint/`（auth / gateway / protocol / config 等页）。
- **reference 是从 blueprint 生成的对外快照**：从 blueprint 提炼成「外部客户端 / 内部 agent / 前端」可直接查阅的手册。
- **不一致时以 blueprint 为准**，并顺手更正 reference；文档与代码不一致时，按 `../README.md` 的分工以代码为准、再回改文档。

## 索引

| 文件 | 主题 | 适用读者 |
| --- | --- | --- |
| `http-api.md` | 对外 HTTP 接口：鉴权、限流、端点总表、`/v1/models`、`/api/gateway/status`、错误归一、流式约定 | 外部 OpenAI 兼容客户端、内部 agent、前端 |
| （后续）协议字段手册 | OpenAI / Anthropic / Gemini 的请求与响应字段逐项说明 | 外部客户端、provider 适配器实现者 |
| （后续）配置字段手册 | providers.json / routing.json / settings.json 字段说明 | 配置维护者、设置页前端 |

## 与 blueprint、designs 的关系

- `designs/02-网关与路由.md`、`03-协议翻译.md`、`08-鉴权与配额.md` 定「怎么做」。
- `blueprint/{auth,gateway,protocol,config}.md` 定「现在长什么样」。
- `reference/` 只做「查询用快照」，不新增字段、不改语义。
