# DSDock M0 实测接力手册

> 读者：在 **macOS** 或 **Windows** 上执行本手册的 coding agent（以及配合它的用户）。
> 目的：把 [design.zh-CN.md](./design.zh-CN.md) 里所有标了【待实测】的假设，在真实机器上跑出结论。
> 产出：`docs/m0-results/<平台>.md`（`macos.md`、`windows.md`、`linux.md`），并更新本文末尾的"接力进度"表。

---

## 0. 给 agent 的开场说明（必读）

你在帮用户做一个叫 **DSDock** 的项目：它是 DeepSeek Harness（DSH）的外部管理器，负责 DSH 版本管理，以及插件的安装、快照、回滚和救援。现在还没写代码，处于 **M0 · 验证假设** 阶段。你的任务**只有实测和记录**，不写产品代码。

开始之前：

1. 读完本文第 1 节的安全规则，**严格遵守**；
2. 浏览一遍 `design.zh-CN.md` 的 §2（DSH 机制事实），了解每项 V 在验证什么；
3. 查看本文末尾的"接力进度"，看其它平台已经得到哪些结论，避免重复劳动。和其它平台结果不一致的地方要**明确写出来**；
4. 按 V1 → V14 的顺序执行。**V1 不通过就停下**，向用户报告，不要继续往下做。

---

## 1. 安全规则

| # | 规则 |
|---|---|
| S1 | **绝不读写用户真实的 `~/.dsh`**（Windows 上是 `%USERPROFILE%\.dsh`）。所有测试都在临时工作区里，通过 `DSH_HOME` 隔离。V1 必须先证明隔离是有效的 |
| S2 | **不卸载、不修改**用户已经安装的 DSH（全局 npm 安装、桌面版），也不修改用户的 PATH、shell 配置文件或注册表。V12 只在当前终端会话里临时修改 PATH |
| S3 | **不使用用户的 API key**，除非用户在当前对话里明确同意（只有 V11 需要）。没有同意就把 V11 标为"阻塞"，然后跳过 |
| S4 | 不发布任何东西（npm、GitHub、网站），不 push 代码，除非用户明确要求 |
| S5 | 自己启动的进程，测完要关掉。最后清理临时工作区，但**清理前先把需要的日志摘录到结果文件里** |
| S6 | 结果文件里不能出现 token、key、邮箱等敏感信息。日志摘录前先检查 |
| S7 | 遇到会影响用户真实环境的情况（比如发现 DSH 忽略了 `DSH_HOME`、往真实目录写了文件），**立刻停下**，向用户报告 |

---

## 2. 准备工作

### 2.1 记录环境信息（写在结果文件开头）

- 操作系统和版本、CPU 架构（arm64 或 x64）
- `node -v`、`npm -v`、`pnpm -v`（没装就写"未安装"）
- 机器上是否已经有 DSH：`which dsh` 或 `where dsh` 的结果，`dsh -V` 的版本，是否装了官方桌面版
- 使用的 shell（zsh、bash、PowerShell 7、Windows PowerShell 5.1、cmd）

**前置条件**：DSH 要求 Node ≥ 24.9.0，`dsh plugin` 要求 PATH 里有 pnpm。如果不满足：

- Node 不够新：**不要升级用户的全局 Node**。用一个便携的 Node 放进工作区（从 nodejs.org 下载对应平台的压缩包，解压到 `$W/node`），只在当前会话里把它放到 PATH 最前面；
- 没有 pnpm：用工作区里那个 Node 自带的 `corepack enable --install-directory $W/bin` 装，或者 `npm i -g pnpm --prefix $W/pnpm`，同样只加到当前会话的 PATH 里。

**记下实际用的是哪种方式**，这本身就是 V2 的结论之一。

### 2.2 创建工作区

**macOS（zsh / bash）：**

```bash
export W="$HOME/dsdock-m0"            # 工作区
mkdir -p "$W"/{versions,home,fixtures,logs}
export DSH_HOME="$W/home"             # 关键：隔离 DSH_HOME
echo "W=$W DSH_HOME=$DSH_HOME"
```

**Windows（PowerShell）：**

