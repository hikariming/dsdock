# M0 实测结果：Linux

- 执行者：Claude Code（Opus 5.5），2026-10-27
- 系统：Debian 12，Linux 6.1.0-35-amd64，x86_64
- 系统自带 Node：/usr/bin/node v18.20.4；~/.local/opt/node v22.23.3。**都不满足 ≥ 24.9**，所以按手册 2.1 用了工作区里的便携 Node v24.22.1（nodejs.org 官方包，SHA256 校验通过）
- pnpm：系统里没有。用便携 Node 自带的 corepack 装到工作区，版本 12.15.0（corepack 拿到的最新版）
- npm：便携 Node 自带的 11.19.1
- 机器上原有 DSH：没有全局安装，没有桌面版；有一个真实的 `~/.dsh`（7 个文件）。**全程没有读写它**，测试前后文件数和最新修改时间都没变
- shell：bash
- DSH 版本：0.2.1-rc.2（`latest`）、0.2.1-rc.4（`next`）

## 总览

| 项 | 状态 | 一句话结论 |
|---|---|---|
| V1 DSH_HOME 隔离 | ✅ | 环境变量有效，真实目录没有任何变化 |
| V2 版本隔离、Node、pnpm | ✅ | 多版本可以并存；**用 npm 每个版本 856MB，用 pnpm 两个版本一共 548MB**；发布的包没有声明 `engines` |
| V3 profile 结构 | ✅ | 比文档多出 `.plugin-manager/logs/` 和启动时生成的 `cordis.yml` |
| V4 --dump-config 格式 | ✅ | 层标记和覆盖来源都能解析；**同一个 id 插入两次时没有任何警告** |
| V5 快照和回滚 | ✅ | 快照 32KB，离线回滚约 380ms，回滚后输出逐字节一致 |
| V6 更新语义 | ✅ | CLI 方式下直接 add 新版本、`update --latest` 都可以，顺序不变 |
| V7 运行中修改 | ✅（Linux） | CLI 增删不报错，重启后生效；`cordis.patch.yml` 的有效修改会热重载，无效修改被静默忽略 |
| V8 冒烟测试 | ⚠️ | **加载失败的插件不会让 DSH 退出**，必须解析 stderr 的警告；重复 id 时前一个插件被静默丢弃 |
| V9 Host API | ❌ | 没有面向外部程序的公开接口，热安装推迟 |
| V10 多版本共用 DSH_HOME | ⚠️ | 两个版本可以同时运行；新装版本第一次启动时出现过一次 30 秒无输出，没能复现 |
| V11 会话格式降级 | ⏸ | 需要 API key，没有授权。已知 rc.2 和 rc.4 的会话格式都是 v4 |
| V12 转发器和 PATH | ✅ | `exec` 方式的转发器能正确传递 SIGINT；**非 exec 的包装会留下孤儿进程**（实际踩到了） |
| V13 官方桌面版 | ⏸ | Linux 上不适用 |
| V14 git 安装放行构建 | ✅ | 放行 key 格式确认；失败时 profile 不变；key 和 commit 绑定 |

---

### V1 DSH_HOME 隔离

- 状态：✅
- 命令：`DSH_HOME=$W/home dsh4 --profile t1 --dump-config`
- 关键输出：
  ```
  Error: dsh: profile "t1" does not exist; create it with 'dsh plugin --profile t1 add <package>'
  ```
- 结论：`DSH_HOME` 环境变量有效。只有工作区出现了文件，真实的 `~/.dsh` 文件数（7）和最新修改时间测试前后完全一致。
- **和文档不一致**：文档说"dump 会初始化缺失的 profile 文件"，实际上对一个**不存在**的自定义 profile 跑 `--dump-config` 会直接报错（耗时约 200ms，退出码 1），必须先用 `dsh plugin --profile X add` 创建。
- 对设计的影响：
  - `DSDOCK_HOME` 加 `DSH_HOME` 的隔离测试方案可行；
  - DSDock 的"新建 profile"不能靠 dump 来创建，只能用 `dsh plugin add` 或 `--from-default-profile`。

### V2 版本隔离和运行环境

