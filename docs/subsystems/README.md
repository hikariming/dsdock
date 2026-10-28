# 子系统

改动某个子系统的代码之前，先读它的说明，再看链接的源码。

| 子系统 | 包 | 说明 |
|---|---|---|
| 编排 | `internal/core` | "计划 → 确认 → 执行"：CLI 和 GUI 都只是它的外壳。新功能先加到这里 |
| 图形界面 | `internal/gui` | 本机 HTTP 服务加原生 JS 页面（`assets/`）。只监听 127.0.0.1、每次运行的随机令牌、检查 Host 和 Origin；长任务排队执行。改界面后用 `scripts/gui-shots.mjs` 截图检查 |
| 桌面版 | `internal/desktop` | `-tags desktop`。菜单栏图标、面板、主窗口；通过 `gui.Desktop` 接口（Open、Quit、ResizePanel）和页面通信 |
| 救援 | `internal/rescue` | 诊断顺序：patch 文件 → 组装 → 启动；"上一个正常状态"要和当前配置不同（`snapshot.LastGoodOther`） |
| 事务 | `internal/txn` | [txn.md](./txn.md)：所有改动 profile 的操作的唯一入口 |
| 组装结果解析 | `internal/layer` | 解析 `--dump-config` 输出和 DSH 的 stderr 警告；格式见设计文档 K8、K20 |
| 快照 | `internal/snapshot` | 内容寻址的文件存储加 sqlite 元数据；设计文档 §4.5 |
| 装前检查 | `internal/preflight` | 规则 PFxx；兼容性判断移植自 DSH，见 LESSONS 第 6 条 |
| 用户层编辑 | `internal/userpatch` | 禁用和启用；语义照搬官方插件管理器，见 LESSONS 第 4 条 |
| 进程 | `internal/proc` | 启动测试、进程组、查找运行中的 DSH；见 LESSONS 第 1、2 条 |
| DSH 环境 | `internal/dshenv` | 唯一启动 dsh 的地方；按版本检查 Node（LESSONS 第 10 条） |
| 版本管理 | `internal/versions` | 用 pnpm 安装各版本、默认版本、按 profile 锁定、切换前预检（以目标版本实际组装为准，LESSONS 第 9 条） |
| 转发器 | `internal/shim` | 以 `dsh` 的身份运行，选版本后 exec 到 Node；见设计文档 §4.6 |
| dshfind 目录 | `internal/catalog` | PF06/PF07 的数据；按仓库匹配，防止假冒仓库连累同名的官方包 |
