**DSDock 的第一个 macOS 预览版**，只有命令行，图形界面在 M2 开发中。功能见 [README](https://github.com/hikariming/dsdock#readme)。

> ⚠️ 预览版，还在频繁变化。所有改动 profile 的操作都会先拍快照、失败自动回滚，但请先在不重要的 profile 上试用。

## 下载哪个

| 文件 | 适用于 |
|---|---|
| `dsdock-{{TAG}}-darwin-universal.tar.gz` | **不确定就选这个**，Apple 芯片和 Intel 都能用 |
| `dsdock-{{TAG}}-darwin-arm64.tar.gz` | Apple 芯片（M1 及以后） |
| `dsdock-{{TAG}}-darwin-amd64.tar.gz` | Intel 芯片 |

## 安装

```bash
tar -xzf dsdock-{{TAG}}-darwin-universal.tar.gz
xattr -d com.apple.quarantine dsdock 2>/dev/null   # 见下面的说明
mkdir -p ~/.local/bin && mv dsdock ~/.local/bin/   # 或者任意在 PATH 里的目录
dsdock version
```

**为什么要 `xattr`**：这个预览版还没有 Apple 开发者证书签名和公证，只做了 ad-hoc 签名。用浏览器下载时 macOS 会打上"隔离"标记并拦截运行，`xattr` 这一行就是去掉这个标记。用 `curl` 下载的文件没有这个标记，可以跳过。正式版会做签名和公证。

校验文件完整性：`shasum -a 256 -c SHA256SUMS`

## 试一试

需要 Node（DSH 已发布版本要求 `^22.19.0 || >=24.0.0`）和 pnpm。

```bash
dsdock dsh install latest        # 安装 DSH，第一个版本自动设为默认
dsdock dsh shim                  # 装上 dsh 转发器，按提示把目录加进 PATH
dsdock status                    # 所有 profile 的健康状态
dsdock add <插件> -p web --smoke  # 安装插件，装完真正启动一次检查，失败自动回滚
dsdock help
```

已经有 `npm i -g` 装的 DSH？`dsdock dsh adopt` 可以直接接管，不移动也不删除。

## 反馈

遇到问题请开 [Issue](https://github.com/hikariming/dsdock/issues)，附上 `dsdock status` 和 `dsdock log -p <profile>` 的输出（日志里的令牌和密钥已经自动脱敏，提交前也请再看一眼）。
