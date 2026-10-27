# DSDock

**DeepSeek Harness 的外部管理器**：DSH 版本管理，以及插件的安装、更新、快照、回滚、救援。

DSDock 不是 DSH 插件，不做聊天界面，也不替代 DSH。它在 DSH 外面工作，所以 DSH 起不来的时候也能把它救回来。

> 🚧 **M1 开发中**：插件生命周期的命令行（安装、卸载、更新、禁用、快照、回滚、启动测试）已经可以用，在 Linux 上对真实的 DSH 做过端到端测试。DSH 版本管理和图形界面还没做。

## 计划中的功能

- **安全安装**：装之前检查兼容性和风险 → 拍快照 → 安装 → 验证 → 失败自动回滚
- **快照与回滚**：每次变更前自动拍快照，一键回到任意一个时间点
- **救援模式**：DSH 起不来时诊断原因，回到上次正常的状态，二分定位出问题的插件
- **DSH 版本管理**：多个版本并存、一键切换、每个 profile 单独锁定版本、降级前警告
- **冲突分析**：哪个插件覆盖了哪个插件的配置，你写的覆盖在插件更新后是否已经过时
- **整合包**：导出、导入、分享一整套插件组合

## 试用（命令行）

需要 Go 1.26 或更高版本，以及能运行 DSH 的环境（Node ≥ 24.9、pnpm）。

```bash
go build -o dsdock .

./dsdock status                                  # DSH 环境和所有 profile 的健康状态
./dsdock check dsh-meme -p web                   # 只做装前检查
./dsdock add dsh-meme -p web --smoke             # 安装，装完真正启动一次检查
./dsdock update dsh-meme -p web                  # 更新到兼容当前 DSH 的最新版本
./dsdock disable <id> -p web                     # 禁用一行配置，不卸载
./dsdock snap list -p web                        # 快照时间线
./dsdock snap restore <快照id>                    # 回到某个时间点（可撤销）
./dsdock smoke -p web                            # 启动测试：每个插件都激活了吗？
./dsdock help
```

每次改动之前都会自动拍快照；改动后配置无法组装、或者（加了 `--smoke` 时）插件没能激活，都会**自动回滚**。

默认使用 PATH 里的 `dsh`。要指定某个安装，设置 `DSDOCK_DSH=<dsh 的 lib/bin.js>` 和 `DSDOCK_NODE=<node>`。

## 文档

- [设计方案](docs/design.zh-CN.md)
- [M0 实测接力手册](docs/m0-handoff.md)
- [M0 实测结果](docs/m0-results/)
- [子系统说明](docs/subsystems/)
- [LESSONS](LESSONS.md)：踩过的坑和由此定下的规则

## 许可证

[MIT](LICENSE)
