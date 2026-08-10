# 顾清影 · 桌面壳（Swift + WKWebView）

独立窗口版:直接内嵌 WebUI(`http://127.0.0.1:4096`),**界面与 WebUI 完全一致**——因为就是同一个页面。
壳只负责:探活、一键拉起 `gqy web --no-open`、独立窗口。

## 为什么不是原生 UI

之前做过一版 SwiftUI 原生聊天界面(走 HTTP + SSE 协议),但「和 WebUI 一模一样」意味着把
WebUI 的 HTML/CSS/JS 全部翻译成 SwiftUI——工作量大且永远无法同步。WKWebView 内嵌是
唯一能保证像素级一致的做法,零 UI 代码维护。后端协议层(SSE 解析等)随原生 UI 一并删除。

## 构建运行

```zsh
./build-app.sh   # release 构建并组装 build/GQYApp.app，自动打开
swift run        # 开发运行
```

环境变量: `GQY_BIN`（后端二进制路径）、`GQY_HOME`、`GQY_WEB_PORT`（默认 4096）。
WebUI 若设置了密码,直接在页面内登录即可（WKWebView 自带 cookie）。

## 一体化（自包含）

`build-app.sh` 会把 gqy 二进制内嵌进 `Contents/Resources/gqy`，并生成 `GQYIcon.icns`。
整个 `GQYApp.app` 单独拷走即可用：后端未运行时点「唤醒她」用的是**内嵌二进制**，
不依赖系统已装的 gqy（找不到内嵌时才回退到 `GQY_BIN` → homebrew）。
用户数据仍在 `GQY_HOME`（默认 `~/Library/Application Support/gqy`），App 卸载不影响。

## 结构

```
Sources/GQYApp/APIClient.swift   # 仅探活（/api/health）
Sources/GQYApp/ShellViewModel.swift  # 连接状态 + 一键启动后端
Sources/GQYApp/WebShellView.swift    # WKWebView 容器
Sources/GQYApp/AppEntry.swift        # App 入口
```
