# DSDock 设计方案

> 状态：草案 v0.1 · 2026-10-27
> 依据：DeepSeek Harness 上游仓库 `deepseek-ai/deepseek-harness`（HEAD，2026-10-27）的文档，以及 dshfind 目录数据。
> 标记说明：**【已核实】** 表示有上游文档或 npm 数据支撑；**【待实测】** 表示需要在 M0 阶段实际跑一遍才能确认，见 [m0-handoff.md](./m0-handoff.md)。

---

## 0. 一句话

**DSDock 是 DeepSeek Harness 的外部管理器**：负责管理 DSH 版本，以及插件的整个生命周期（安装、更新、快照、回滚、救援、冲突分析）。它本身不是 DSH 插件，不做聊天界面，也不替代 DSH。

- 名字：DSDock（DS + Dock，取"船坞、程序坞"的意思：插件停靠、检修、出坞）
- 中文名：待定
- 命令行程序：`dsdock`

---

## 1. 背景与定位

### 1.1 为什么一定要在 DSH 外面

DSH 自己已经有插件页（"已安装"和"发现"两个标签，带更新和兼容性检查），所以 DSDock **不做第二个插件浏览器**。它只做 DSH 内部做不到的事：

| 场景 | 为什么 DSH 内部做不到 |
|---|---|
| **DSH 起不来**（装了坏插件、升级后不兼容） | 内置插件页也打不开了 |
| **跨时间**：快照、回滚、版本切换 | DSH 只管"现在"，没有历史 |
| **跨 profile、跨版本、跨机器** | 运行中的 DSH 只能看到自己的那个 profile 和那个版本 |
| **全局视角**：插件之间的覆盖、用户覆盖是否过时 | 需要同时对比所有层和插件更新前后的变化 |

### 1.2 参考对象：Magpie

`yetone/magpie`（5 周 2.3 万 star）是模型层的统一入口。值得借鉴的地方：

1. **不取代原工具**，只改原工具自己的配置，迁移成本几乎为零；
2. **从别人那里接用户**：支持导入其它工具的配置，提供 "Add to magpie" 深链；
3. **Go 单二进制**，同时提供 GUI、TUI、Web UI、CLI；跨平台从第一天开始；
4. **迭代极快**（版本号 v0.1.1734），靠的是自动更新加上密集的测试；
5. **边界清楚**：有风险的功能放到社区插件里，主程序保持干净。

### 1.3 竞品

| 项目 | ★ | 做了什么 | DSDock 和它的区别 |
|---|---|---|---|
| anywhere-labs/dsh-desktop | 33145 | 插件生态桌面端 | 它是聊天客户端，DSDock 是管理工具，两者互补 |
| dsh-tauri/deepseek-harness-desktop | 3273 | 8MB 安装包、预装插件 | 同上 |
| EAC-Desktop | 2269 | 让上百个插件共存 | 它靠"预先调好的组合"，DSDock 靠检查、快照、冲突分析 |
| yyh-001/DSH-X | 756 | 启动器：多实例、版本、插件、整合包 | **最接近的竞品**。DSDock 的区别在于事务式变更、救援、冲突分析，以及 dshfind 的数据 |
| deepseek-harness-studio | 695 | 一键安装和管理插件 | 9 月 1 日后停更 |
| dsh-plugin-gating-hub | 112 | 升级前预检、回滚点 | 它是 DSH 插件，DSH 起不来时救不了 |
| dsh-config-manager | 245 | 备份、恢复、迁移 | 同上 |

### 1.4 不做什么

- 不做聊天、会话界面；
- 不做模型和供应商管理（那是 Magpie 的地盘，两者可以并存）；
- 不 fork、不修改 DSH 本身；
- **永远不直接改写 profile 里的依赖**：安装和卸载一律交给官方的 `dsh plugin`。

---

## 2. DSH 机制事实

