# 顾清影克隆音色 TTS 安装指南

> 克隆音色 TTS（Qwen3-TTS + mlx-audio）是**可选增强**：不安装时聊天照常，
> 只是语音回复 / gqy tts --clone 会提示未安装。系统自带 say（普通语音）不受影响。

## 一键安装

```
# 源码/开发模式（venv 建在仓库 gqy/venv）
gqy/src/scripts/tts-setup.sh

# App / CLI 模式（装到 GQY_HOME，随备份迁移；换机 backup restore 后仍在）
gqy/src/scripts/tts-setup.sh "$HOME/Library/Application Support/gqy"
```

脚本做三件事：把 tts-server.py 放到目标 scripts/ 目录、建 venv、pip install mlx-audio。
（模型约 1GB 不预下载，首次合成时自动从 hf-mirror 拉取。）

## 音色克隆三要素（缺一不可）

1. **参考音频**：5-15 秒干净人声 wav（无背景音乐/混响）
   - 放到 <目标>/assets/voice/ref.wav（默认位置）
   - 或设置环境变量 GQY_TTS_REF=<任意 wav>
2. **参考文本**：录音里的原话（逐字一致），设置 GQY_TTS_REF_TEXT
3. **模型**：首次合成自动下载

## 服务如何被拉起

gqy 在运行时按以下优先级解析 tts-server.py 与 venv（不再用编译期路径）：

1. GQY_TTS_SCRIPT / GQY_TTS_PYTHON 环境变量（完全自定义）
2. GQY_HOME/scripts/tts-server.py + GQY_HOME/venv/bin/python（App/CLI 自托管，随备份迁移）
3. 可执行文件旁 share/gqy/scripts/tts-server.py（App bundle / brew 内嵌脚本）
4. 源码树 src/scripts/tts-server.py（开发模式）

调用（/api/tts、gqy tts --clone、speak 工具）时若服务未在跑，自动拉起并轮询
/health 直到就绪（默认 120s，覆盖首次下载模型；GQY_TTS_START_TIMEOUT 秒数可覆盖）。
服务空闲 10 分钟自动退出释放内存。

## 试听

```
gqy tts --clone "你好，我是顾清影"
```
