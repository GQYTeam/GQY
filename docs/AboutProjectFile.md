1. 术语与边界

任务产物 Artifact：任务完成后需要交付或保留的文件，分为最终产物和中间产物。
运行态文件 Runtime Files：临时文件、工具日志、上下文快照、WebUI 脚本等，不等同于交付产物。
工作区 Workspace：用户选定的项目根目录，记为 $WORKSPACE。
会话 Session：一次对话/任务运行，记为 $SESSION_ID。
全局数据根目录：$GQY_HOME，默认 ~/.gqy，所有会话级数据放在其下。
不再使用 */{project}/.tmp 这种模糊写法，统一用环境变量和占位符表达。
2. 目录规范

2.1 工作区临时代码目录

用于存放 agent 在项目内执行时产生的临时代码、补丁草稿、可重建文件。

推荐路径：

text
$WORKSPACE/.gqy/tmp/
  code/
  patches/
如果必须沿用 .tmp，则统一为：

text
$WORKSPACE/.tmp/gqy/
要求：

该目录必须自动加入 .gitignore，不能污染项目版本库。
任务成功结束后默认清理；任务失败或用户要求保留时可保留。
若工作区不可写，回退到会话临时目录：$SESSION_DIR/tmp/workspace/。
2.2 无工作区临时产物目录

当用户未选择工作区，或产物不应写入项目时使用：

text
$GQY_HOME/sessions/$SESSION_ID/tmp/
  uploads/
  generated/
  scratch/
用途：

uploads/：用户上传或引用的临时文件。
generated/：模型生成但尚未确认交付的临时产物。
scratch/：可随时删除的中间文件。
2.3 工具调用日志目录

text
$GQY_HOME/sessions/$SESSION_ID/logs/
  tools/
    2026-10-22.jsonl
要求：

推荐使用 JSONL，每次工具调用记录一条。
字段至少包括：timestamp、tool_name、status、duration_ms、input_summary、output_summary、artifact_paths。
敏感参数需脱敏，不记录完整密钥、token、环境变量。
支持按日期或大小轮转。
2.4 IO / 上下文与可视化目录

原“IO 存放上下文明文”建议拆成明确的 input/output/context，可视化脚本单独放：

text
$GQY_HOME/sessions/$SESSION_ID/io/
  input/
  output/
  context/
    context.md
    messages.jsonl
  webui/
    open.sh
    index.html        # 由 open.sh 生成或刷新
要求：

input/：用户输入、附件引用。
output/：最终输出清单、产物索引。
context/：上下文明文快照，便于调试和可视化。
webui/open.sh：在终端运行后自动创建标准html并用浏览器打开（已经存在html则替换数据），读取当前 session 的上下文、日志和产物索引进行可视化查看。
WebUI 默认只绑定 127.0.0.1，使用随机 token；默认只读，不提供任意文件写入能力。
3. 完整目录示例

text
$GQY_HOME/sessions/$SESSION_ID/
  manifest.json
  tmp/
    uploads/
    generated/
    scratch/
  logs/
    tools/
      2026-10-22.jsonl
  io/
    input/
    output/
    context/
      context.md
      messages.jsonl
    webui/
      open.sh
      index.html        # 由 open.sh 生成或刷新
  scripts/
工作区：

text
$WORKSPACE/
  .gqy/
    tmp/
      code/
      patches/
manifest.json 建议记录：

json
{
  "session_id": "...",
  "workspace": "/path/to/project",
  "created_at": "...",
  "artifacts": [],
  "cleanup_policy": {
    "session_tmp_ttl_days": 7,
    "logs_ttl_days": 30
  }
}
4. 生命周期与清理策略

会话临时目录 $SESSION_DIR/tmp/：默认保留 7 天，可配置。
工具日志与上下文快照：默认保留 30 天，可配置。
工作区临时代码：任务成功后默认清理；失败时保留以便排查。
提供统一清理命令，例如 gqy clean --session ... 和 gqy clean --expired。
5. 安全与实现约束

目录权限：会话目录 0700，敏感文件 0600。
所有路径通过 $GQY_HOME、$SESSION_ID、$WORKSPACE 解析，禁止硬编码 ~。
对路径做规范化校验，防止 ../ 路径穿越。
日志和上下文明文需脱敏，避免记录密钥、token、完整环境变量。