| # | 事实 | 来源 | 状态 |
|---|---|---|---|
| K1 | DSH 以 npm 包 `@deepseek-ai/dsh` 发布，可执行文件是 `lib/bin.js`，要求 Node `>=24.9.0`；源码仓库用 pnpm 11 | npm、`apps/cli/package.json` | 【已核实】 |
| K2 | 发布渠道：`latest`=0.2.1-rc.2，`next`=0.2.1-rc.4，`alpha`=0.2.1-alpha.5；共 38 个版本，10 月 9 日到 26 日发了 8 个 | npm dist-tags | 【已核实】 |
| K3 | `DSH_HOME` 默认是 `~/.dsh`，可以用环境变量覆盖。**必需插件**启动失败时，日志写在 `$DSH_HOME/logs/startup-*.log` | `apps/cli/reference/README.md`、Linux V1 | 【已核实】 |
| K4 | profile 位于 `$DSH_HOME/profiles/<name>`，是一个 pnpm 项目：`package.json`（依赖，加上有序的 `dsh.profile.bundles`）、`cordis.patch.yml`（用户层，默认内容是 `[]`）、`pnpm-lock.yaml`、`pnpm-workspace.yaml`（`nodeLinker: hoisted`）。另外还有启动时生成的 `cordis.yml`（空的根配置）和 `.plugin-manager/logs/`（每次操作的 pnpm 日志）。`DSH_HOME` 顶层有 `.credentials.yaml`（**凭据**）、`.anonymous-user-id`、`storages/` | publish.md、Linux V3 | 【已核实】 |
| K5 | `dsh plugin --profile X <args>` 在 profile 目录里**原样转发 pnpm 命令**（add、remove、update、why…），要求 PATH 里有 pnpm。每次成功后，会根据实际安装结果重新整理 `dsh.profile.bundles` | CLI 参考文档 | 【已核实】 |
| K6 | 配置叠加顺序：各组合包（按列表顺序）→ profile 的 `cordis.patch.yml` → `$DSH_HOME/cordis.patch.yml` → 命令行的 `--patch` | CLI 参考文档 | 【已核实】 |
| K7 | 同一个 id 的行，后面的层**整行覆盖**前面的；`config` 是整块替换，不深度合并 | 同上 | 【已核实】 |
| K8 | `--dump-config` 只组装配置、不启动（约 150ms）。每层开头是 `# == <包名>`，被改过的层是 `# == <包名>, patched by <文件>`；目标找不到时 stderr 输出 `dsh: [<文件>] patch: entry "<id>" not found`，**退出码仍为 0**。对**不存在**的自定义 profile 会直接报错，不会自动创建。`--dump-config-schema` 输出约 600KB 的 JSON Schema | CLI 参考文档、Linux V4 | 【已核实】 |
| K9 | 内置组合包（`dsh-base`、`dsh-web-app` 等）**总是从当前运行的 dsh 安装里解析**，外部插件从 profile 的 `node_modules` 解析，所以 **profile 和 DSH 版本是分开的** | 同上 | 【已核实】 |
| K10 | 插件兼容性先看 `engines.dsh`，没有才看 `@deepseek-ai/dsh*` 的 peer 依赖；安装和启动时都会检查 | `upgrade-guide/v0.2.1-alpha.3/plugin-engines-dsh` | 【已核实】 |
| K11 | 用 CLI 增删组合包后，**正在运行的 profile 要重启才生效**；改 `cordis.patch.yml` 可以热重载 | CLI 参考文档 | 【已核实】 |
| K12 | DSH **内部**的插件管理器可以热安装（`installBundle` / `removeBundle`），开着热重载时重复安装会报 `already-installed`。远程客户端是给 DSH 自己的 Web 前端用的，经过 API Gateway、需要启动令牌，**没有面向外部程序的公开接口**。CLI 方式下直接 `add <包名>@<新版本>` 就能更新，顺序不变 | 升级指南、插件管理器 README、Linux V6/V9 | 【已核实】 |
| K13 | 从 git 安装时，pnpm 10 以上会拦截 `prepare` 脚本，需要在 profile 的 `pnpm-workspace.yaml` 的 `allowBuilds` 里手动放行 | publish.md | 【已核实】 |
| K14 | `--from-default-profile` 用官方模板重置 profile，先在临时目录组装好再替换，失败时保留旧目录 | CLI 参考文档 | 【已核实】 |
| K15 | 会话格式**只能往前迁移**（目前 v0 到 v4，0.2.1-rc.2 和 rc.4 都是 v4）。迁移会写一个新文件，**原文件保留**；遇到比自己新的格式会**直接报错** | 会话格式设计文档、`session-format-guard` 测试、Linux V2 | 【已核实】降级后的实际表现【待实测 V11】 |
| K16 | 每个版本都有结构化的升级指南：`docs/upgrade-guide/<版本>/<变更>/guide.md` | 上游仓库 | 【已核实】 |
| K17 | 官方插件索引格式 `format: 1`：一个 JSON 文件，列出插件的各个版本、来源（npm 或 git+commit）和 `engines.dsh` | `docs/user/guide/plugin-market.md` | 【已核实】 |
| K18 | 官方桌面版内置了插件市场，有自己的安装契约（dshfind 的 `/market/v1/plugins` 就是为它服务的） | dshfind 代码 | 【已核实】它的 `DSH_HOME` 和状态文件【待实测 V13】 |
| K19 | **两个组合包插入同一个 id 时，dump 不报警告，启动时前一个插件被静默丢弃** | Linux V4/V8 | 【已核实】 |
| K20 | **插件加载失败不会让 DSH 退出**。stderr 打印 `dsh: warning: N entry did not activate`，后面每行是 `<id> (<包名>): <原因>`（异常加调用栈，或者 `pending (waiting for service: <服务>)`）。dump 发现不了这类错误 | Linux V8 | 【已核实】 |
| K21 | web profile 的就绪信号：stdout 打印 `dsh web: http://127.0.0.1:<端口>/?token=<令牌>`（**带令牌，必须脱敏**），约 3.5 秒 | Linux V8 | 【已核实】 |
| K22 | 用 `dsh plugin --profile <任意名字>` 创建的 profile 只有 `dsh-base`，没有 web 服务；只有官方模板名（`web` 等）或 `--from-default-profile` 才带 `dsh-web-app` | Linux V3/V8 | 【已核实】 |
| K23 | `cordis.patch.yml` 的**无效修改**会被运行中的 DSH 静默忽略，但下次启动直接失败（YAML 解析错误）；有效修改会热重载 | Linux V7 | 【已核实】 |
| K24 | npm 上发布的 `@deepseek-ai/dsh` **没有 `engines`**。已发布版本的 tag（`dsh-v<版本>`）根目录 `package.json` 声明 `^22.19.0 \|\| >=24.0.0`（rc.2、rc.3、alpha.1 都是）；上游 HEAD 的 `apps/cli/package.json` 才是 `>=24.9.0`，**不能代表已发布版本**。Node 18 下报看不懂的 import 错误 | Linux V2、第二波 | 【已核实】 |
| K25 | 每个 DSH 版本用 npm 安装占 856MB；用 pnpm（全局缓存 + 硬链接）安装，两个版本合计 548MB。DSH 依赖的安装脚本里**只有 `dsh-subprocess-local` 需要运行**（给 node-pty 预编译的 macOS `spawn-helper` 加执行权限）；`node-pty` 自带六个平台的预编译二进制，`koffi` 的二进制通过可选依赖 `@koromix/koffi-<平台>` 提供，它们的脚本都可以跳过。DSDock 用 `allowBuilds` 明确放行和拒绝，加上 `strictDepBuilds: false`，安装退出码为 0 | Linux V2、第二波 | 【已核实】 |
| K27 | DSH 遇到不兼容的组合包时**跳过它**，stderr 打印 `dsh: skipping profile bundle "<包名>": <原因>`，**退出码仍为 0**。用户可以用 `dsh plugin allow-version` 给某个插件版本授予豁免 | 第二波实测、`dsh-app-boot` 源码 | 【已核实】 |
| K28 | 上游 tag 名是 `dsh-v<版本>`，新版本可能还没打 tag（rc.4 发布时最新 tag 是 rc.3） | GitHub | 【已核实】 |
| K26 | git 插件的放行 key 格式是 `<包名>@<规格>#<40 位 commit>`，和 commit 绑定；pnpm 打印的 key 会被终端换行截断；安装失败时 profile 不变 | Linux V14 | 【已核实】 |

