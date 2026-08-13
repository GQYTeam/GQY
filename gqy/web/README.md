# GQY WebUI assets

这些静态资源在编译期嵌入 GQY Rust 二进制。本地 WebUI 用：

```sh
cargo run -- web
```

服务监听所有 IPv4 接口并打印每个访问地址。密码认证可选：

```sh
cargo run -- web -p secret
cargo run -- web -p
cargo run -- web --password-file /path/to/password.txt
```

配置密码后，WebUI 会要求输入并建立同源会话。

## 前端源码与构建（稳定性防线）

前端源码在 `src/`（ES modules），构建产物 `app.js` 提交进 git，
由 Rust 的 `include_str!` 嵌入——**cargo build 不依赖 node**。

- 改源码后重新生成产物：`npm run build`（默认带 inline sourcemap，方便调试）
- 发布时用小体积产物：`npm run build:min`
- 静态检查：`npm run lint`（0 error 是底线；warnings 是记录在案的技术债）
- 安装依赖：`npm install`（首次）

约定：**改了 `src/` 必须 `npm run build` 并把 `app.js` 一起提交**，
否则 Rust 侧嵌入的是旧产物。

### 结构

```
web/
├── src/main.js       # 入口（IIFE，esbuild --format=iife 打包）
├── src/utils.js      # 纯工具函数（无 DOM 依赖）
├── app.js            # 构建产物（提交进 git，include_str 引用）
├── index.html        # 页面骨架（直接嵌入）
├── styles.css        # 样式（直接嵌入，CSS 自定义属性主题）
├── package.json      # esbuild + eslint（devDependencies）
└── eslint.config.js  # ESLint 9 flat config
```

### 稳定性护栏

- **全局错误边界**（src/main.js 顶部）：未捕获异常/未处理的 Promise 拒绝
  显示错误遮罩（带复制/重载），替代白屏；
- **Rust 侧 smoke 测试**（web.rs tests::webui_assets_smoke）：关键 DOM id
  与静态资源加载检查，防止拆分/重构漏引用；
- **eslint no-undef 为 error**：引用未定义变量 = 白屏级 bug，必须修。