```powershell
$W = "$env:USERPROFILE\dsdock-m0"
New-Item -ItemType Directory -Force "$W\versions","$W\home","$W\fixtures","$W\logs" | Out-Null
$env:DSH_HOME = "$W\home"
"W=$W DSH_HOME=$env:DSH_HOME"
```

> 之后每开一个新终端，都要重新设置 `W` 和 `DSH_HOME`。

### 2.3 把两个 DSH 版本分别装进独立目录

```bash
# macOS
npm install --prefix "$W/versions/rc2" @deepseek-ai/dsh@0.2.1-rc.2
npm install --prefix "$W/versions/rc4" @deepseek-ai/dsh@0.2.1-rc.4
alias dsh2="node $W/versions/rc2/node_modules/@deepseek-ai/dsh/lib/bin.js"
alias dsh4="node $W/versions/rc4/node_modules/@deepseek-ai/dsh/lib/bin.js"
dsh2 -V; dsh4 -V
```

```powershell
# Windows
npm install --prefix "$W\versions\rc2" "@deepseek-ai/dsh@0.2.1-rc.2"
npm install --prefix "$W\versions\rc4" "@deepseek-ai/dsh@0.2.1-rc.4"
function dsh2 { node "$W\versions\rc2\node_modules\@deepseek-ai\dsh\lib\bin.js" @args }
function dsh4 { node "$W\versions\rc4\node_modules\@deepseek-ai\dsh\lib\bin.js" @args }
dsh2 -V; dsh4 -V
```

> 版本号以 npm 上的 `latest` 和 `next` 为准（`npm view @deepseek-ai/dsh dist-tags`）。如果 rc.2 或 rc.4 已经不是这两个渠道的版本，就用当时的 `latest` 和 `next`，并在结果里注明。
> **下文用 `dsh4` 作为默认版本**，只有涉及版本切换的测试才用 `dsh2`。

> ⚠️ **Linux 实测踩到的坑**：`dsh2` / `dsh4` 是 shell 函数（Windows 上是 PowerShell 函数）。**在后台启动 DSH 时不要用它们**，否则 `kill` 只会杀掉外层，里面的 Node 进程会变成孤儿一直运行。后台启动时直接写 `node <版本目录>/node_modules/@deepseek-ai/dsh/lib/bin.js …`，记下 Node 进程本身的 PID；测完用 `pgrep -af dsdock-m0`（Windows：`Get-CimInstance Win32_Process | ? CommandLine -match 'dsdock-m0'`）检查有没有残留。
>
> 另外：用 `dsh plugin --profile <任意名字>` 创建的 profile **只有 base，没有 web 服务**。需要 web 的测试（V7、V8、V10）请用名字叫 `web` 的 profile（`dsh4 plugin --profile web add …` 会按官方 web 模板创建），或者用 `--from-default-profile web`。启动 web profile 的命令是 `dsh4 --profile web --no-open --port <端口>`，**不要**在后面再加 `web`。

记录：两个版本各自的安装耗时、目录大小（`du -sh` 或 PowerShell 的 `Get-ChildItem -Recurse | Measure-Object Length -Sum`）。

### 2.4 创建测试插件样本

在 `$W/fixtures/` 下创建 5 个本地组合包。每个都是一个目录，里面三个文件。

**fx-ok**：正常插件

`fx-ok/package.json`
```json
{
  "name": "fx-ok",
  "version": "1.0.0",
  "type": "module",
  "main": "index.js",
  "files": ["index.js", "cordis.patch.yml"],
  "dsh": { "bundle": { "patch": "./cordis.patch.yml" } }
}
```
`fx-ok/cordis.patch.yml`
```yaml
- insert:
    - id: fx-ok
      name: fx-ok
```
`fx-ok/index.js`
```js
export const name = 'fx-ok'
export function apply() {
  console.log('[fx-ok] loaded')
}
```

**fx-broken**：加载时抛异常。复制 fx-ok，把所有 `fx-ok` 换成 `fx-broken`，`index.js` 改成：
```js
export const name = 'fx-broken'
export function apply() {
  throw new Error('fx-broken: boom on apply')
}
```

**fx-incompat**：声明不兼容。复制 fx-ok，改名为 `fx-incompat`，在 `package.json` 里加上：
```json
"engines": { "dsh": ">=99.0.0" }
```