---

## 3. 功能

优先级：**P0** 是 MVP 必做，**P1** 是第二版，**P2** 是以后。

### F1 总览（P0）

- 自动发现：dsh 可执行文件和版本、`DSH_HOME`、所有 profile、正在运行的 DSH 进程。
- 每个 profile 一张卡片：组合包（按叠加顺序）、版本、来源（npm、git、本地链接）、是否有更新、dshfind 评分和风险标记、锁定的 DSH 版本。
- 健康状态：后台对每个 profile 跑 `--dump-config`，显示 🟢 正常 / 🟡 有警告 / 🔴 无法组装。
- 托盘：有更新时显示角标；一键启动某个 profile（`dsh web` 等）；显示正在运行的实例。

### F2 安全安装（P0）

流程：**检查 → 快照 → 执行 → 验证 → 失败自动回滚**（见 §4.4 事务）。

装前检查规则（每条规则有一个编号，方便测试和文档引用）：

| 编号 | 规则 | 级别 |
|---|---|---|
| PF01 | 包里声明了 `dsh.bundle.patch`，并且路径安全（相对路径、不往上跳出目录） | 不通过就拒绝 |
| PF02 | `engines.dsh` 或 peer 依赖兼容目标 DSH 版本 | 不兼容就拒绝，并推荐一个兼容的版本 |
| PF03 | 还没装过（装过就改走"更新"流程） | 改走更新 |
| PF04 | 有没有生命周期脚本（preinstall、install、postinstall、prepare） | 警告，并展示脚本内容 |
| PF05 | 从 git 安装且需要放行构建（K13） | 单独弹确认框，展示包名和 commit |
| PF06 | dshfind 标记为有风险（`is_risky`、`risk_note`） | 红色警告 |
| PF07 | 仓库已归档，或超过 180 天没发版 | 提示 |
| PF08 | `engines.node` 和本机（或托管的）Node 版本 | 不兼容就拒绝 |
| PF09 | 来源可信度：npm 包是否回链到 GitHub 仓库（复用 dshfind 的 verified 结论） | 提示 |
| PF10 | 新包插入的 id 和 profile 里已有的行重复（K19：DSH 会静默丢弃前一个插件） | 警告，说明哪个插件会失效 |
| PF11 | 插件依赖 web 服务（比如 patch 里的行需要 `webServer`），但目标 profile 没有 `dsh-web-app`（K22） | 警告 |

执行后验证分两级：

- **第一级（每次都做，约 150ms）**：`--dump-config` 退出码为 0；出现了新包的那一层；stderr 里没有新增的 `patch: entry "…" not found`。
- **第二级（可选，默认对"救援"和"批量更新"开启）**：用临时端口启动 profile。web profile 等就绪信号（K21），其它 profile 等到一段时间没有新输出；解析 stderr 里的 `did not activate` 列表（K20）。超时 20 秒，结束时杀掉整个进程组。新出现的未激活插件视为失败。

### F3 卸载（P0）

快照 → `dsh plugin --profile X remove <pkg>` → 验证。卸载前额外检查：用户的 `cordis.patch.yml` 里是否还有指向这个插件的行（K7）。有的话提示用户，避免留下"找不到目标"的警告。

**禁用而不卸载（P0）**：在用户层写 `disabled: true`，语义照搬官方插件管理器：只改最后一条匹配的覆盖；没有就追加一条；只剩 `id` 和 `disabled` 并且和默认状态一致的覆盖直接删掉。写入时必须正确处理默认的 `[]`（K23），写入前用 `--patch` 加 dump 先验证一遍。

### F4 快照与回滚（P0）

