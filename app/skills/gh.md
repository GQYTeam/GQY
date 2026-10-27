# gh — GitHub CLI 完整使用参考

> 本 skill 供顾清影 agent 使用。需要操作 GitHub 时，用 `shell` 工具执行 `gh` 命令。
> 前置：本机需已安装 gh 并 `gh auth login`（`gh auth status` 可查）。

## 命令速查（按场景）

### 认证与账户
- `gh auth status` — 当前登录态、token 权限、账号
- `gh auth login` — 登录（交互式）
- `gh auth switch` — 切换账号
- `gh auth token` — 打印 token（谨慎）

### 仓库（repo）
- `gh repo clone <owner>/<repo>` — 克隆
- `gh repo view [<owner>/<repo>]` — 查看仓库信息（含 README、star、fork）
- `gh repo create <name> --public|--private` — 新建仓库
- `gh repo fork <owner>/<repo>` — fork
- `gh repo list <owner>` — 列出仓库
- `gh repo sync` — 同步 fork

### 拉取请求（pr）
- `gh pr list` / `gh pr list --state open` — 列出 PR
- `gh pr view [<number>]` — 查看 PR 详情（含 diff、评论、状态）
- `gh pr create --title "..." --body "..."` — 创建 PR
- `gh pr checkout <number>` — 检出 PR 分支
- `gh pr diff <number>` — 查看 diff
- `gh pr review <number> --approve|--comment|--request-changes`
- `gh pr merge <number> --squash|--rebase|--merge`
- `gh pr checks <number>` — 查看 CI 状态

### 议题（issue）
- `gh issue list` / `gh issue view <number>`
- `gh issue create --title "..." --body "..."`
- `gh issue comment <number> --body "..."`
- `gh issue close <number>` / `gh issue reopen <number>`

### 搜索（search）
- `gh search repos <query>` — 搜仓库
- `gh search code <query>` — 搜代码（需登录）
- `gh search issues <query>` — 搜 issue/PR
- `gh search prs <query>` — 搜 PR
- 语法：`owner:xxx language:go stars:>1000` 等限定符

### 发布与 release
- `gh release list`
- `gh release view <tag>`
- `gh release create <tag> --title "..." --notes "..." [files...]`
- `gh release upload <tag> <file>`

### Actions / CI
- `gh run list` / `gh run view <id>`
- `gh run watch <id>` — 实时看日志
- `gh workflow list` / `gh workflow run <name>`

### 通用 API（api）
- `gh api <endpoint>` — 直接调 GitHub REST API
- `gh api /user` — 当前用户信息
- `gh api repos/{owner}/{repo}/issues/1`
- `gh api --paginate <endpoint>` — 翻页取全量
- `gh api -X POST <endpoint> -f field=value` — 带字段的写操作

### gist
- `gh gist create <file>` / `gh gist list` / `gh gist view <id>`

## 使用要点

1. **读优先用 `gh api` 或 `gh ... view`**，写操作前先确认影响范围。
2. **批量/翻页**：`--paginate` 或 `--limit`，避免一次性拉太多。
3. **输出格式**：`--json` 加 `--jq` 提取字段，例如 `gh pr list --json number,title --jq '.[] | "\(.number) \(.title)"'`。
4. **不要写 token 到文件/日志**；`gh auth token` 仅用于管道内临时使用。
5. **操作他人仓库前**先 `gh repo view` 确认权限（是否有 push/fork）。
6. **不确定的写操作**（merge、close、delete）先 `ask_user` 确认。

## 常见组合

```bash
# 查某 PR 的 CI 状态
gh pr checks 123

# 列出我名下所有 open PR
gh search prs --author=@me --state=open

# 查某文件最后一次修改的提交
gh api "repos/{owner}/{repo}/commits?path=README.md&per_page=1"

# 给 issue 打标签
gh issue edit 123 --add-label bug
```