- 状态：✅
- 两个版本分别用 `npm install --prefix` 装到独立目录，`-V` 分别输出 `0.2.1-rc.2` 和 `0.2.1-rc.4`。
- **占用空间**：

  | 安装方式 | rc.2 | rc.4 | 两个合计 |
  |---|---|---|---|
  | `npm install --prefix`（每个版本独立） | 856MB（178 秒，冷缓存） | 856MB（47 秒） | **1.7GB** |
  | `pnpm add`，`node-linker=hoisted`（全局缓存 + 硬链接） | 521MB（55 秒） | 28MB（15 秒） | **548MB** |

  大头是 `@deepseek-ai/*`（566MB）。用 pnpm 装的版本 `-V` 和 `--dump-config` 都正常。
- **构建脚本被拦截**：
  - npm 11 拦了 5 个包的安装脚本：`@deepseek-ai/dsh-subprocess-local`（postinstall：`ensure-spawn-helper.mjs`）、`node-pty`（`node-gyp rebuild`）、`koffi`、`protobufjs`、`@google/genai`（空操作）；
  - pnpm 12 拦了以后**直接返回退出码 1**（`ERR_PNPM_IGNORED_BUILDS`），但文件其实都装好了；
  - 没放行的情况下，启动和组装都正常。**会不会影响 agent 执行命令（子进程辅助程序、终端）还没测**，需要一个真实会话才能确认。
- **会话格式**：rc.2 和 rc.4 的 `SESSION_FORMAT_VERSION` 都是 **4**（在 `@deepseek-ai/dsh-session/lib` 里）。
- **Node 版本要求**：npm 上发布的 `@deepseek-ai/dsh` **没有 `engines` 字段**（源码仓库 `apps/cli/package.json` 里写的是 `>=24.9.0`）。实测：
  - Node 18：报一个很难看懂的错误 `import { inspect, parseEnv } from "node:util"`；
  - Node 22：`--dump-config` 能运行（没有测完整启动）。
- 对设计的影响：
  - **版本目录改用 pnpm 安装**（§4.6）：省三分之二的空间，后续版本几乎不占额外空间；
  - 安装 DSH 版本时要放行一份"DSH 官方构建脚本"清单（至少包括 `dsh-subprocess-local`、`node-pty`、`koffi`），并把 pnpm 的 `ERR_PNPM_IGNORED_BUILDS` 和真正的失败区分开；
  - Node 版本要求不能从发布的包里读，要从对应 tag 的源码 `apps/cli/package.json` 读，或者在 DSDock 里维护一张表；运行前由 DSDock 自己检查，给出友好提示。

### V3 profile 的完整结构

- 状态：✅
- 命令：`dsh4 plugin --profile t1 add $W/fixtures/fx-ok`（3 秒，输出 `dsh: initialized profile t1 at …`）
- `$DSH_HOME` 顶层：刚开始只有 `profiles/`；启动过 web 之后多了 `.anonymous-user-id`、`.credentials.yaml`（权限 600）、`storages/workspace.json`。
- profile 目录（不含 `node_modules`）：
  ```
  package.json
  pnpm-lock.yaml          # lockfileVersion '9.0'，一个本地依赖时 13 行
  pnpm-workspace.yaml
  cordis.patch.yml        # 默认内容是注释加上 []
  cordis.yml              # 启动时生成：空的根配置，注释写着"不要编辑这个文件"
  .plugin-manager/logs/operation-<随机>/pnpm.log   # 每次 dsh plugin 操作的 pnpm 日志
  ```
- `package.json`：
  ```json
  {
    "name": "dsh-profile-t1",
    "private": true,
    "dependencies": { "fx-ok": "link:/home/…/fixtures/fx-ok" },
    "dsh": { "profile": { "bundles": ["@deepseek-ai/dsh-base", "fx-ok"] } }
  }
  ```
  - 指定了版本号的 npm 包会记成精确版本（`"@nagi-ovo/dsh-ads": "0.1.2"`）；
  - 本地目录记成 `link:` 加绝对路径；
  - git 包记成 `git+file:///…`，**不带 commit**，commit 只锁在锁文件里。