- **什么时候拍**：每次安装、卸载、更新之前；切换 DSH 版本之前；救援之前；用户手动拍。
- **拍什么**：profile 的 `package.json`、`pnpm-lock.yaml`、`pnpm-workspace.yaml`、`cordis.patch.yml`，以及 `$DSH_HOME/cordis.patch.yml`。另外记录当时的 DSH 版本和锁定信息。**不拷** `node_modules`、`cordis.yml`、`.plugin-manager/`；**绝对不碰** `$DSH_HOME/.credentials.yaml`。实测一个快照约 32KB。
- **回滚**：原子地写回文件 → `dsh plugin --profile X install --frozen-lockfile --offline`（失败再去掉 `--offline`）→ `--dump-config` 验证。Linux 实测约 380ms，回滚后 dump 逐字节一致；即使删掉了整个 `node_modules` 也一样（V5）。
- **时间线界面**：每个快照显示时间、原因、DSH 版本、是否验证通过，以及和现在的差异（多了、少了、版本变了的包）。
- **P1**：可选把"最后一次验证通过"时的插件包文件存一份，防止作者从 npm 撤回版本后无法回滚。
- **清理策略**：每个 profile 默认保留最近 50 个快照，加上所有手动快照，加上每天最后一个验证通过的快照，保留 30 天。

### F5 更新（P0）

- 检查每个组合包有没有新版本，**只推荐兼容当前 DSH 的版本**；预发布版本需要单独打开开关。
- 单个更新：快照 → `dsh plugin --profile X add <包名>@<新版本>` → 验证。CLI 方式下这样就能直接覆盖，组合包顺序不变（V6）。**不要先删再装**，那样会把它移到列表末尾，改变叠加顺序。
- 批量更新时**逐个处理**：某一个失败只回滚那一个，最后出一份报告。
- 显示更新说明（GitHub Release，或仓库里的 CHANGELOG）。

### F6 救援模式（P0，主打功能）

1. **诊断**：按顺序检查三类问题，把"哪个插件、哪一层、什么错误"翻译成人话：
   - 配置文件解析失败（K23，最常见的是手改 `cordis.patch.yml` 写坏了 YAML）：直接指出文件和行号，提供"回到上次正常的用户层"；
   - `--dump-config` 失败或有 `not found` 警告；
   - 第二级冒烟测试里出现的 `did not activate` 列表（K20），以及 `$DSH_HOME/logs/startup-*.log`（只有必需插件失败时才有）。
2. **一键回到上次正常的状态**：找到最近一个验证通过的快照，回滚。
3. **二分定位**（P1）：复制一个临时 profile（`.dsdock-bisect-<id>`），每轮去掉一半插件后做冒烟测试，几轮就能找到是哪个插件出的问题。**用户原来的 profile 全程不动。**冒烟测试的具体做法【待实测 V8】。
4. **最后手段**：先给整个 profile 拍快照，再用 `--from-default-profile` 重置（K14）。

### F7 冲突与覆盖分析（P1，差异化卖点）

- **插件之间的冲突**：两个组合包往同一个 id 插入了行（K19，实测会静默丢掉前一个插件，**这部分提前到 P0**，作为 PF10 和总览里的红色提示）；或者后装的包整行覆盖了前面某个插件的行（官方 base 层的正常覆盖不算）。
- **过时的覆盖**：用户在 `cordis.patch.yml` 里整块覆盖了某个插件的 `config`。插件更新后配置结构变了（用 `--dump-config-schema` 对比更新前后），新增的字段就丢了。提示用户检查。
- **层叠视图**：每一行配置最终来自哪一层，被哪几层改过（直接用 K8 输出里的来源注释）。

### F8 整合包 Dockfile（P1）

- 导出：一个 profile 的组合包（顺序、精确版本、来源）、建议的 DSH 版本，用户层 patch 可选（带上时自动脱敏）。**永远不包含** `$DSH_HOME/.credentials.yaml` 和 `.env`。
- 导入：新建一个 profile，逐个走 F2 的流程，结束后出一份报告。
- 分享：dshfind 托管 Dockfile，用深链 `dsdock://import?url=<https-url>` 一键导入。

格式草案：

```json
{
  "dockfile": 1,
  "name": "我的写作套装",
  "createdBy": "dsdock 0.1.0",
  "dsh": { "version": "0.2.1-rc.4", "channel": "next" },
  "bundles": [
    { "name": "dsh-meme", "spec": "dsh-meme@0.1.62", "source": "npm" },
    { "name": "@team/dsh-bar", "spec": "git+https://github.com/team/x.git#<40位commit>&path:/packages/bar", "source": "git" }
  ],
  "userPatch": { "included": true, "redacted": ["config.apiKey"], "content": "- override: …" }
}
```

### F9 市场数据源（P0 只读，P1 完整）

- 默认用 dshfind 的 `/market/v1/plugins`（分页、带 revision，可以增量同步），再叠加 `/v1/catalog` 里的评分和风险数据。
- 支持添加**官方 `format: 1` 索引**（K17），企业或团队可以用自己的索引，和 DSH 内置"发现"标签同一个格式。
- 浏览界面保持简单，重点放在"装到哪个 profile"和检查结果。

### F10 多机同步（P2）

把 Dockfile、profile 的用户层、锁定信息同步到 git 仓库或 WebDAV，新机器上一键还原。

### F11 DSH 版本管理（P0 到 P1）

