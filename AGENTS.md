# AGENTS.md — GQY

## Project overview

GQY is a Rust AI assistant with a character persona, focused on the macOS desktop app. Single Rust crate at `gqy/` plus the Swift desktop shell in `macos/GQYApp`.

## Key commands

All Rust commands run from the `gqy/` directory:

```bash
cargo build              # debug build
cargo build --release    # production build
cargo test               # run all Rust tests
cargo clippy             # lint
cargo fmt                # format (default rustfmt, no rustfmt.toml)
cargo run -- web         # start WebUI on port 4096
```

macOS desktop shell:

```bash
cd macos/GQYApp && ./build-app.sh   # release build
cd macos/GQYApp && swift run        # dev run
```

**No CI, no pre-commit hooks, no Makefile, no justfile** in this repo.

## Build-time codegen

`gqy/build.rs` generates two artifacts into `OUT_DIR` at compile time:
1. XOR-obfuscated system prompt (`gqy.md`) — base64-encoded, decoded at runtime
2. Compact binary vocabulary from `assets/o200k_base.tiktoken`

If you change `src/prompts/gqy.md`, `src/prompts/plan.md`, `src/prompts/chat.md`, or `assets/o200k_base.tiktoken`, the build script re-runs automatically via `cargo:rerun-if-changed`.

The `o200k_base.tiktoken` file is ~200k tokens and takes a few seconds to process; this is normal.

## Architecture

### State directory

`GQY_HOME` 是 gqy 的独立数据根（默认 `~/Library/Application Support/gqy`）。设置后走**隔离布局**，全部状态收在 `$GQY_HOME` 下；未设置时兼容系统目录布局（macOS 下散在 `~/Library/Application Support/gqy` 根目录，config.jsonc 直接在根）。

隔离布局（App 与推荐用法）：

```text
$GQY_HOME/
├── config/config.jsonc     — JSON-with-comments 配置（注意：不是 $GQY_HOME/config.jsonc）
├── config/prompts/         — 人格文件
├── state/conversation.db   — SQLite WAL 回合存储（channel_id + mode 列隔离）
├── data/personas/<persona>/memory/memory.db — 长期记忆（按人格分目录）
├── sessions/               — 会话状态
├── cache/logs/             — 日志
└── backup/                 — Git 快照仓库
```

`config.jsonc` 有两份并存时，以 `$GQY_HOME/config/config.jsonc`（隔离布局）为准，根目录那份是旧布局遗留，仅 CLI（未设 GQY_HOME）会读。

### Source layout (`gqy/src/`)

- `main.rs` — CLI entrypoint (clap derive)
- `web.rs` — WebUI server, SSE event stream, password auth
- `agent/` — agent loop, tool dispatch, turn management
- `llm/` — LLM client, provider routing, streaming
- `memory/` — vector search, git-backed snapshots
- `tools/` — tool implementations (system, knowledge, entertainment)
- `render/` — terminal rendering (crossterm + rustyline)
- `prompts/` — system prompt templates (obfuscated at build time)

### Channel isolation

Channels: `terminal`, `webui`. Each channel runs its own daemon process with independent context. The conversation DB stores `channel_id` to keep histories separate.

### Agent modes

Three modes with history isolation (v0.8.2+): `Normal` (full tools), `Plan` (read-only), `Chat` (12-turn window, girlfriend persona). Filtered by the `mode` column in the DB.

### Provider hot-swap

`gqy provider add <url> --api-key <key>` auto-discovers models via `GET /models`. The WebUI watches `$GQY_HOME/config/config.jsonc` mtime for changes and hot-reloads without restart.

### WebUI frontend

Static HTML/CSS/JS in `gqy/web/`, embedded at compile time via `include_str!`. No framework.

## Style and conventions

- Rust edition 2021, no custom rustfmt or clippy config
- Serialize enums as PascalCase strings (serde rename_all = "PascalCase") for agent modes and most config
- Chinese comments and variable names are common in the codebase
- Handle both JSON and SSE content types in responses

## Testing

No special test setup required. `cargo test` runs everything. Tests that need a live model provider will skip if no provider is configured.
