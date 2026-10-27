# 子系统

改动某个子系统的代码之前，先读它的说明，再看链接的源码。

| 子系统 | 包 | 说明 |
|---|---|---|
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