- `pnpm-workspace.yaml`：
  ```yaml
  packages:
    - .

  nodeLinker: hoisted
  autoInstallPeers: false
  ```
- pnpm 全局缓存在 `~/.local/share/pnpm/store/v11`，**在 `DSH_HOME` 外面**；本地依赖在 `node_modules` 里是软链接。
- **profile 模板**：用 `dsh plugin --profile <任意名字>` 创建的 profile 只有 `dsh-base`，**没有 web 服务**。只有名字叫 `web` 等官方模板名，或者用 `--from-default-profile web` 创建的，才包含 `@deepseek-ai/dsh-web-app`。
- 对设计的影响：
  - 快照文件清单：`package.json`、`pnpm-lock.yaml`、`pnpm-workspace.yaml`、`cordis.patch.yml`。**不要**包含 `cordis.yml`、`.plugin-manager/`；
  - `$DSH_HOME/.credentials.yaml` 存的是凭据：**快照、整合包、多机同步都绝对不能带上它**；
  - "新建 profile"的界面要让用户选模板（只有 base / web / headless …），否则建出来的 profile 用不了 web 插件。

### V4 --dump-config 的输出格式

- 状态：✅
- 正常情况：退出码 0，耗时约 **134ms**，363 行。每一层开头有一行 `# == <包名>`：
  ```
  # == @deepseek-ai/dsh-base
  - id: tool-plugin-manager
  ...
  # == fx-ok
  - id: fx-ok
    name: fx-ok
  ```
- 被用户层改过的行，层标记后面会追加来源：
  ```
  # == fx-ok, patched by /home/…/profiles/t1/cordis.patch.yml
  - id: fx-ok
    name: fx-ok
    disabled: true
  ```
- **重复 id**：fx-conflict-a 和 fx-conflict-b 都插入 `fx-same`。dump 的退出码是 0，stderr 是空的，**两行都原样输出，没有任何警告**：
  ```
  # == fx-conflict-a
  - id: fx-same
    name: fx-conflict-a
  # == fx-conflict-b
  - id: fx-same
    name: fx-conflict-b
  ```
- **patch 语法更正**：没有 `override` 这个关键字。修改已有的行，直接写 `- id: <目标>` 加上 `config` 或 `disabled`；插入新行用 `- insert: [...]`。（手册里的写法是错的，已经更正）
- **目标找不到**：stderr 输出下面这行，**退出码仍然是 0**：
  ```
  dsh: [/home/…/profiles/t1/cordis.patch.yml] patch: entry "no-such-row" not found
  ```
- `--dump-config-schema`：退出码 0，约 600KB；根节点 `x-cordis` 下的键是 `profile`、`complete`、`entries`、`diagnostics`、`patchSchema`；`complete=true`，有 99 个 entries。
- 对设计的影响：
  - 层叠视图（F7）直接解析 `# == <包名>[, patched by <文件>]`；
  - **重复 id 检测提升为 P0**：DSH 自己完全不提示，而且启动时会静默丢掉前一个插件（见 V8）。装前检查新增一条规则 PF10；
  - 验证步骤不能只看退出码，还要扫描 stderr 里的 `patch: entry "…" not found`。

### V5 快照和回滚是否可行

- 状态：✅
- 步骤：t1 里装了 fx-ok 和 `@nagi-ovo/dsh-ads@0.1.2` → 复制四个文件作为快照 → 删掉 dsh-ads、加上 fx-conflict-a → 把四个文件复制回去 → `dsh4 plugin --profile t1 install --frozen-lockfile --offline`
- 结果：

  | 场景 | 退出码 | 耗时 |
  |---|---|---|
  | 快照大小 | — | **32KB**（对比 `node_modules` 24MB） |
  | 离线回滚（`node_modules` 还在） | 0 | **379ms** |
  | 删掉整个 `node_modules` 后离线回滚 | 0 | **386ms** |

  两种情况下，回滚后的 `--dump-config` 和快照时**逐字节一致**；中途多装的 fx-conflict-a 被清掉；`dsh.profile.bundles` 正确。
- 对设计的影响：§4.5 的快照方案成立。离线回滚依赖 pnpm 全局缓存，所以"清理 pnpm 缓存"会让回滚退化成联网下载，F4 里保留插件包文件的 P1 功能可以兜底。

