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