**fx-conflict-a / fx-conflict-b**：两个包插入同一个 id。分别复制 fx-ok，改名为 `fx-conflict-a` 和 `fx-conflict-b`；**两者**的 `cordis.patch.yml` 都写：
```yaml
- insert:
    - id: fx-same
      name: fx-conflict-a    # b 里写 fx-conflict-b
```

**npm 上的真实插件**：`@nagi-ovo/dsh-ads`（有 0.1.2 和 0.1.3 两个版本，没有安装脚本），用来测更新和回滚。

---

## 3. 实测项

每一项都按这个格式记录到结果文件里：

```markdown
### Vx 标题
- 状态：✅ 符合预期 / ⚠️ 部分符合 / ❌ 不符合 / ⏸ 阻塞（原因）
- 用的 DSH 版本：
- 执行的命令：（关键几条）
- 关键输出：（截取，最多 30 行，已经脱敏）
- 结论：（一两句话）
- 对设计的影响：（需要改 design.zh-CN.md 的哪一节、怎么改；没有影响就写"无"）
```

---

### V1 `DSH_HOME` 隔离（**不通过就停下**）

**目标**：确认设置 `DSH_HOME` 环境变量后，DSH 只读写这个目录。

步骤：
1. 先记下真实 home 目录的状态：`~/.dsh` 存不存在、最近修改时间（只看元数据，**不读内容**）。
2. `dsh4 --profile t1 --dump-config > "$W/logs/v1.txt"`（Windows 上把输出重定向到 `$W\logs\v1.txt`）。
3. 检查 `$DSH_HOME/profiles/t1/` 有没有被创建；再看一眼真实的 `~/.dsh` 修改时间有没有变化。

判定：只有工作区里出现了 `profiles/t1`，并且真实目录没有任何变化，才算 ✅。
**另外记录**：不设置 `DSH_HOME` 时的默认路径（只从文档或 `--help` 里确认，**不要实际运行**）。

---

### V2 版本隔离和运行环境

**目标**：确认用 `npm install --prefix` 装到独立目录的多个 DSH 版本可以并存、分别运行。

1. `dsh2 -V` 和 `dsh4 -V` 分别输出正确的版本号；
2. 读两个版本 `package.json` 里的 `engines`；
3. 在两个版本的目录里搜索 `SESSION_FORMAT_VERSION` 的定义，找到具体数值。提示：在 `node_modules/@deepseek-ai/dsh-session/` 下面搜 `SESSION_FORMAT_VERSION =`；
4. 记录：Node 和 pnpm 是怎么准备的（见 2.1）。如果用的是工作区里的便携 Node，确认 DSH 在它下面能正常运行；
5. 空间对比（Linux 结论：npm 每个版本 856MB；pnpm 两个版本合计 548MB）：在 `$W/pv/<版本>/` 下各放一个 `{"private":true}` 的 package.json，执行 `pnpm add @deepseek-ai/dsh@<版本> --config.node-linker=hoisted`（pnpm 会因为构建脚本被拦截返回退出码 1，属于正常现象），然后分别 `du` 单个目录和整个 `pv/`。**Windows 上请特别看硬链接是否生效**。

---

### V3 profile 的完整结构

1. `dsh4 plugin --profile t1 add "$W/fixtures/fx-ok"`
2. 列出 `$DSH_HOME/profiles/t1/` 下的所有文件（不包括 `node_modules` 内部），以及 `$DSH_HOME` 顶层的所有文件和目录；
3. 把 `package.json` 和 `pnpm-workspace.yaml`（如果有）的完整内容贴到结果里；
4. 锁文件叫什么名字（`pnpm-lock.yaml`？），大概多少行；
5. pnpm 的全局存储在哪里（`pnpm store path`），`node_modules` 里的文件是硬链接、软链接还是复制（Windows 上特别注意这一点）；
6. `fx-ok` 这个本地依赖在 `package.json` 里记成了什么（`link:` 还是 `file:`）。

---

### V4 `--dump-config` 的输出格式