### V6 更新的语义（CLI 方式，DSH 没在运行）

- 状态：✅
- `dsh4 plugin --profile t1 add @nagi-ovo/dsh-ads@0.1.3`（已经装了 0.1.2）：**直接成功**，输出 `- 0.1.2` / `+ 0.1.3`，用时 11.9 秒；
- `dsh4 plugin --profile t1 update @nagi-ovo/dsh-ads --latest`：成功，用时 115ms（缓存命中）；
- 两种方式下，`dsh.profile.bundles` 的顺序**都不变**。
- 结论：`already-installed` 报错只出现在热重载模式下的 Host API 里。CLI 方式直接 add 新版本就是最简单的更新。
- 对设计的影响：F5 的更新改为 `dsh plugin add <包名>@<新版本>`，不用先删再装（先删再装会改变组合包的顺序）。

### V7 DSH 正在运行时，用 CLI 修改 profile

- 状态：✅（只代表 Linux；Windows 上文件占用是重点）
- web profile 运行时 add fx-conflict-a、remove fx-ok：**都成功**（191ms 和 64ms），没有文件占用错误；运行中的 DSH **没有任何输出**。
- 重启后：fx-conflict-a 加载了，fx-ok 没有加载。增删在重启后生效，和文档一致。
- 热重载：
  - 有效修改（`- id: fx-same` + `disabled: true`）后再改回来，DSH 重新打印了 `[fx-conflict-a] loaded`，说明**热重载有效**；禁用时没有任何输出；
  - **无效修改会被静默忽略**：我在默认内容 `[]` 后面直接追加列表项，生成了无效 YAML。运行中的 DSH 没有任何提示；**但下次启动时直接失败**：
    ```
    Error: dsh: failed to parse overlay …/cordis.patch.yml: YAMLException: end of the stream or a document separator is expected (6:1)
    ```
- 对设计的影响：
  - Linux 和 macOS 上可以"运行中修改，然后提示重启"；Windows 等 V7 的结果再定；
  - **DSDock 修改 `cordis.patch.yml` 时必须用 yaml Node API 正确处理 `[]`**，写入前先用 `--dump-config` 加 `--patch` 验证；
  - 救援模式要专门识别"YAML 解析失败"，并直接定位到出错的行号。这是用户手动编辑时最容易犯的错误。

### V8 冒烟测试

- 状态：⚠️（结论明确，但和设计假设不同）
- **就绪信号**：web profile 启动成功后，**stdout** 打印一行：
  ```
  dsh web: http://127.0.0.1:<端口>/?token=<令牌>
  ```
  从启动到端口能连上约 **3.4 秒**。根路径没有令牌时返回 401，`/healthz` 返回 404。**这一行包含访问令牌，DSDock 记录日志时必须脱敏。**
- **插件加载失败时，DSH 不会退出**（t2 只装了 fx-broken）：进程一直运行，60 秒后被 `timeout` 杀掉。stderr：
  ```
  dsh: warning: 1 entry did not activate
  fx-broken (fx-broken): Error: fx-broken: boom on apply
      at new apply (file:///…/fixtures/fx-broken/index.js:3:9)
      …
  ```
  **没有生成** `$DSH_HOME/logs/startup-*.log`（那个日志只在必需插件失败时才写）。
- 同样的 profile，`--dump-config` 退出码是 0，所以**光靠 dump 发现不了加载错误**。
- **依赖服务没就绪**时也是同样的格式：
  ```
  dsh: warning: 1 entry did not activate
  dsh-ads (@nagi-ovo/dsh-ads): pending (waiting for service: webServer)
  ```
  （dsh-ads 需要 web 服务，被装进了只有 base 的 profile）
