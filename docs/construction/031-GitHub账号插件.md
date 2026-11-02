# 031 · GitHub 账号插件

日期：2026-11-02
状态：完成（2026-11-02）

## 目标

把 GQY 安装目录 extras 下 `github/` 那套参考账号布局（`account.json` + `gh/` + `gitconfig`）做成 **顾清影 专属的常驻插件** `github-account-default`，验证两条新做法：

1. **远程 GitHub 授权、本地登录**：设备码（OAuth Device Flow）远端授权，本机零交互拿到令牌并落盘；同时给出 `gh auth login --web` 委托路径与 PAT 导入路径作为等价替代。
2. **隔离 gh**：gh 与 git 全程只认 `$GQY_HOME/github/<profile>/` 下的配置目录（`GH_CONFIG_DIR` / `GIT_CONFIG_GLOBAL`），不读不写宿主 `~/.config/gh` 与 `~/.gitconfig`，令牌不外泄。

在 012 图片展示、015 表情包之后，这是第一个**碰外部凭据与外部二进制**的插件，也是第一次把「参考数据布局 → 隔离账号存储 → 外部命令执行」串成一条链。

## 范围

**做：**

- `manifest.json`：id `github-account-default`；name `GitHub 账号（顾清影 专属）`；version `1.0.0`；protocol `gqy-jsonrpc-v1`；command `["node","index.js"]`；cwd `.`；timeout_ms `120000`；concurrency `single-channel`；actions `status` / `login` / `login-wait` / `logout` / `env` / `gh` / `git`（各带 JSON Schema）；provides / requires 空。字段按 04 P3 与蓝图 manifest 全字段表。
- `index.js`：换行 JSON-RPC 2.0（initialize / call / shutdown，stdin EOF 自退），错误码分层（-32700 / -32600 / -32601 / -32602 / -32000），任何错误不崩进程。
- 七个动作（契约见仓库 `docs/protocol.md`，唯一事实来源）：
  - `status`：本地状态、profiles 扫描、gh/git 探测（60s 缓存）、`probe=true` 用 `GET /user` 复核且永不抛错；
  - `login`：`route=device`（原生设备码：`/login/device/code` → 返回 `user_code` + `verification_uri`，pending 落 `pending.json`）、`route=gh`（委托隔离 `gh auth login --web`，解析一次性码并保持子进程存活）、`route=token`（PAT 校验后落盘）、`route=import`（收养参考布局，身份与令牌不一致时 `force` 才继续）；
  - `login-wait`：单次 ≤90 秒的分片等待，状态机 `ready / pending / expired / denied / failed / missing`，device 轮询遵守 `interval` 与 `slow_down`；
  - `logout`：清 hosts.yml 令牌与 account.json，`purge=true` 删整个 profile 目录；
  - `env`：`GH_CONFIG_DIR` / `GIT_CONFIG_GLOBAL` 等隔离环境与可直接粘贴的 `exports`；
  - `gh` / `git`：argv 数组、不过 shell、进程组回收、30s 默认/90s 上限、输出各截断 1MiB、统一脱敏；`gh` 拒绝 `auth token` / `auth login` / `auth logout`。
- 隔离存储：`$GQY_HOME/github/<profile-slug>/` 下的 `account.json`、`gh/config.yml`、`gh/hosts.yml`、`gitconfig`、`profile.json`、`pending.json`；目录 `0700`、文件 `0600`、临时文件 + rename 原子写；路径规范化与软链解析后必须仍在根内。
- 仓库标准基线（STANDARDS §1）：`LICENSE` / `NOTICE` / `README.md` / `CONTRIBUTING.md` / `SECURITY.md` / `CHANGELOG.md` / `.editorconfig` / `.gitignore` / `Makefile`（`make check`）/ `.github/`（CI + Issue / PR 模板 + dependabot）。
- 测试四组（零依赖，生产代码路径，真实子进程）：`test/manifest.mjs`、`test/handshake.mjs`、`test/isolation.mjs`、`test/real-upstream.mjs`（真实上游，无令牌时 SKIP）。
- 文档：仓库 `README.md`、`docs/README.md`、`docs/protocol.md`（行为契约）、`CHANGELOG.md`；org 根 `README.md` 仓库地图登记；`.standards/STANDARDS.md` 与 `.github/STANDARDS.md` 仓库清单登记；本施工单与 `construction/README.md` 索引。
- 远程：建 `GQYTeam/gqy-plugins-GitHubAccount-default`（public）并推 `main`。