1. `dsh4 --profile t1 --dump-config`：贴出完整输出（应该不长），特别注意**来源注释的具体格式**（文档里说会有一行类似 `# == fx-ok` 的标记）；
2. 记录退出码和耗时；
3. 加上冲突样本：`dsh4 plugin --profile t1 add "$W/fixtures/fx-conflict-a"`，再 add `fx-conflict-b`，然后再跑一次 `--dump-config`，看 `fx-same` 这一行怎么显示：有没有警告？能不能看出是谁覆盖了谁？
4. 把 `$DSH_HOME/profiles/t1/cordis.patch.yml` 的**全部内容替换**成下面这样（Linux 已确认：修改已有的行直接写 `- id:`，没有 `override` 关键字。注意文件默认内容是 `[]`，**不能在它后面直接追加列表项**，否则 YAML 无效，DSH 会启动失败）：
   ```yaml
   - id: no-such-row
     config: {}
   - id: fx-ok
     disabled: true
   ```
   看 stderr 里的警告长什么样、退出码是多少，以及 fx-ok 那一层的层标记有什么变化；
5. 跑一次 `dsh4 --profile t1 --dump-config-schema`，记录输出大小，以及根节点 `x-cordis` 下面有哪些键；
6. 测完把 `cordis.patch.yml` 恢复原样，并 remove 掉两个冲突样本。

---

### V5 快照和回滚是否可行

**目标**：验证"只保存几个小文件，再用锁文件重装 `node_modules`"这个方案行不行。

1. 当前 t1 里只有 fx-ok。安装真实插件：`dsh4 plugin --profile t1 add @nagi-ovo/dsh-ads@0.1.2`；
2. **拍快照**：把 `package.json`、锁文件、`pnpm-workspace.yaml`（如果有）、`cordis.patch.yml` 复制到 `$W/snap1/`；
3. 改变状态：`dsh4 plugin --profile t1 remove @nagi-ovo/dsh-ads`，再 add `fx-ok` 以外的一个样本（比如 fx-conflict-a）；
4. **回滚**：把 `$W/snap1/` 里的文件复制回去，然后依次尝试：
   - a. `dsh4 plugin --profile t1 install --frozen-lockfile --offline`
   - b. 如果 a 失败，换成 `dsh4 plugin --profile t1 install --frozen-lockfile`
5. 检查：`--dump-config` 的结果和第 2 步时是否完全一致；`node_modules` 里多出来的 fx-conflict-a 有没有被清掉；`dsh.profile.bundles` 是否正确；
6. 记录每一步的耗时。另外测一种情况：直接删掉整个 `node_modules` 后再回滚，耗时多少。

---

### V6 更新的语义（CLI 方式，DSH 没在运行）

1. 当前装的是 `@nagi-ovo/dsh-ads@0.1.2`。试 `dsh4 plugin --profile t1 add @nagi-ovo/dsh-ads@0.1.3`：会成功，还是报错说已经装过？
2. 把版本恢复到 0.1.2 后，试 `dsh4 plugin --profile t1 update @nagi-ovo/dsh-ads --latest`；
3. 两种方式下，`dsh.profile.bundles` 里的顺序有没有变化（会不会被移到列表末尾）？

---

### V7 DSH 正在运行时，用 CLI 修改 profile

0. 准备：`dsh4 plugin --profile web add "$W/fixtures/fx-ok"`，再 `dsh4 plugin --profile web add @nagi-ovo/dsh-ads@0.1.2`（dsh-ads 需要 web 服务）。
1. 启动：`dsh4 --profile web --no-open --port 38080`（按上面的提醒直接用 node 在后台启动，或者另开一个终端），等 stdout 出现 `dsh web: http://…` 这一行；
2. DSH 运行期间，执行 `dsh4 plugin --profile web add "$W/fixtures/fx-conflict-a"`：命令是否成功？正在运行的 DSH 有什么反应（日志里有没有输出）？
3. DSH 运行期间，执行 `dsh4 plugin --profile web remove @nagi-ovo/dsh-ads`：**Windows 上重点看会不会出现 `EBUSY`、`EPERM` 之类的文件占用错误**；
4. DSH 运行期间，把 `profiles/web/cordis.patch.yml` 的内容替换成 `- id: fx-same` 加 `disabled: true`（**整体替换**，不要在 `[]` 后追加），再改回原样，看 stdout 有没有重新出现 `[fx-conflict-a] loaded`（Linux 上会出现，说明热重载有效）；
5. 关掉 DSH，再启动一次，确认第 2、3 步的修改生效了；
6. 测完关掉进程。