- **重复 id 时启动的实际表现**：web profile 里同时装了 fx-conflict-a 和 fx-conflict-b，stdout 只有 `[fx-conflict-b] loaded`。**fx-conflict-a 被静默丢掉，没有任何警告。**
- `headless --help` 不启动任何东西，不能用来做冒烟测试（和文档一致，没有单独测）。
- 推荐的冒烟测试方法：
  1. 第一级：`--dump-config`，看退出码和 stderr 里的 `not found` 警告（约 150ms）；
  2. 第二级：用一个**临时端口**启动 profile，同时读 stdout 和 stderr。web profile 等到就绪信号出现；其它 profile 等到"N 秒内没有新输出"。然后解析 `dsh: warning: N entry did not activate` 后面的每一行 `<id> (<包名>): <原因>`。超时建议 20 秒（正常约 3.5 秒；冷启动见 V10）。测完杀掉**整个进程组**。
- 对设计的影响：F2 和 F6 的"验证"分成两级；第二级的解析规则就是上面这个格式。可以研究一个更可靠的办法：用 `--patch` 注入一个 DSDock 自己的探针插件，在所有插件就绪后打印标记（M1 再调研）。

### V9 从外部连接正在运行的 DSH

- 状态：❌（没有可用的公开接口）
- 读了 `@deepseek-ai/dsh-api-remotes` 和 `@deepseek-ai/dsh-plugin-manager` 的 README：
  - 远程客户端是给 DSH 自己的 Web 前端（以及以后的 TUI）用的门面，"不负责传输层和服务发现"，传输由 API Gateway 负责；
  - Web 访问需要启动时打印的令牌；
  - 没有找到给外部程序用的、有文档的接口。
- 插件管理器 README 里几个可以照搬的细节：
  - 切换插件开关时，**只改** `cordis.patch.yml` 里**最后一条**匹配的覆盖的 `disabled`；没有就追加一条；只剩 `id` 和 `disabled` 并且和默认状态一致的覆盖会被删掉；
  - pnpm 拦截构建时，失败结果里会带 `pendingBuilds`；
  - 支持 npmmirror 作为备用源（`NPMMIRROR_REGISTRY`），按顺序尝试多个源；
  - 官方插件更新时用 `saveExact: true`。
- 对设计的影响：**M3 不做热安装**，改为"修改后提示重启，托盘里一键重启该 profile"。等官方开放接口后再做。插件开关（禁用而不卸载）照搬官方的语义。

### V10 不同版本同时使用同一个 DSH_HOME

- 状态：⚠️
- 第一次尝试：rc.2 用 `--from-default-profile web` 启动 web2，同时 rc.4 启动 web。rc.4 正常就绪；**rc.2 进程活着，但 30 秒内 stdout 和 stderr 都没有任何输出**。
- 之后 rc.2 单独启动（3 秒就绪）、两个版本再次同时启动（都正常就绪，端口都返回 401）、`--from-default-profile` 单独用（正常），**都没能复现**。第一次恰好是 rc.2 装好后的**第一次启动**，怀疑是冷启动慢。
- 用 rc.2 组装由 rc.4 安装的 t1：退出码 0，输出和 rc.4 的**完全一致**（98 行）。
- 两个版本同时运行时，共享的写入文件只有各自 profile 下的 `cordis.yml` 等，没有看到锁文件或报错。
- 对设计的影响：
  - 按 profile 锁定版本、多版本同时运行（F11）可行；
  - 安装新版本后，DSDock 应该先在临时 profile 里做一次**预热启动**；冒烟测试的超时时间要留余量。

### V11 会话格式降级

- 状态：⏸（没有使用 API key 的授权）
- 已知：rc.2 和 rc.4 的会话格式都是 v4，所以它们之间切换没有降级风险。npm 上最早的版本是 0.0.1-rc.1，后续测试可以从 0.1.x 系列里找会话格式更旧的版本。

### V12 转发器和 PATH

- 状态：✅
- 转发脚本 `$W/bin/dsh`：
  ```sh
  #!/bin/sh
  exec "<便携 node>" "<rc4>/lib/bin.js" "$@"
  ```
  放到 PATH 最前面后，`which dsh` 指向它，`dsh -V` 输出 `0.2.1-rc.4`。