**不做（后置）：**

- 不做 OAuth App 注册、不内置 client_secret、不做令牌刷新与远端吊销（GitHub 设备码令牌不过期，吊销交使用者在设置页做）。
- 不做 SSH key 生成 / 上传：`git_protocol` 固定 `https`，走 gh 的 credential helper。
- 不做 GUI / 二维码渲染：只回 `verification_uri` 与 `user_code`。
- 不做多 host 批量同步、不做 keyring / 系统钥匙串集成。
- 不写 shell rc、不改宿主 git 全局配置、不改宿主 gh 登录态。

## 关键决定

- **单文件零依赖**（同 012 / 015）：插件只靠 Node 内置模块（`readline` / `fs` / `path` / `os` / `child_process` / `https`），装进 `$GQY_HOME/plugins/<id>/` 即可跑，CI 不装依赖。代价是 HTTP 客户端、YAML 子集解析都要自己写；用「只接受 gh 自己写出的形状」的窄解析把风险压住。
- **四条登录路径而非一条**：`device` 是主路径（符合「远程授权本地登录」，不需要本机 gh 与浏览器）；`gh` 委托复用 gh 自带的 OAuth App（本机已装 gh 时最省事，不用自己注册 client_id）；`token` 是兜底（任何环境都能用）；`import` 是把参考布局接进来的迁移路径——这正是本单「根据这个」的落点。
- **设备码需要 client_id**：GitHub 不提供公共 client_id，硬编码别人的 OAuth App 等于冒用；所以 `device` 路径要求使用者自己的 OAuth App / GitHub App client_id（参数 / `GQY_GITHUB_CLIENT_ID` / `$GQY_HOME/github/client.json` 三处），`gh` 路径交给 gh 自己的 client_id，`token` 路径完全绕开。README 写清取舍。
- **`login-wait` 分片**：manifest `timeout_ms` 上限 120 秒、蓝图 `single-channel` 语义要求阻塞时不并发，而设备码有效期 900 秒。因此 `login` 立即返回 pending，`login-wait` 单次最多 90 秒（默认 20），未完成回 `pending` + `remaining_s` 供再次调用；`pending.json` 让插件重载后还能接上 device 流程。
- **隔离靠环境变量而非复制宿主配置**：`GH_CONFIG_DIR` + `GIT_CONFIG_GLOBAL` + `GIT_CONFIG_NOSYSTEM=1` 就能让 gh 与 git 完全换一套配置（本机实测：指到参考布局即可得到 `GQYbot` 身份与 gh 凭据助手），比「改宿主全局配置再改回来」安全。子进程环境不继承宿主同名变量，避免外泄宿主登录态。
- **`hosts.yml` 兼容 gh 自己的形状**：`github.com: {users.<login>.oauth_token, git_protocol, oauth_token, user}`。这样复用的是整个 gh 生态（`gh auth status` / `gh api` / `gh auth git-credential`），git 的 credential helper 直接写 `!gh auth git-credential`，不用自己实现凭据助手。
- **`account.json` 保持参考布局同形**（只有 `login` + `id`），插件自己的元数据（来源、scope、git 身份、时间戳）另放 `profile.json`：既兼容参考工具，又不用往旧文件里塞字段。
- **令牌只有两处**：进程内存与 `hosts.yml`（0600）；`pending.json` 只有短时 device_code。所有结果 / 错误 / stderr 统一脱敏（`gh[pousr]_*` / `github_pat_*`），`gh` 动作拒绝 `auth token`，从机制上堵住「插件把令牌当输出」这条路。
- **`gh` / `git` 不过 shell**：argv 直接 `spawn`，不做变量展开、不拼命令行；独立进程组（`detached`）保证超时能整组回收，不残留孤儿（对齐 04 P8 的回收思路）。
- **`status` 的 probe 不抛错**：状态查询是「可观测」而不是「判定」；401 / 403 / 网络失败都装进 `verified.ok=false`，让调用方自己决定。这样 agent 在令牌过期时仍能看到本地状态。
- **`import` 只读源目录、只复制**：迁移不能破坏原数据；复制后统一收紧权限位，源目录一个字节不改。
- **`concurrency: single-channel`**：七个动作里 `login-wait` 会阻塞数十秒，且 pending 状态与 gh 子进程都是进程内的单实例状态；串行处理让语义确定，也符合插件串行 stdin 的实现。

