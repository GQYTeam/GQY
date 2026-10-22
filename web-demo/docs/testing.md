# 测试与验收

## 必跑

```bash
npm run check
```

启动本地服务后（`npm run dev`）手动走一遍：

1. 首屏：种子会话自动流式播放，思考 → 工具 → 正文 → 产物/提问出现。
2. 发送消息：Enter 发送、Shift+Enter 换行；停止按钮可中断；停止后内容保留。
3. 提问卡片：点选项 / 键盘 1–3 / 「其他…」自定义；已回答态禁用。
4. 产物：点击打开右侧文件面板；对话变窄；树可展开、键盘导航、点文件看到代码；拖宽、关闭恢复。
5. 代码灯箱：面板「全屏」打开；Esc 关闭；复制可用。
6. 设置：外观（字号/动效/侧栏）、对话（流式速度/思考展开）、数据（导出/恢复/清空）、用量页。
7. 看板：整页高度、返回对话；天气/建议/任务/进度数据齐全。
8. 响应式：390 / 600 / 1100 无横向滚动；面板在窄屏覆盖。
9. 减少动效：无打字光标、无循环动画。

## Headless 冒烟（可选）

用本机 Chrome 跑无头检查，模式：

```bash
"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome" \
  --headless=new --disable-gpu --no-sandbox \
  --virtual-time-budget=12000 --dump-dom http://localhost:5173/ > /tmp/dom.html
```

临时探针的做法（不要提交）：

1. 复制 `index.html` 为 `_probe.html`，在 `</body>` 前插一段脚本；
2. 脚本里用 `setTimeout` / `setInterval` 等目标节点出现后操作；
3. `--dump-dom` 抓取结果并在终端解析；
4. 完成后删除 `_probe.html`。

常见检查点：`.msg` 数量、`.artifact`、`.linkcard`、`[class^=tok-]`、`#filesPanel.is-open`、`#settingsLayer.hidden`、`#boardView.hidden`、`#usagePct`。