- **SIGINT**：通过转发器启动 web 后，因为用了 `exec`，转发器和 Node 是**同一个进程号**。向进程组发送 SIGINT 后 **256ms** 内干净退出，端口也释放了。
- **踩到的坑（真实发生的）**：V8 里我是通过 shell 函数（不是 exec）在后台启动 DSH 的。`kill` 只杀掉了外面那层包装，里面的 Node 进程变成孤儿，一直运行到 V12 才被发现。
- 对设计的影响：
  - 转发器在 Unix 上必须用 `syscall.Exec` 替换自己；
  - DSDock 自己启动 DSH 时，要用**新的进程组**（Windows 上用 Job Object），停止时向整个进程组发送信号；
  - 提供"列出并清理残留 DSH 进程"的功能（§4.8 的进程扫描本来就要做）。

### V13 官方桌面版

- 状态：⏸（Linux 上没有官方桌面版）

### V14 从 git 安装时放行构建

- 状态：✅
- dshfind 目录里星数最高的 40 个 `build-required` 插件，没有一个同时带 `prepare` 脚本和 `dsh.bundle`（dshfind 的 `build-required` 是"仓库里没有构建产物"的意思）。所以改用自制样本：本地 git 仓库 `fx-git`，`prepare` 脚本在安装时生成 `index.js`，用 `git+file://` 安装。
- 第一次安装，退出码 1：
  ```
  Error: ERR_PNPM_GIT_DEP_PREPARE_NOT_ALLOWED
    ╰─▶ The git-hosted package "fx-git@1.0.0" needs to execute build scripts but
        is not in the "allowBuilds" allowlist.
    help: … allowBuilds:
            fx-git@git+file:///home/…/fixtures/fx-
          git#d90d3ace33aa31aca61ef0f9c17ee115f20f8fc3: true
  dsh: plugin command failed; diagnostics: …/profiles/t1/.plugin-manager/logs/operation-<随机>/pnpm.log
  dsh: git-hosted plugins build on install via their prepare script, which pnpm blocks until allowed — add the exact key pnpm printed above under allowBuilds in …/pnpm-workspace.yaml
  ```
  **注意，pnpm 打印的 key 被终端换行截断了**（`fx-` 和 `git#…` 被拆到两行）。
- 安装失败后，`package.json` 和组合包列表**完全没变**。
- 在 `pnpm-workspace.yaml` 末尾追加下面内容后，再装一次就成功了（1.1 秒），`node_modules/fx-git/index.js` 是安装时生成的，`--dump-config` 里出现了 `# == fx-git`：
  ```yaml
  allowBuilds:
    fx-git@git+file:///home/…/fixtures/fx-git#d90d3ace33aa31aca61ef0f9c17ee115f20f8fc3: true
  ```
- key 的格式：`<包名>@<规格>#<40 位 commit>`。`package.json` 里只记规格，不带 commit。
- 对设计的影响：
  - PF05 由 DSDock 自己拼出 key（用 `git ls-remote` 或锁文件拿到 commit），**不要解析 pnpm 打印的文字**；
  - key 和 commit 绑定，所以**每次更新 git 插件都要重新放行**，界面要展示两个 commit 之间的差异链接；
  - 安装失败时，从 `dsh: plugin command failed; diagnostics: <路径>` 这一行取出 pnpm 日志的路径，附到 DSDock 的操作记录里。

---

## 给下一棒的话

1. **Windows 重点**：V7（DSH 运行中删除插件会不会 `EBUSY` / `EPERM`）、V12（`.cmd` 的 Ctrl+C 提示、`.ps1` / `.cmd` / `.exe` 的优先级、关闭后有没有残留 Node 进程）、V2（`node-pty`、`koffi` 这些原生模块被拦截后，Windows 上能不能启动）。
2. **macOS 重点**：V13 官方桌面版（`DSH_HOME`、自带的 Node、插件市场额外写了什么）；V12 里 GUI 程序能否读到 shell 的 PATH。
3. **V8 冒烟测试**：请复用本文推荐的方法，记录你那边的就绪耗时。
4. **V10 冷启动**：请记录每个新装版本**第一次**启动的耗时，看看是不是普遍现象。
5. 如果用户同意使用 API key，请补 V11，并顺便确认 V2 遗留的问题：构建脚本被拦截后，agent 执行命令是否正常。
6. V2 里 pnpm 安装版本的对比，请在你的平台上也测一下合计占用（Windows 的 NTFS 硬链接行为可能不同）。