## 落点

```
gqy-plugins-GitHubAccount-default/
  manifest.json             # 新增：七个动作与 schema
  index.js                  # 新增：stdio JSON-RPC + 隔离存储 + 四条登录路径 + gh/git 执行
  package.json              # 新增：test 串起四组测试
  docs/protocol.md          # 新增：行为契约（唯一事实来源）
  docs/README.md            # 新增：文档索引、manifest 约定、动作语义、当前状态
  test/manifest.mjs         # 新增：manifest + 仓库标准 + 无令牌扫描
  test/handshake.mjs        # 新增：协议握手与错误路径
  test/isolation.mjs        # 新增：路径穿越 / 诱饵文件 / 权限位 / 不写仓库
  test/real-upstream.mjs    # 新增：真实 GitHub 上游（无令牌 SKIP）
  README.md / CHANGELOG.md  # 新增：仓库门面与变更记录
  LICENSE / NOTICE / CONTRIBUTING.md / SECURITY.md / .editorconfig / .gitignore / Makefile
  .github/{workflows/ci.yml,ISSUE_TEMPLATE/*,pull_request_template.md,dependabot.yml}
GQY/docs/construction/031-GitHub账号插件.md   # 本单
GQY/docs/construction/README.md               # 索引 +1 行
README.md（org 根）                            # 仓库地图 +1 行、指令日志 +1 条
.standards/STANDARDS.md、.github/STANDARDS.md  # 仓库清单 +1 行
```

## 验收

```bash
cd gqy-plugins-GitHubAccount-default
make check        # node --check 全部 .js/.mjs + npm test（四组）
```

行为验收：

1. `initialize` 回 `{ok:true,name:"github-account-default",version:"1.0.0"}`，`id` 与请求一致；`shutdown` 后 0 退出；只关 stdin 也自退。
2. `status` 在空 `$GQY_HOME` 下返回 `logged_in:false`、`root` 在临时目录内、`profiles` 数组、`tools` 探测结果；`probe=true` 在有令牌时 `verified.ok=true` 且 `login/id` 与令牌一致，无令牌时 `verified.ok=false` 不抛错。
3. `login route=token` + 真令牌 → `state:"ready"`；`login route=import` 收养参考布局 → `account.json` 的 `login/id` 与源一致；`login route=device` 缺 client_id 报 `-32000`，给了 client_id 返回 `user_code` 与 `verification_uri`（真实上游会拿到 GitHub 设备码）。
4. `login-wait` 在无 pending 时回 `missing`；device 流程在 `wait_s` 内未完成回 `pending` 并带 `remaining_s`。
5. `env` 返回的 `GH_CONFIG_DIR` / `GIT_CONFIG_GLOBAL` 全在 `$GQY_HOME/github/<profile>/` 内，`exports` 可粘贴执行；`gh` 动作 `["api","user","--jq",".login"]` 返回登录名；`git` 动作 `["config","--global","user.name"]` 返回提交身份。
6. 隔离：profile 传 `../x` / `a/b` / 绝对路径 / `..` / 空串一律 `-32602` 且根外无新文件；宿主 `~/.config/gh` 与 `~/.gitconfig` 诱饵内容与 mtime 不变；插件不写仓库工作树；profile 目录 `0700`、文件 `0600`。
7. 令牌不外泄：任何动作返回、错误消息、stderr 都不含令牌原文；`gh` 动作拒绝 `auth token`。
8. GQY 侧端到端（真实 `gqy-server` + `$GQY_HOME/plugins/github-account-default/`）：`POST /api/plugins/github-account-default/load` → `state=ready`、`actions` 七项；`/call status`、`/call env`、`/call gh` 正常返回；`/unload` 后实例消失。
9. 标准与远程：`make check` 全绿；`git status --short` 干净；`git remote -v` 指向 `github.com/GQYTeam/gqy-plugins-GitHubAccount-default`；`gh repo view` 为 public。

