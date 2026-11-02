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
| `protocol-fields.md` | OpenAI / Anthropic / Gemini 的请求与响应字段逐项说明、错误分类 | 外部客户端、provider 适配器实现者 |
| `config-fields.md` | providers.json / routing.json / settings.json 字段说明与生效方式 | 配置维护者、设置页前端 |
| `plugin-manifest.md` | 插件 manifest v1.1：字段、两种模式（installed / local）、设置 schema 与存放位置 | 插件作者、插件市场维护者 |
| `agent-onboarding.md` | 非 GQY 的 agent 把 GQY 当统一上游：选协议、base_url、模型引用、鉴权 | 外部 agent 作者、集成方 |
| `legacy-home.md` | 旧版 GQY v0.7.0 的 `GQY_HOME` 导出结构快照（非 blueprint 派生，迁移专用） | 迁移器实现者、数据迁移维护者 |

`legacy-home.md` 是唯一例外：它记录历史导出，**不是**从 blueprint 生成的对外快照。迁移决策在 `../designs/18-记忆与迁移.md`，落点映射在 `../blueprint/migrate.md`。

## 与 blueprint、designs 的关系

- `designs/02-网关与路由.md`、`03-协议翻译.md`、`08-鉴权与配额.md` 定「怎么做」。
- `blueprint/{auth,gateway,protocol,config}.md` 定「现在长什么样」。
- `reference/` 只做「查询用快照」，不新增字段、不改语义。
