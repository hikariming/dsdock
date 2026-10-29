# LESSONS

每条都是真实踩过的坑，以及由此定下的规则。改代码之前先读一遍。

## 2026-10-27 · M0 和第一波

### 1. 包装进程被杀，DSH 还在跑

**发生了什么**：M0 里通过 shell 函数在后台启动 DSH，`kill` 只杀掉了外层的包装，里面的 Node 进程变成孤儿，一直运行到很久以后才被发现。

**规则**：DSDock 启动 DSH 时一律放进新的进程组（`proc.newGroup`），停止时对整个进程组发信号（`proc.killGroup`）。以后写转发器时，Unix 上必须用 `syscall.Exec` 替换自身，不能用"启动子进程再等待"。

### 2. 加载失败的插件不会让 DSH 退出

**发生了什么**：插件在 `apply` 里抛异常，DSH 照常运行，只在 stderr 打印 `dsh: warning: N entry did not activate`。`--dump-config` 也发现不了这类问题。

**规则**：不能用"进程退出码"或"dump 成功"来判断插件是否正常。第二级验证必须真正启动一次，然后解析 stderr（`layer.ParseWarnings`）。

### 3. 重复 id 会让一个插件静默失效

**发生了什么**：两个插件插入同一个 id，dump 原样输出两行、没有任何警告，启动时只加载后一个。

**规则**：装前检查 PF10 和验证阶段都要检查重复 id（`layer.Tree.Duplicates`），提示里要写清楚**哪个插件会失效**。

### 4. yaml.v3 会静默丢掉文档后面的内容

**发生了什么**：`cordis.patch.yml` 的默认内容是 `[]`。如果在它后面追加列表项，js-yaml（DSH 用的）会拒绝这个文件，`yaml.Unmarshal` 却读成一个空列表，**不报任何错**。如果 DSDock 读完再写回去，就会抹掉用户写的配置。

**规则**：读用户的 YAML 文件一律用 Decoder 解码两次，第二次必须返回 `io.EOF`（`userpatch.parse`）。不能直接用 `yaml.Unmarshal` 处理用户写的文件。

### 5. `/dev/null` 也是"字符设备"

**发生了什么**：用 `os.ModeCharDevice` 判断 stdin 是不是终端，stdin 重定向到 `/dev/null` 时被误判为终端，脚本里会卡在确认提示上。

**规则**：判断终端用 `isatty`。

### 6. npm 默认不包含预发布版本，但 DSH 包含

**发生了什么**：DSH 自己的版本就是预发布版（`0.2.1-rc.4`）。按 npm 的默认规则它不满足 `>=0.2.0`，但 DSH 判断兼容性时用的是 `includePrerelease: true`。

**规则**：兼容性判断以 DSH 源码为准（`preflight.Compat` 移植自 `dsh-app-boot` 的 `evaluatePluginCompatibility`），并且用 `parity_test.go` 和 npm semver 的实际输出做对比。DSH 升级后，要重新核对这段源码。

### 7. pnpm 提示里的 key 会被终端换行截断

**发生了什么**：git 插件需要放行构建时，pnpm 打印的 `allowBuilds` key 被拆成了两行。

**规则**：不要解析 pnpm 给人看的提示文字；需要 key 时自己按 `<包名>@<规格>#<commit>` 拼出来。

### 8. 同一秒内的快照顺序是随机的

**发生了什么**：快照按"创建时间（毫秒），再按 id"排序，而 id 只精确到秒、后面跟着随机串。禁用、启用这种很快的操作，前后两个快照常常落在同一毫秒，顺序就成了随机的。本地碰巧排对，CI 上暴露了出来。

**规则**：同一时间内的先后顺序用 sqlite 的 `rowid`（插入顺序）决定，不能依赖 id 字符串。"最近一个已验证的快照"选错，会直接导致救援时回到错误的状态。

## 2026-10-27 · 第二波（版本管理）

### 9. 不兼容的插件会被 DSH 静默跳过

**发生了什么**：DSH 切换到更新的版本后，一个声明 `engines.dsh: <0.2.1-rc.3` 的插件在 rc.4 下被跳过了。`--dump-config` 的退出码仍然是 0，只在 stderr 打印一行 `dsh: skipping profile bundle "…": …`。DSDock 当时没有识别这一行，`status` 显示 ✓。

**规则**：凡是读 DSH 输出的地方，都要用 `layer.ParseWarnings` 检查 `Skipped`。切换版本前的预检以**目标版本实际组装的结果**为准，而不是只做静态判断。这样也能自动尊重用户用 `dsh plugin allow-version` 授予的豁免。

### 10. 不要把 HEAD 的要求当成已发布版本的要求

**发生了什么**：第一波把 Node 最低版本写死成 24.9.0，依据是上游 HEAD 的 `apps/cli/package.json`。但已发布的 rc.2、rc.3 声明的是 `^22.19.0 || >=24.0.0`，Node 22 的用户会被误报。

**规则**：版本相关的要求一律按 tag（`dsh-v<版本>`）读取；读不到时用所有已发布版本都满足的保守范围（`dshenv.DefaultNodeRange`）。

### 11. 为了一个小文件下载整个包

**发生了什么**：装前检查需要插件的 `cordis.patch.yml`。一个解压后 24MB 的插件把这个文件放在压缩包的最后，读到它花了 2 分 21 秒，最后超时，整个检查失败。

**规则**：单个文件先从 jsDelivr 取（`npmreg.FileCDN`）；退回下载完整包时有大小上限。读取失败不能让检查失败，改为安装后再验证。