| 子功能 | 优先级 | 说明 |
|---|---|---|
| 版本列表 | P0 | 已安装的版本（渠道、安装时间、占用空间、哪些 profile 在用）；可安装的版本（按稳定、测试、尝鲜分组，标出推荐版本） |
| 安装、卸载 | P0 | 每个版本装在独立目录里，**用 pnpm 安装**以共享文件（K25：两个版本 548MB，npm 要 1.7GB）；放行 DSH 官方的构建脚本清单；装好后在临时 profile 里做一次预热启动（V10 出现过首次启动卡顿）。有 profile 锁定在某个版本时，不允许卸载它 |
| 全局切换 | P0 | 把默认的 `dsh` 指向另一个版本（通过转发器，见 §4.6） |
| 接管现有安装 | P0 | 检测到 `npm i -g` 装过的 DSH，登记为"外部版本"，可以直接选用，不删除、不移动 |
| 切换前预检 | P1 | 插件兼容性（K10）、降级时的会话格式警告（K15）、两个版本之间的升级指南（K16）、对受影响的 profile 逐个跑 `--dump-config` |
| 一键回退 | P1 | 切换前拍快照；旧版本还在本地，秒级回退 |
| 按 profile 锁定版本 | P1 | 比如 `work` 固定在 rc.2，`play` 跟 alpha。不同 profile 可以同时用不同版本运行（K9） |
| 升级指南影响分析 | P2 | 优先显示和已装插件相关的条目 |
| 托管 Node 和 pnpm | P2 | 用户机器上什么都不用装，实现"零环境配置" |

降级警告的文案：

> 目标版本使用会话格式 v{旧}，当前版本是 v{新}。你在新版本里创建或继续过的会话，旧版本打不开。更早的会话还保留着旧格式的原文件，可以看，但新版本里聊过的部分看不到。

---

## 4. 技术架构

### 4.1 技术栈

| 层 | 选型 | 理由 |
|---|---|---|
| 核心 | Go 1.26，单二进制 | 和 Magpie 一样，跨平台分发最简单 |
| 桌面外壳 | Wails v3（beta） | 托盘加窗口；Mac 用 WebKit，Win 用 WebView2。Magpie 已经在生产环境用它 |
| 前端 | 原生 JS + CSS，用 `embed` 打包进二进制 | 不用框架、没有构建链，改完即用 |
| 存储 | `modernc.org/sqlite` | 纯 Go，交叉编译不依赖 C |
| YAML | `gopkg.in/yaml.v3`（Node API） | 保留注释，用来读写 patch |
| JSON | `tidwall/gjson` + `sjson` | 只改要改的字段，保留顺序和格式 |
| 监听文件 | `fsnotify` | 用户手动改了 profile 也能立刻刷新 |
| 终端界面 | bubbletea + lipgloss（P2） | 和 Magpie 一样 |
| 和 JS 通信 | 内嵌一个 `bridge.mjs`，用**目标版本 DSH 对应的 Node** 运行 | 远程客户端是 JS 包；npm 的 semver 规则用 Go 模拟容易出错 |

### 4.1.1 图形界面的落地方式（M2 实际做法）

先做**本机 Web 界面**（`dsdock gui`：Go 内置 HTTP 服务 + 原生 JS 页面，资源打包进二进制），再用 Wails 套原生窗口和托盘，加载同一套页面。原因：Wails 在 macOS 上需要 WebKit 和 CGO，界面效果也必须在 Mac 上看，开发机（Linux）验证不了；Web 界面在任何平台都能开发和测试（`scripts/gui-shots.mjs` 用无头 Chromium 驱动页面并截图）。Magpie 也同时提供 Web UI 模式。

安全：只监听 127.0.0.1；每次运行生成随机令牌，放在 URL 的 `#` 片段里交给页面（不会进入请求和日志）；所有 API 校验令牌、Host（防 DNS 重绑定）和写请求的 Origin；CSP 禁止内联脚本和样式，第三方内容（市场里的描述）一律用 `textContent` 渲染。

### 4.2 进程模式

同一个二进制，按参数或者被调用时的名字进入不同模式：

| 调用方式 | 模式 |
|---|---|
| `dsdock`（无参数，GUI 环境下） | GUI：托盘加主窗口 |
| `dsdock <子命令>` | CLI |
| `dsdock daemon` | 后台：定时检查更新、监听文件、刷新健康状态 |
| `dsdock tui` | 终端界面（P2） |
| 以 `dsh` 或 `dsh.exe` 的名字被调用 | **转发器**（§4.6） |

GUI 和 CLI 共用同一个核心库。同一时刻只运行一个 GUI 实例，CLI 通过本地 socket（Windows 上是命名管道）通知 GUI 刷新。

### 4.3 目录结构

```
dsdock/
├── main.go
├── internal/
│   ├── dshenv/      # 找到 dsh、版本、DSH_HOME、Node、pnpm；扫描正在运行的 DSH 进程
│   ├── versions/    # 版本管理：安装、卸载、列表、渠道、接管外部安装
│   ├── shim/        # 转发器：解析该用哪个版本、转发参数和信号
│   ├── profile/     # 读 profile：manifest、bundles、锁文件、patch
│   ├── layer/       # 解析 --dump-config 的输出，算出层叠视图
│   ├── preflight/   # 检查规则，每条规则 PFxx 一个文件，各自带测试
│   ├── snapshot/    # 内容寻址的文件存储，加元数据
│   ├── executor/    # cli.go：调 dsh plugin；live.go：通过 bridge 热安装
│   ├── txn/         # 事务：快照 → 执行 → 验证 → 回滚
│   ├── rescue/      # 诊断、回到上次正常、二分定位
│   ├── conflict/    # 冲突分析、过时覆盖检测
│   ├── catalog/     # dshfind 数据源和 format-1 索引，带缓存和 ETag
│   ├── dockfile/    # 导入导出、脱敏
│   ├── bridge/      # bridge.mjs（内嵌）和 Go 这一侧的 JSON-RPC 客户端
│   ├── store/       # sqlite 结构和迁移
│   ├── update/      # DSDock 自己的自动更新：渠道、镜像、代理
│   ├── gui/         # Wails 应用、托盘、assets/
│   └── cli/
├── docs/
│   ├── design.zh-CN.md   # 本文
│   ├── m0-handoff.md     # M0 实测接力手册
│   ├── m0-results/       # 各平台的实测结果
│   └── subsystems/       # 每个子系统一份设计文档
├── LESSONS.md
└── site/                 # Cloudflare Worker：官网、install.sh、install.ps1
```