## 留底

完成（2026-11-02）。仓库 `gqy-plugins-GitHubAccount-default`：提交链 `8b4d507`（首版，24 个文件、6405 行）→ `7b78133`（软链逃逸修复 + 文档校正）→ `acf5db5`（子路径逃逸回归 + 检查项数校正），远程 <https://github.com/GQYTeam/gqy-plugins-GitHubAccount-default>（public、默认分支 `main`、三个提交的 CI 均绿、已开分支保护：1 次评审 + 必需 CI 检查 + 禁强推 / 删）。

### 命令与结果

```bash
cd gqy-plugins-GitHubAccount-default
make check       # node --check 全部 .js/.mjs + npm test
make fmt-check   # OK
git status       # 干净
```

- `test/manifest.mjs` → OK，229 项（manifest 全字段 + 仓库标准 + 全仓令牌扫描）
- `test/handshake.mjs` → OK，347 项（initialize / status / env / logout / 错误码 / shutdown / EOF 自退）
- `test/isolation.mjs` → OK，562 项（含软链越界回归）（9 个非法 profile × 7 动作、诱饵 `~/.config/gh` 与 `~/.gitconfig` 零变化、根外零写入、0700/0600、`.probe/`）
- `test/real-upstream.mjs`（无令牌）→ `SKIP`，exit 0
- `GQY_GITHUB_TEST_LAYOUT=<参考布局> node test/real-upstream.mjs` → OK，281 项（token 登录 / status probe / env / gh / git / logout / import / device 探针 / 委托 gh 设备码）

### GQY 侧端到端（真实 gqy-server + 临时 `$GQY_HOME`）

把仓库 rsync 到 `$GQY_HOME/plugins/github-account-default/`，起真实 `gqy-server`（`HOST=127.0.0.1`、随机端口、临时 `GQY_HOME`）：

- `POST /api/plugins/github-account-default/load` → `state=ready`，`actions` 七项；
- `/call status` → `logged_in:false`、`root` 在临时 `$GQY_HOME` 内、`tools.gh.available=true`；
- `/call login {"route":"import","path":"<参考 github/ 布局>"}` → `state:"ready"`、`source:"import"`、真实账号与 scope 与参考布局一致（`gh api user` 复核）；
- `/call status {"probe":true}` → `verified.ok=true`，`login` / `id` 与令牌一致；
- `/call env` → 四变量都在 profile 内、`exports` 四行全单引号；
- `/call gh ["api","user","--jq",".login"]` → 退出码 0、stdout 为登录名；
- `/call git ["config","--global","user.name"]` → 退出码 0、输出为隔离 gitconfig 里的提交身份；
- `/call logout {"purge":true}` → `state:"logged-out"`、`removed` 六项、profile 目录删除；
- `/unload` → 实例消失；server 日志无异常。

### 独立验收

- 独立 verifier 按 9 项清单验收（标准 / manifest / 语法与测试 / 独立协议探针 / 隔离对抗 / 令牌安全 / GQY e2e / 文档一致性 / 施工单与组织登记）：结论见 Lead 汇报；发现项按需修复后复验。

