# GQY

[![CI](https://github.com/GQYTeam/GQY/actions/workflows/ci.yml/badge.svg)](https://github.com/GQYTeam/GQY/actions/workflows/ci.yml)
[![CodeQL](https://github.com/GQYTeam/GQY/actions/workflows/codeql.yml/badge.svg)](https://github.com/GQYTeam/GQY/actions/workflows/codeql.yml)
![Go](https://img.shields.io/badge/Go-1.22%2B-00ADD8?logo=go)

> **千队并行，各自封疆。**
> 密钥不出门，能力随便长。

GQY 是一个跑在你自己电脑上的 AI 助手网关：把 OpenAI 兼容、Anthropic、Gemini 等多家的模型接入，统一成一套协议，既服务内部的 agent loop，也服务外部的 OpenAI 兼容客户端。工具之外的能力用插件运行时扩展：原生 JSON-RPC、MCP，以及远程 unix / tcp 插件。

> 文档与代码正在按 `docs/` 里的设计文档重新整理。

## 当前状态

施工单 `001`–`034` 已落地：配置快照、网关运行时、OpenAI / Anthropic / Gemini 三协议、会话与产物、核心工具与 agent loop、插件运行时、评测与一次性插件、零构建 Web UI、记忆、人格、指标、热加载与 macOS 应用外壳。

正在执行 [`docs/designs/21-与Miyu对齐.md`](docs/designs/21-与Miyu对齐.md)：与 [Miyu](https://github.com/miyu-agent/miyu) 的功能和 UI 逐项对齐，交付可用的 macOS app。阶段 1（外壳行为对齐，施工单 `035`）已完成，随后是对话页、管理页、知识库/语音与打包验收。

## 快速开始

```bash
git clone https://github.com/GQYTeam/GQY.git
cd GQY
cp app/.env.example app/.env    # 按需填写 GQY_API_KEY 等
make run                        # 默认 http://127.0.0.1:5173
```

`providers.json` / `routing.json` / `settings.json` 放在 `$GQY_HOME`（默认 `~/.gqy`）。文件不存在时使用环境变量 provider 兜底。

## Web UI

```bash
make demo          # http://127.0.0.1:5173/ ，默认接本机 Magpie
```

对话页支持 ⌘K 命令面板、会话搜索与日期分组、流式思考与工具轨迹、问题卡片、草稿、图片/文本附件；侧栏可收起，也可打开控制台（用量、人格、模型、供应商、网关、插件、工具、外观、关于）与会话信息抽屉。插件页可从项目插件仓库安装、配置插件设置。可安装为 PWA 独立窗口；视觉方向有六套主题，对照页在 <http://127.0.0.1:5173/demos.html>。UI 是零构建原生 ES modules，改完刷新即可。



```bash
curl http://127.0.0.1:5173/healthz
curl http://127.0.0.1:5173/v1/models
curl http://127.0.0.1:5173/v1/chat/completions \
  -H 'Content-Type: application/json' \
  -d '{"model":"env/deepseek-chat","messages":[{"role":"user","content":"你好"}]}'
```

## 代码布局

- `app/`：Go 后端（`cmd/` + `internal/`），标准库，零第三方运行时依赖。
- `docs/designs/`：设计文档，按编号读，从 `00-设计理念.md` 开始。
- `docs/construction/`：施工方案与施工单。
- `docs/blueprint/`：当前实现形状、代码落点与测试。
- `docs/reviews/`：调研与走查记录。
- 插件仓库独立存放，例如 `gqy-plugins-ShowImages-default`。

## 开发

```bash
make check      # gofmt + vet + test + build
make race
make lint       # 需要 golangci-lint
make cover
```

验证一律走生产代码路径的单元/集成测试与真实上游，不建 mock 冒烟资产。

提交前阅读 [CONTRIBUTING.md](CONTRIBUTING.md)。安全问题请按 [SECURITY.md](SECURITY.md) 私密报告。

## 第三方参考

Web UI 的信息架构与 dawn / dusk 主题方向参考了 [yxxbc/gqy-agent](https://github.com/yxxbc/gqy-agent)。该仓库使用 PolyForm Noncommercial 1.0.0；许可证与 Required Notice 见 [docs/third-party/README.md](docs/third-party/README.md)。其品牌与角色素材未复制。

## 许可与署名

本项目按 **PolyForm Noncommercial License 1.0.0** 授权：全文见 [LICENSE](LICENSE)，署名要求见 [NOTICE](NOTICE)。

- **禁止商用。** 生产、部署、售卖、SaaS、内置于商业产品等任何商业目的的使用，都不在授权范围内。
- **必须署名。** fork、二次开发、摘取代码 / 文档 / 设计，都必须标注来源与原作者，并原样保留 `LICENSE`、`NOTICE` 与顶部的 Required Notice。

衍生项目请在 `README.md` 里原样写清这四项：

```text
来源项目：GQYTeam/GQY
项目地址：https://github.com/GQYTeam/GQY
原作者：GQYTeam
许可证：PolyForm Noncommercial License 1.0.0
```