---

### V8 冒烟测试：怎样不经人工判断就知道 DSH 启动成功了

**目标**：为救援功能和二分定位找到一个自动化的"启动是否成功"判断方法。

> Linux 的结论（请在你的平台上复核）：就绪信号是 **stdout** 上的 `dsh web: http://127.0.0.1:<端口>/?token=<令牌>`（**令牌必须脱敏**），约 3.5 秒；**插件加载失败时 DSH 不会退出**，而是在 stderr 打印 `dsh: warning: N entry did not activate`，后面每行是 `<id> (<包名>): <原因>`。

1. 正常情况：`dsh4 --profile web --no-open --port 38081`，记录就绪信号出现的耗时，以及 `http://127.0.0.1:38081/` 返回的状态码；
2. 失败情况：新建 profile t2，add `fx-broken`，然后 `dsh4 --profile t2`（t2 只有 base，没有就绪信号，等 20 秒）：
   - 进程有没有退出？退出码是多少？
   - stderr 里写了什么？
   - `$DSH_HOME/logs/` 下面有没有生成 `startup-*.log`？日志里有没有指出是 `fx-broken`？（只摘录前 30 行，**注意脱敏**）
3. 对 t2 跑 `--dump-config`：`fx-broken` 只在 apply 时才会抛错，所以配置组装**应该**能通过。确认这一点，它说明光靠 `--dump-config` 不足以完成冒烟测试；
4. 试一下 headless profile 能不能当冒烟测试用：`dsh4 --profile t2 headless --help`。文档说 `--help` 不启动任何东西，所以它**很可能**没用，确认一下；
5. 重复 id：在 web profile 里同时装 fx-conflict-a 和 fx-conflict-b，启动后看 stdout 里哪个打印了 `loaded`（Linux 上只有 b，a 被静默丢掉）；
6. 结论：推荐一种冒烟测试方法，并给出超时时间的建议值。

---

### V9 从外部连接正在运行的 DSH（Host API）

**目标**：搞清楚 DSDock 能不能在 DSH 运行时热安装插件，不需要重启。

1. 在 DSH 安装目录里找 `@deepseek-ai/dsh-api-remotes` 这个包，读它的 README（或者看上游仓库 `packages/` 下面对应的 README）；也找一下 `@deepseek-ai/dsh-plugin-manager` 的 README；
2. 回答：远程客户端怎么连接（HTTP、WebSocket，还是别的）？地址和端口从哪里来？怎么鉴权？是不是只有 web profile 才提供这个接口？
3. 如果文档写得够清楚，就写一个**最小的 Node 脚本**（放在 `$W/scripts/`，**不要**放进任何仓库），连接 V8 里启动的 web 实例，调用一次 `installBundle` 安装 `fx-conflict-a`，看结果是不是 `applied`，以及是否不重启就生效了；
4. 做不出来就写清楚卡在哪一步，标 ⏸。

---

### V10 不同版本的 DSH 同时使用同一个 `DSH_HOME`

1. 用 rc2 启动 `--profile web2 --from-default-profile web --no-open --port 38083`，同时用 rc4 启动 `--profile web --no-open --port 38084`。**记录 rc2 这次是不是它装好后的第一次启动，以及就绪耗时**（Linux 上第一次出现过 30 秒无输出，之后没能复现）；
2. 两个实例能不能同时正常运行？
3. 关掉两个实例后，看 `$DSH_HOME` 顶层有哪些共享文件（凭据、日志、索引之类），各自的修改时间；有没有出现锁文件或者报错；
4. 用 `dsh2` 去跑原本由 `dsh4` 装好的 t1（`dsh2 --profile t1 --dump-config`）：能不能组装成功？内置组合包是不是从 rc2 的目录里解析的（看来源注释里的路径）？

---

### V11 会话格式降级（需要用户同意使用 API key，否则标 ⏸）

前提：V2 里发现两个版本的 `SESSION_FORMAT_VERSION` **不一样**。如果一样，就去 npm 上找一个会话格式更旧的版本（往前翻，从 0.2.0 系列开始找），装到 `$W/versions/old`。