### 12. 测试里的 os.Executable() 是测试程序

**发生了什么**：转发器脚本里写的是 `os.Executable()`。在 `go test` 里，它返回的是测试程序本身，而不是 dsdock。

**规则**：凡是涉及"调用 dsdock 自己"的测试（转发器），先 `go build` 出真正的二进制，再通过它来测（见 `versions_integration_test.go`）。

## 2026-10-28 · M2（救援模式、图形界面）

### 13. `pkill -f` 会杀掉执行它的 shell

**发生了什么**：清理残留的 Chromium 时用了 `pkill -f '<模式>'`，又换成 `pgrep -f '<模式>' | xargs kill`。两次都把执行这条命令的 shell 自己杀掉了：`-f` 匹配的是完整命令行，而 shell 的命令行里恰好包含这个模式。同一次还发现，截图脚本只结束了 Chromium 主进程，它的 zygote 子进程留了下来。

**规则**：按命令行查找进程时用 `[c]hrome` 这种写法，让模式匹配不到自己；能按 PID 或进程组结束就不要按名字。自己写的测试工具也要遵守第 1 条：子进程放进独立的进程组，结束时对整个组发信号（`scripts/gui-shots.mjs`）。

## 2026-10-28 · preview.2 的用户反馈

### 14. 从 Finder 打开的应用看不到终端里的 PATH

**发生了什么**：用户在终端里 `dsh -V` 能输出版本，DSDock.app 却说"还没有可用的 DSH"。macOS 从 launchd 启动图形应用，PATH 只有 `/usr/bin:/bin:/usr/sbin:/sbin`；用户在 `~/.zshrc`、`~/.zprofile` 里加的路径（nvm、Homebrew、npm 全局目录）全都看不到，`node` 和 `pnpm` 也一样。M0 手册的 V12 列过这个问题，但当时没有 Mac 实测；CI 截图又是从终端启动的应用，继承了终端的 PATH，所以也没发现。

**规则**：桌面版启动时先运行一次用户的登录 shell 读取 PATH（`internal/shellenv`）。验证桌面版要模拟真实的启动方式：`desktop.yml` 用 `env -i` 加最小 PATH 启动应用，并截图确认。

### 15. headless 不读网页里保存的 key，需要授权的操作也会被静默拒绝

**发生了什么**：做快速提问时读 headless 的文档才发现两件事。一是 headless 关闭了凭据存储，只从环境变量和 `.env` 读 key，用户在 DSH web 里保存的 key 不生效；二是 headless 没有授权应答者，沙箱外的操作（启动容器、写文件）一律被拒绝，用户看不到任何提示。

**规则**：快速提问用 `--patch` 覆盖层恢复凭据读取，并注入 DSDock 自己的应答者（`internal/quick/assets/dsdock-approval.mjs`）。不修改用户的任何 profile。测试用本地的假 Anthropic 接口（`llm-deepseek` 的 `baseURL`），不花模型费用。

### 16. 只处理 Ctrl+C，被 kill 时子进程就留下了

**发生了什么**：面板启动的 DSH web 应该随 DSDock 一起退出。测试时用 `kill` 停掉 `dsdock gui`，DSH web 还在跑。CLI 只监听了 `os.Interrupt`，SIGTERM 直接结束进程，清理代码（`srv.Close()`）没有运行。

**规则**：凡是启动了长期运行的子进程的命令，都要处理 SIGINT、SIGTERM、SIGHUP（`cli.Main` 统一处理），子进程放进独立的进程组，退出时整组停掉。测试清理时不能只按 Ctrl+C，也要用 `kill` 验证。桌面版由 Wails 处理信号，退出时调用 `OnShutdown`。

### 17. 用环境变量里的 key 测试，掩盖了用户真实的登录方式

**发生了什么**：快速提问的集成测试用 `DEEPSEEK_API_KEY` 环境变量提供 key，测试通过。用户在 Mac 上用的时候报 `MISSING_CREDENTIAL`（`deepseek-official` 路由没有 API key）。原因是用户在 DSH web 里选的模型（例如 DeepSeek 账号登录后的模型）只写进了那个 profile 的 `cordis.patch.yml`，headless profile 用的是它自己的默认模型。

**规则**：测试要覆盖用户真实的配置方式：key 保存在 `$DSH_HOME/.credentials.yaml`、模型在网页里选过，而不只是测环境变量。借用别的 profile 运行时，先列出用户在原 profile 里做过的设置（模型、key、登录状态），逐项确认新 profile 能不能拿到。

### 18. 照搬别人的窗口设置，输入法候选框被面板盖住

**发生了什么**：菜单栏面板照搬了 Magpie 的设置：不激活应用的 NSPanel，层级是 floating（Wails 的 `AlwaysOnTop`）。Magpie 的面板里没有输入框，这套设置对它没问题。DSDock 的面板里有输入框，用户反馈打中文时候选框被盖住；把输入框挪到面板底部也没有解决。原因有两个：有的第三方输入法把候选框画在 floating 或更低的层级，会被面板盖住；面板不激活应用时，输入法的输入上下文可能不生效（Ghostty 的快捷终端遇到过同样的问题）。

**规则**：有文字输入的面板要激活应用（`Show().Focus()`），层级用 normal，不要高于输入法。照搬别人的窗口设置前，先确认对方的使用场景（有没有输入框）和自己一样。开发机上测不了输入法，改完必须请用户在 Mac 上用中文输入法验证。
