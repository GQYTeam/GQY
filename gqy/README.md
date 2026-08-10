# GQY 核心(顾清影)

本目录为 GQY 的**核心源码**(Rust 工程),仓库根目录存放项目其他部分(docs/kb/communication 等)。

## 构建

```bash
cd gqy
cargo build --release
```

- 当前版本:v0.8.6(与正在运行的顾清影.app 一致)
- ⚠️ 构建必需文件(已 gitignore,勿删):`assets/o200k_base.tiktoken`、`pics/GQY-avatar.png`、`pics/GQY-image.png`
- 提示词:`src/prompts/`