1. 用新版本跑一个最小任务，生成一个会话：`dsh4 --profile headless "回答一个字：好"`（需要 `DEEPSEEK_API_KEY`）；
2. 找到会话文件存在哪里，记录路径和文件名格式；
3. 用旧版本列出会话、继续这个会话（如果 headless 支持 `--session-id`），记录：是整个会话列表都加载失败，还是只有这一个会话打不开？报错信息是什么？
4. 更早的会话（由旧版本创建）用新版本打开后，旧格式的原文件是不是还在？

---

### V12 转发器和 PATH

**只在当前终端会话里修改 PATH，不要写入任何配置文件。**

1. 在 `$W/bin/` 里放一个最简单的转发脚本，模拟 DSDock 的转发器：
   - macOS：`$W/bin/dsh`，内容是 `#!/bin/sh` 加上 `exec node "$W/versions/rc4/node_modules/@deepseek-ai/dsh/lib/bin.js" "$@"`（把 `$W` 展开成实际路径），然后 `chmod +x`；
   - Windows：先写成 `$W\bin\dsh.cmd`（`@node "<实际路径>\bin.js" %*`）。
2. 把 `$W/bin` 放到当前会话的 PATH 最前面，运行 `dsh -V`、`which dsh` / `where dsh`。如果机器上本来就装了全局的 dsh，确认现在用的是转发器这一个；
3. **Ctrl+C 测试**：通过转发器启动 web，按 Ctrl+C：
   - DSH 能不能正常退出（看它有没有打印关闭的日志）？会不会留下孤儿 Node 进程？
   - Windows 上用 `.cmd` 时，会不会弹出"终止批处理操作吗(Y/N)?"——这正是设计里打算用 `.exe` 转发器的原因，确认一下；
4. Windows 额外记录：PowerShell 7、Windows PowerShell 5.1、cmd 三种环境下，`dsh` 分别会被解析成哪个文件（`.ps1`、`.cmd`、`.exe` 的优先级）；
5. macOS 额外记录：用户的登录 shell 是什么；`~/.zprofile` 和 `~/.zshrc` 哪个存在（只看是否存在，**不修改**）；GUI 程序（比如从 Dock 启动的 App）能不能读到 shell 里的 PATH（可以查资料确认，不必实测）。

---

### V13 官方桌面版（只观察，不修改）

如果机器上装了 DSH 官方桌面版（或者用户愿意装一个）：

1. 它的 DSH 运行时放在哪里（App 包内部的哪个路径），版本号是多少？
2. 它用的 `DSH_HOME` 是 `~/.dsh` 吗？它的 profile 叫什么（文档说有一个保留的 `desktop` profile）？
3. 它的插件市场装插件时，除了 profile 目录，还会不会写别的地方？（只看文件的修改时间，**不读内容**）
4. 它自带的是哪个版本的 Node？

没有桌面版、用户也不想装，就标 ⏸。

---

### V14 从 git 安装时放行构建

1. 找一个需要构建、可以从 GitHub 直接安装的 DSH 插件（可以从 dshfind 的目录里找 `install.kind` 为 `build-required` 的插件，挑 star 数高、仓库活跃的一个）；
2. `dsh4 plugin --profile t1 add github:<owner>/<repo>`：记录第一次失败时的完整提示（pnpm 的 `allowBuilds` 提示，以及 dsh 给出的指引）；
3. 按提示修改 `profiles/t1/pnpm-workspace.yaml`，再装一次；贴出修改后的 yaml 片段；
4. 记录这个 key 的具体格式（DSDock 要能自动生成它，并让用户确认）。

---

## 3.5 附加：验证 DSDock 本身（M0 做完后再做）

DSDock 的命令行（插件生命周期和版本管理）已经写好，Linux 上全部测过。请在你的平台上再跑一遍，**发现问题就记进结果文件的"DSDock"一节**，不要直接改代码（除非用户要求）。需要 Go ≥ 1.26。

```bash
go build -o dsdock .          # Windows：go build -o dsdock.exe .
go vet ./... && go test ./...
```

**macOS**（集成测试会真的安装 DSH 版本、启动 DSH，全部在临时目录里）：

