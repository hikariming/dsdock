**DSDock 预览版**：DeepSeek Harness 的外部管理器。管理插件（装之前检查、改坏了自动回滚）、救援出问题的 profile、管理多个 DSH 版本。

> ⚠️ 预览版，还在频繁变化。所有改动 profile 的操作都会先拍快照、失败自动回滚，但请先在不重要的 profile 上试用。

## 桌面版（推荐）

下载 **`DSDock-{{TAG}}-macos-universal.dmg`**（Apple 芯片和 Intel 都能用），打开后把 DSDock 拖进"应用程序"。

**第一次打开**：这个预览版还没有 Apple 开发者证书签名和公证，macOS 会拦截。任选一种方式放行：

- 在"应用程序"里**右键点 DSDock → 打开**，在弹出的对话框里再点"打开"；
- 或者在终端里执行：`xattr -dr com.apple.quarantine /Applications/DSDock.app`

打开后 DSDock **不会出现在 Dock 里**，而是在屏幕右上角的菜单栏出现一只鲸鱼：

- **左键**：弹出快捷面板：
  - 顶部输入框**快速提问**，比如"找一下桌面上上周的 PDF"，DSH 在后台完成；要执行有风险的命令时会先问你；
  - **DSH Web / DSH Desktop** 两个按钮一键打开完整界面；问完点"在 DSH Web 里继续"，刚才的对话在会话列表最上面；
  - 下面是每个 profile 的状态，可以切换它用的 DSH 版本；
- **右键**：菜单（打开主窗口、打开 DSH Web、退出）；
- 主窗口关掉后 DSDock 仍然常驻菜单栏。

需要电脑上有 Node（DSH 已发布版本要求 `^22.19.0 || >=24.0.0`）和 pnpm。还没有 DSH？在主窗口的"DSH 版本"里点一下就能装。

## 命令行版

| 文件 | 适用于 |
|---|---|
| `dsdock-{{TAG}}-darwin-universal.tar.gz` | 不确定就选这个 |
| `dsdock-{{TAG}}-darwin-arm64.tar.gz` | Apple 芯片 |
| `dsdock-{{TAG}}-darwin-amd64.tar.gz` | Intel 芯片 |

```bash
tar -xzf dsdock-{{TAG}}-darwin-universal.tar.gz
xattr -d com.apple.quarantine dsdock 2>/dev/null   # 浏览器下载的文件需要这一步
mkdir -p ~/.local/bin && mv dsdock ~/.local/bin/
dsdock dsh install latest   # 安装 DSH
dsdock status               # 所有 profile 的健康状态
dsdock gui                  # 在浏览器里打开图形界面
dsdock help
```

校验文件完整性：`shasum -a 256 -c SHA256SUMS`

## 反馈

遇到问题请开 [Issue](https://github.com/hikariming/dsdock/issues)，附上截图，以及 `dsdock status`、`dsdock log -p <profile>` 的输出（日志里的令牌和密钥已经自动脱敏，提交前也请再看一眼）。
