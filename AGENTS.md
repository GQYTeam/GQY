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

All runtime state lives in `$GQY_HOME` (default `~/Library/Application Support/gqy`):
- `config.jsonc` — JSON-with-comments configuration
- `conversation.db` — SQLite WAL-mode turn storage (`channel_id` + `mode` columns for isolation)
- `memory.db` — long-term memory
- `prompts/` — personality files
- `sessions/` — session state

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

`gqy provider add <url> --api-key <key>` auto-discovers models via `GET /models`. The WebUI watches `config.jsonc` mtime for changes and hot-reloads without restart.

### WebUI frontend

Static HTML/CSS/JS in `gqy/web/`, embedded at compile time via `include_str!`. No framework.

## Style and conventions

- Rust edition 2021, no custom rustfmt or clippy config
- Serialize enums as PascalCase strings (serde rename_all = "PascalCase") for agent modes and most config
- Chinese comments and variable names are common in the codebase
- Handle both JSON and SSE content types in responses

## Testing

No special test setup required. `cargo test` runs everything. Tests that need a live model provider will skip if no provider is configured.