### 4.4 事务（txn）：所有变更的唯一入口

```
Begin(profile, op, reason)
 1. 拿 profile 的文件锁（<数据目录>/locks/<home哈希>-<profile>.lock）
 2. dshenv.Running(profile)
      ├─ 没在运行 → 直接执行
      └─ 正在运行 → Linux / macOS：照常执行，结束后提示"重启 profile 才会生效"（K11，V7）
                   Windows：取决于 V7 的实测结果，可能必须先停掉 DSH
         （没有外部热安装接口，见 K12；executor.live 推迟到官方开放接口之后）
 3. preflight.Run(op)        → 有拒绝级别的问题就终止；有警告就请用户确认
 4. snapshot.Take()          → snapID
 5. executor.Run(op)         → 实时输出日志，并写入 ops 表
 6. verify(profile)          → --dump-config：退出码，加上预期的层是否出现
 7a. 通过 → 把快照标成 verified，提交操作日志
 7b. 失败 → snapshot.Restore(snapID) → 再验证一次 → 报告（带完整日志）
```

**不变式**：只要是会改动 profile 的操作，不经过 txn 就不允许执行。代码审查和测试都以这一条为准。

### 4.5 快照存储

```
<数据目录>/blobs/<sha256前2位>/<sha256>      # 文件内容，按内容去重
<数据目录>/dsdock.db
```

sqlite 表结构草案：

```sql
CREATE TABLE snapshots (
  id TEXT PRIMARY KEY, dsh_home TEXT, profile TEXT, reason TEXT,
  created_at INTEGER, dsh_version TEXT, verified INTEGER, manual INTEGER, parent TEXT
);
CREATE TABLE snapshot_files (snapshot_id TEXT, relpath TEXT, blob TEXT, mode INTEGER,
  PRIMARY KEY (snapshot_id, relpath));
CREATE TABLE ops (id TEXT PRIMARY KEY, kind TEXT, dsh_home TEXT, profile TEXT, spec TEXT,
  status TEXT, snapshot_id TEXT, log_blob TEXT, started_at INTEGER, ended_at INTEGER);
CREATE TABLE versions (version TEXT PRIMARY KEY, path TEXT, source TEXT /* managed|external */,
  node TEXT, session_format INTEGER, installed_at INTEGER);
CREATE TABLE pins (dsh_home TEXT, profile TEXT, version TEXT, PRIMARY KEY (dsh_home, profile));
CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT);
```

恢复时：把每个文件先写到 `<文件>.dsdock-tmp`，再 rename 覆盖；快照里没有、但当前目录里有的受管文件要删掉；最后按锁文件重装。

### 4.6 版本管理和转发器

**目录布局：**

```
<数据目录>/versions/<版本号>/          # 用 pnpm add @deepseek-ai/dsh@<版本号>（node-linker=hoisted）装到这里，共享 pnpm 全局缓存
<数据目录>/runtimes/node-<版本号>/      # P2：托管的 Node
<数据目录>/bin/dsh                    # Mac：指向 dsdock 二进制的硬链接或复制品
<数据目录>/bin/dsh.exe                # Windows：同上。用 .exe 而不用 .cmd，避免 Ctrl+C 时弹出"终止批处理操作吗"
```

**转发器的实现（第二波已完成）：** Unix 上是一个两行的 shell 脚本 `exec '<dsdock 路径>' __shim "$@"`，这样升级 dsdock 后转发器不会过时，而且每一步都是 exec，进程号不变（实测 SIGINT 能直接送到 DSH）。Windows 上是 `dsdock.exe` 的副本 `dsh.exe`，按调用时的名字进入转发模式。`dsdock dsh shim` 负责写入，只打印 PATH 的设置方法，不修改用户的 shell 配置。

**转发器决定用哪个版本的顺序：**

1. 环境变量 `DSDOCK_DSH_VERSION`（临时指定）；
2. 参数里指定的 profile 有锁定版本。解析规则必须和 dsh 自己一致：`dsh <name>` 简写、`--profile <name>`、`dsh plugin --profile <name>`；
3. 全局默认版本；
4. 外部安装（`npm i -g` 装的那个）；
5. 都没有 → 提示用 `dsdock dsh install` 安装。

**执行：** `<node> <版本目录>/node_modules/@deepseek-ai/dsh/lib/bin.js <原样转发的参数>`。转发器自己**不输出任何内容**，以免影响脚本解析 dsh 的输出。

- Unix：用 `syscall.Exec` **替换自身**，进程号不变，信号天然正确（V12：SIGINT 后 256ms 干净退出）。**不能**用"启动子进程再等待"的方式，Linux 实测这样会留下孤儿 Node 进程；
- Windows：没有 exec，只能启动子进程，然后转发 Ctrl+C 事件、等待并返回退出码，用 Job Object 保证转发器退出时子进程一起结束；
- DSDock 自己启动 DSH 时（托盘、冒烟测试），放进新的进程组（Windows 上是 Job Object），停止时对整个进程组发信号。