```bash
DSDOCK_IT_DSH=<某个 DSH 的 lib/bin.js> DSDOCK_IT_NETWORK=1 go test ./internal/cli/ -run Integration -v -count=1
```

另外手动确认：`./dsdock dsh shim` 之后，从**新开的终端**和**从 Dock 启动的终端 App** 里运行 `dsh -V`，是否都走 DSDock 的转发器（这关系到 V12 里 GUI 程序读不到 PATH 的问题）。

**Windows**（集成测试依赖 Unix 的转发器脚本，会自动跳过；请手动验证这几项）：

1. 用独立的 `DSDOCK_HOME` 和 `DSH_HOME`（同 2.2 的隔离方式）：`.\dsdock.exe dsh install latest`，然后 `.\dsdock.exe dsh shim`，按提示**只在当前 PowerShell 会话**里把转发器目录加到 PATH 前面；
2. `dsh -V`、`dsh plugin --profile web add <fx-ok 的路径>`、`dsh --profile web --no-open --port 38099`；
3. 在第 2 步启动的 web 里按 **Ctrl+C**：DSH 是否正常退出？有没有出现"终止批处理操作吗"？用 `Get-CimInstance Win32_Process | ? CommandLine -match 'dsdock-m0'` 检查有没有残留的 node 或 dsh.exe；
4. 直接关掉终端窗口（不按 Ctrl+C），再检查一次残留进程；
5. `.\dsdock.exe add <fx-broken 的路径> -p web --smoke`：应该自动回滚，并且没有残留进程（这里用的是 `taskkill /T`）；
6. DSH 运行时执行 `.\dsdock.exe remove fx-ok -p web`：记录是否出现文件占用错误，以及 DSDock 回滚后 profile 是否完好。

## 4. 收尾

1. 把所有启动过的 DSH 进程都关掉（检查一遍有没有残留的 Node 进程）；
2. 确认结果文件已经写完，日志已经脱敏；
3. 问用户要不要删除工作区 `$W`（**不要自己直接删**）；
4. 更新下面的"接力进度"表：每个平台一行，写上完成时间、通过和不通过的数量、最重要的 3 条发现；
5. 在结果文件末尾写"给下一棒的话"：哪些项和其它平台的结果不一样，哪些项建议换个平台复测。

---

## 5. 接力进度

| 平台 | 执行者 | 日期 | ✅ / ⚠️ / ❌ / ⏸ | 最重要的发现 | 结果文件 |
|---|---|---|---|---|---|
| Linux | Claude Code | 2026-10-27 | 9 / 2 / 1 / 2 | ① 加载失败的插件不会让 DSH 退出，重复 id 时前一个插件被静默丢弃；② 快照方案成立：32KB、离线回滚约 380ms；③ 用 pnpm 装多个 DSH 版本只需三分之一空间 | `m0-results/linux.md` |
| macOS | — | — | 未开始 | — | `m0-results/macos.md` |
| Windows | — | — | 未开始 | — | `m0-results/windows.md` |

---

## 6. 结果汇总（三个平台都完成后填写）

| 项 | Linux | macOS | Windows | 对设计的影响 |
|---|---|---|---|---|
| V1 DSH_HOME 隔离 | ✅ | | | |
| V2 版本隔离、Node、pnpm | ✅ npm 856MB/版；pnpm 两版共 548MB | | | |
| V3 profile 结构 | ✅ | | | |
| V4 --dump-config 格式 | ✅ 重复 id 无警告 | | | |
| V5 快照和回滚 | ✅ 380ms | | | |
| V6 更新语义 | ✅ add 新版本即可 | | | |
| V7 运行中修改 | ✅ 无占用；无效 YAML 被静默忽略 | | | |
| V8 冒烟测试 | ⚠️ 失败不退出，要解析 stderr | | | |
| V9 Host API | ❌ 无外部接口 | | | |
| V10 多版本共用 DSH_HOME | ⚠️ 首次启动卡顿一次 | | | |
| V11 会话格式降级 | ⏸ | | | |
| V12 转发器和 PATH | ✅ 必须 exec | | | |
| V13 官方桌面版 | ⏸ 不适用 | | | |
| V14 git 安装放行构建 | ✅ | | | |