### 独立验收与返工（2026-11-02）

- 独立 verifier 按 9 项清单验收：7 项 PASS、2 项 FAIL、1 项低危。
- FAIL ①（高危，隔离）：`logout`（非 purge）只做词法校验，会跟随「根内指向根外的目录软链」删除根外文件；`env` 返回逃逸路径；`gh` / `git` 会用逃逸的 `GH_CONFIG_DIR` / `GIT_CONFIG_GLOBAL` 在根外写。复现：`github/evil -> 根外目录`，`logout {profile:"evil"}` 真删掉根外 `account.json`。
  修复（`7b78133`）：新增 `profileEscapeReason` / `assertProfilePathsSafe`，对 profile 目录及 `gh/`、`gh-state/`、`gh-data/`、`gitconfig` 逐个做词法 + realpath 校验；`removeFileQuietly` 改走 `guardInternalPath`；`logout`（含 purge）、`env`、`gh`、`git`、`login`、`login-wait`、读令牌与落盘前一律 `-32000`；`status` 永不抛错，逃逸时 `isolated.outside_writes:true` + `blocked` 原因、`logged_in:false`、`account:null` 且不读逃逸目录。`test/isolation.mjs` 补软链越界 / 子路径逃逸 / 根内合法软链 / 软链环回归，检查项 415 → 562。
- FAIL ②（文档）：`docs/README.md` 检查项数漂移（361/411 与实测 347/415 不符）、`README.md` 工具名用连字符（按 GQY `ToolName` 应为下划线：`github_account_default__status` 等）；另修正「未登录可用动作」与 `auth token` 拒绝规则的表述。
- 低危：`.github/ISSUE_TEMPLATE/config.yml` 残留 `<REPO>` 占位符，已替换。
- 返工后复跑：`make check`（229 / 347 / 562 全绿）、真实上游 281 项 PASS、`fmt-check` OK。
- 第二轮独立复验（verifier，HEAD `acf5db5`）：全 PASS、结论「可发布」。证据要点：根级软链的 `logout`（含 purge）/ `env` / `gh` / `git` / `login` / `login-wait` 全 `-32000` 且根外文件存在性 / 内容 sha256 / 权限 / mtime 零变化；子路径逃逸（`gh/`、`gitconfig`、`gh-state/`、`gh-data/`）同样拒绝且不泄露；根内合法软链不误伤（`outside_writes:false`、`env` realpath 在根内）；软链环不崩；单文件（`pending.json` / `profile.json` / `hosts.yml`）指向根外也拒绝（13/13）；文档四项（数字 229/347/562/281、工具名下划线、未登录范围、`auth token` 规则）与 `status.isolated` 三处一致；`<REPO>` 占位符 0 残留；`make check` / 真实上游 281 / `fmt-check` / 令牌扫描与工作树干净逐项复现。

### 偏差与实现说明

- 无功能性偏差。执行中收敛了四处契约细节，都以「契约为主、实现与测试对齐」收口：
  1. `env.exports` 统一「所有值单引号包裹（含 `'1'` / `'0'`）」，契约示例同步改齐；
  2. 补 `XDG_STATE_HOME` / `XDG_DATA_HOME` 覆盖与 `<root>/.probe/` 探测隔离——实测 `gh --version` 也会往 `$XDG_STATE_HOME/gh/device-id` 写文件，只设 `GH_CONFIG_DIR` 不够，否则会写进宿主 `~/.local/state`；
  3. `gh` 动作额外拒绝 `--show-token`（令牌不经插件输出的防御性补充），文档同步；
  4. `route=gh` 解析到设备码后向 `gh` 子进程 stdin 写一个换行（等价使用者按回车让 gh 继续），stdin 保持打开直到 `login-wait` 收口；`login-wait` 无 pending 时回 `missing` 不报错。
- `login-wait` 单次 ≤90s 的分片设计成立：设备码有效期 900s，超时分片返回 `pending` + `remaining_s`，`pending.json` 让 device 流程跨插件重载可续。