**Node 版本**：发布的包没有 `engines`（K24），所以版本要求从对应 tag 的源码 `apps/cli/package.json` 读取并缓存。运行前由转发器检查，不满足时给出友好提示（而不是 Node 18 那种看不懂的 import 错误）。

**PATH：** 安装时把 `<数据目录>/bin` 加到最前面。Mac 写 `~/.zprofile` 或 `~/.zshrc`（选择哪个看 V12 的实测结果）；Windows 写 `HKCU\Environment\Path`，然后广播 `WM_SETTINGCHANGE`。卸载时还原。

**读取版本元数据**（通过 bridge，或者静态读取构建产物）：

- 会话格式版本：`@deepseek-ai/dsh-session` 导出的 `SESSION_FORMAT_VERSION`；
- 需要的 Node 版本：包的 `engines.node`；
- 升级指南：从 GitHub 拉对应 tag 下的 `docs/upgrade-guide/`，缓存在本地。

### 4.7 bridge

- 内嵌在二进制里的 `bridge.mjs`，运行时释放到 `<数据目录>/bridge/<DSDock版本>/`，用**选中的那个 DSH 版本对应的 Node** 运行。
- 通信：stdio 上传按行分隔的 JSON-RPC。
- 方法（初步）：
  - `semver.satisfies(version, range, {includePrerelease})`
  - `dsh.meta(versionDir)` → 会话格式版本、内置组合包列表
  - ~~`live.*` 热安装~~：V9 确认没有外部接口，推迟
- 安全：bridge 只从 DSH 版本目录里加载模块，不加载 profile 里的插件代码。

### 4.8 发现正在运行的 DSH

扫描进程列表，找命令行里有 `@deepseek-ai/dsh/lib/bin.js` 的 Node 进程，解析出 profile 和 `DSH_HOME`（从进程环境变量里读，读不到就用默认值）。Mac 和 Windows 各写一份实现。

### 4.9 冲突分析的实现

- 主要依据 `--dump-config` 输出里的来源注释（K8）：每一行最终来自哪个文件、被哪些层改过；
- 备用方案：自己按 K6 的顺序解析每个组合包的 patch 文件（路径是 `node_modules/<包名>/` 加上 `dsh.bundle.patch`）；
- 过时覆盖检测：更新前后各跑一次 `--dump-config-schema`，对比被用户层整块覆盖的那些行的 `configRef`，看新增了哪些字段、哪些字段变成必填。

### 4.10 数据目录

| 平台 | 路径 |
|---|---|
| macOS | `~/Library/Application Support/DSDock/` |
| Windows | `%APPDATA%\DSDock\` |
| Linux（开发和 CI 用） | `$XDG_DATA_HOME/dsdock/` |

可以用 `DSDOCK_HOME` 覆盖，测试时用来隔离。

### 4.11 安全原则

- 默认**不自动放行**任何构建脚本，git 安装必须经过用户确认（PF05）；
- Dockfile 导出时脱敏，导入时展示每个包的来源；
- 只接受 https 的索引源（和官方规则一致）；
- 救援、回滚之前一定先拍快照；不可逆的操作（`--from-default-profile`、卸载 DSH 版本）需要二次确认；
- 日志可能包含凭据（K3 的启动日志不脱敏），分享诊断报告前先用正则做一遍脱敏。

### 4.12 国际化

先做中文和英文，用 `i18n.js` 键值表（Magpie 的做法）。

---

## 5. CLI 参考

```
dsdock status [--profile X] [--json]
dsdock check <spec> [--profile X]               # 只做装前检查
dsdock add <spec> --profile X [--yes] [--dry-run]
dsdock remove <pkg> --profile X
dsdock update [<pkg>|--all] --profile X [--pre]
dsdock snap list|take|diff <id>|restore <id> --profile X
dsdock rescue --profile X [--auto]
dsdock conflicts --profile X                    # P1
dsdock export --profile X -o file.dockfile.json # P1
dsdock import <file|url> [--as <新profile名>]    # P1

dsdock dsh list [--available] [--channel latest|next|alpha]
dsdock dsh install <版本|latest|next|alpha>
dsdock dsh use <版本>
dsdock dsh pin <版本> --profile X | --unpin
dsdock dsh diff <起始版本> <目标版本>             # P1
dsdock dsh uninstall <版本>
dsdock dsh adopt                                # 接管外部安装

dsdock gui | daemon | tui
dsdock self update [--channel beta]
```

所有命令都支持 `--json`，方便 GUI、脚本，以及 agent 调用。

---

## 6. GUI 信息架构

**托盘菜单：** 当前默认的 DSH 版本 · 每个 profile 一项（启动、停止、打开） · "有 N 个更新" · 打开主窗口 · 退出

**主窗口左侧导航：**

| 页面 | 内容 |
|---|---|
| 总览 | 所有 profile 的卡片和健康状态、正在运行的实例、待处理的更新 |
| Profile 详情 | 组合包列表（可以拖动调整顺序，P2）、层叠视图（P1）、快照时间线、锁定的版本 |
| 版本 | 已安装和可安装的 DSH 版本、切换、预检报告 |
| 市场 | 搜索、检查结果、"安装到哪个 profile" |
| 救援 | 诊断结果、一键回到上次正常、二分定位进度 |
| 操作记录 | 所有事务的日志 |
| 设置 | 数据源、镜像、代理、更新渠道、语言 |

---

## 7. 和 dshfind 的集成

| 方向 | 内容 |
|---|---|
| dshfind → DSDock | `/market/v1/plugins`（目录）、`/v1/catalog`（评分、风险、verified 安装方式） |
| DSDock → 用户 | dshfind 插件详情页加"用 DSDock 安装"按钮，对应深链 `dsdock://add?spec=<npm规格>&source=dshfind` |
| 整合包 | dshfind 托管 Dockfile，提供 `dsdock://import?url=…` 深链 |
| 标准格式 | dshfind 同时输出一份官方 `format: 1` 索引，让 DSH 内置的"发现"标签也能直接用 |

