# core · 进程形态与启动

**状态**：部分（config 骨架已建，2026-11-02）

**对应设计决定**：D4（01-架构）、A3（08-鉴权与配额）、CF2（12-配置与热更新）。

## 具体形状

### 命令

| 命令 | 职责 | 常驻 |
| --- | --- | --- |
| `gqy-server` | HTTP API、网关、agent loop、插件运行时 | 是 |
| `gqy-eval` | 评测：矩阵、循环、判定、落盘回放 | 否 |
| `gqy-clean` | 清理：`--session <id>`、`--expired` | 否 |
| `gqy-import` | 导入外部会话/数据 | 否 |

### 启动顺序（gqy-server）

1. 读启动参数（环境变量，见 `config.md` 的 Startup 表）。
2. 建数据根 `$GQY_HOME` 并强制权限（ST6）。
3. 读三份配置文件 → 构造不可变 Snapshot（CF4）；失败用默认值并记日志。
4. 起 HTTP 监听（默认 `127.0.0.1:5173`，A3）；未设置 `GQY_AUTH_TOKEN` 时只回环。
5. 启动文件监视（CF6）与插件运行时（按配置）。
6. 进入服务循环。

### 信号与退出

- `SIGINT` / `SIGTERM`：停止接收新连接，等在途请求排空（上限 `short_drain` 2s），关闭插件进程组（P8），退出。
- 子命令（eval/clean/import）跑完即退，不接信号热切换。

### 环境变量

启动参数全部来自环境变量，清单与默认值见 `config.md` Startup 表；`.env` 只填充环境里不存在的变量，真实环境变量优先（CF2）。

## 边角情况

- 配置半截/缺失：默认值兜底，记日志，服务照常起（CF9）。
- 端口被占：明确报错退出，不静默换端口。
- 数据根不可写：报错退出，不降级到临时目录。
- 插件进程未关干净：进程组回收兜底（P8），孤儿进程计数必须为 0。
- 令牌未设置但监听非回环：拒绝启动（A3）。

## 计划中的测试

- `TestServerStartsWithDefaults`：无配置、无令牌时在回环端口起服务。
- `TestNonLoopbackWithoutTokenRefused`：非回环 + 无令牌拒绝启动。
- `TestSIGTERMDrainsInFlight`：退出时在途请求排空，新连接不再接收。
- `TestDataRootCreatedWithPermissions`：数据根 0700。
- `TestSubcommandsExitAfterRun`：eval/clean 跑完即退。

## 对应代码

部分已建：`app/internal/config/env.go`、`types.go`（Snapshot、Startup、ProviderView）。待建：`cmd/gqy-server`、`cmd/gqy-eval`、`cmd/gqy-clean`、`cmd/gqy-import`、HTTP 监听与信号处理。旧实现参考：`app/cmd/*`（git 历史 HEAD~2，仅作事实线索）。
