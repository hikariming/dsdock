# Notes for coding agents

本项目处于 **M1**：插件生命周期的命令行已经可用；M0 的 macOS 和 Windows 实测还在进行。

## 构建和测试

```bash
go build -o dsdock .
go vet ./... && go test ./...
# 对真实 DSH 跑集成测试（需要 Node ≥ 24.9 和 pnpm）：
DSDOCK_IT_DSH=<dsh 的 lib/bin.js> DSDOCK_IT_NODE=<node> DSDOCK_IT_NETWORK=1 go test ./internal/cli/ -run Integration -v
```

改代码之前先读 [LESSONS.md](LESSONS.md) 和 [docs/subsystems/](docs/subsystems/)。所有改动 profile 的操作都必须经过 `internal/txn`；新功能先加到 `internal/core`，CLI（`internal/cli`）和 GUI（`internal/gui`）只做展示。

桌面版（`internal/desktop`）只能用 `-tags desktop` 在有 Wails 依赖的平台上编译（macOS 自带；Linux 需要 GTK4/WebKitGTK 开发库）。没有 Mac 时，推送后看 `desktop` 工作流的产物 `desktop-screenshots`：那是 macOS 上真实运行的截图。

改了界面，用无头浏览器截图自己看一遍：

```bash
dsdock gui --no-open --port 38200   # 记下打印的地址
CHROME=<chromium 或 chrome-headless-shell> node scripts/gui-shots.mjs '<地址>' /tmp/shots steps.json
```

## M0 实测

- 如果你被要求做 M0 实测：读 [docs/m0-handoff.md](docs/m0-handoff.md)，严格遵守第 1 节的安全规则，结果写到 `docs/m0-results/<平台>.md`，并更新手册末尾的"接力进度"表。
- 修改设计之前，先读 [docs/design.zh-CN.md](docs/design.zh-CN.md)。实测结论推翻了某条【待实测】假设时，要同步更新设计文档 §2 的"状态"一列，以及受影响的章节。
- 不要读写用户真实的 `~/.dsh`。所有实验都用单独设置的 `DSH_HOME`。