DSDock **不上报任何遥测**。下载量统计只在 dshfind 的服务端看深链和 API 的访问量。

---

## 8. 发布与运维

- 推 tag 后交给**单独的 `dsdock-releases` 仓库**构建、签名、发布，签名密钥只放在那个仓库里。
- Mac：Developer ID 签名 + 公证，arm64 和 amd64 各一份 dmg 和 zip。
- Windows：Azure Trusted Signing 签名，amd64 和 arm64 的安装包和便携版。
- 官网和安装脚本放在 Cloudflare Worker 上（`install.sh`、`install.ps1`），国内镜像放 R2，脚本支持 `--mirror` 和 `--proxy`。
- 自动更新有 stable 和 beta 两个渠道，更新包要校验 SHA256SUMS。
- CI：
  - 单元测试：Linux、Mac、Windows 三平台都跑；
  - 集成测试：用真实的 DSH 版本（latest 和 next），配合 `test/fixtures` 里的测试插件跑 txn、快照、回滚的端到端流程，所有测试都在隔离的 `DSH_HOME` 里进行。

---

## 9. 工程规范

- 每个子系统一份设计文档（`docs/subsystems/<名字>.md`），改代码之前先读。
- `LESSONS.md`：每踩一次坑，记一条规则。
- 测试用的插件样本放在 `test/fixtures/bundles/`：`fx-ok`、`fx-broken`、`fx-incompat`、`fx-conflict-a`、`fx-conflict-b`，和 M0 手册里的定义一致。
- 所有改动 profile 的代码路径必须经过 txn（§4.4 的不变式）。
- 测试**禁止**访问真实的 `~/.dsh`，测试框架强制设置 `DSH_HOME` 和 `DSDOCK_HOME`。

---

## 10. 路线图

| 阶段 | 时间 | 内容 | 完成标准 |
|---|---|---|---|
| **M0 · 验证假设** | 1 周 | 按 [m0-handoff.md](./m0-handoff.md) 在 Linux、Mac、Windows 上逐项实测；搭好仓库骨架和三平台 CI | 每个【待实测】项都有结论；设计文档相应更新 |
| **M1 · CLI 核心**（已完成。第一波：status/check/add/remove/update/disable/enable/snap/smoke/log；第二波：dsh list/install/use/pin/unpin/which/adopt/uninstall/shim、转发器、PF06/PF07） | 3 周 | dshenv、profile、snapshot、txn、preflight、executor(cli)；`status/check/add/remove/update/snap`；版本目录和转发器；`dsh list/install/use/adopt` | 自己日常用 DSDock；故意装坏插件后能自动回滚 |
| **M2 · 救援 + GUI**（M2a 已完成：`dsdock rescue`、`dsdock gui` 浏览器界面、`internal/core` 重构；M2b：Wails 原生窗口和托盘，需要在 Mac 上验证） | 3 周 | F6 的诊断和回到上次正常；Wails 托盘和主窗口（总览、Profile、版本、操作记录）；dshfind 数据源；切换版本前预检、一键回退 | 发布已签名的 Mac dmg |
| **M3 · Windows + 多版本** | 2 周 | 托盘里一键重启 profile；按 profile 锁定版本；会话格式降级警告；Windows 打包、签名、PATH、Job Object | 发布 Windows 安装包 |
| **M4 · 分发** | 2 周 | 自动更新；安装脚本和镜像；dshfind 按钮和深链；Dockfile 导入导出 | 新用户从 dshfind 点一下就能装 |
| **M5 · 差异化** | 持续 | 冲突分析；二分定位；升级指南影响分析；托管 Node 和 pnpm；多机同步；TUI | — |

---

## 11. 风险

| 风险 | 应对 |
|---|---|
| DSH 还在 0.x，CLI 和输出格式变化快 | 用版本矩阵做集成测试；解析输出时宽进严出；关键能力准备两条路径（CLI 和 bridge） |
| 官方以后自己做了快照和回滚 | DSDock 的核心价值是"在 DSH 外面"，包括救援、版本管理和跨 profile；同时持续跟踪上游动向，及时调整 |
| Windows 上 DSH 运行时文件被占用 | 可能需要先停掉 profile，或者只能走热安装（V7） |
| Wails v3 还在 beta | 把 GUI 层做薄，核心逻辑全部放在 CLI 里，必要时可以换掉外壳 |
| npm 版本被作者撤回，导致无法回滚 | F4 的 P1 功能：保留最后一次验证通过时的插件包文件 |
| 和 DSH-X 等竞品同质化 | 主打事务式变更、救援、冲突分析，加上 dshfind 的数据和分发入口 |

---

## 12. 待确认项

全部列在 [m0-handoff.md](./m0-handoff.md) 的 V1 到 V14 中，结果写入 `docs/m0-results/<平台>.md`，然后回来更新本文 §2 的"状态"一列。

进度（2026-10-27）：Linux 已完成，见 [m0-results/linux.md](./m0-results/linux.md)；K19 到 K26 是 Linux 实测新增的事实。macOS 和 Windows 待执行。
