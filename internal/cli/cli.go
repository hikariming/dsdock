// Package cli implements the dsdock command line as a thin shell over
// internal/core.
package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"

	"github.com/mattn/go-isatty"

	"github.com/hikariming/dsdock/internal/core"
	"github.com/hikariming/dsdock/internal/dshenv"
	"github.com/hikariming/dsdock/internal/proc"
	"github.com/hikariming/dsdock/internal/txn"
)

// Version is set at build time.
var Version = "dev"

const usage = `DSDock：DeepSeek Harness 的外部管理器

用法：dsdock <命令> [参数] [选项]

图形界面
  app                        启动桌面版：菜单栏常驻图标 + 主窗口（DSDock.app 双击即是这个）
  gui                        在浏览器里打开 DSDock（--port <端口> 固定端口，--no-open 不自动打开）

查看
  status                     DSH 环境和所有 profile 的健康状态
  outdated -p <profile>      有哪些插件可以更新（只列兼容当前 DSH 的版本）
  check <规格> -p <profile>   只做装前检查，不安装
  log -p <profile>           操作记录

插件（每次改动都会先拍快照，失败自动回滚）
  add <规格> -p <profile>     安装插件。规格可以是 npm 包名[@版本]、本地目录或 git 地址
  remove <包名> -p <profile>  卸载插件
  update <包名> -p <profile>  更新到兼容当前 DSH 的最新版本（--to <版本> 指定版本）
  disable <id> -p <profile>  禁用一行插件配置，不卸载
  enable <id> -p <profile>   重新启用

快照
  snap list -p <profile>     列出快照
  snap take -p <profile>     手动拍一个快照
  snap restore <快照id>       恢复到某个快照（恢复本身也可以撤销）

诊断
  smoke -p <profile>         真正启动一次 profile，检查每个插件是否激活
  rescue -p <profile>        DSH 出问题时用：找出原因，可以一键回到上一个正常的状态
                             （--no-boot 只做快速检查，不启动）

DSH 版本
  dsh list [--available]     已安装的版本；--available 列出可以安装的版本
  dsh install <版本|渠道>     安装（渠道：latest、next、alpha）；第一个装上的版本自动设为默认
  dsh use <版本>              切换默认版本（先检查插件兼容性和会话格式）
  dsh pin <版本> -p <profile> 让某个 profile 固定使用一个版本；dsh unpin -p <profile> 取消
  dsh which [-p <profile>]   现在会用哪个版本、为什么
  dsh adopt [路径]            接管已有的安装（比如 npm i -g 装的），不移动、不删除
  dsh uninstall <版本>        卸载
  dsh shim                   安装 dsh 转发器：在终端里敲 dsh 时自动选对版本

选项
  -p, --profile <名字>        要操作的 profile
  --smoke                    改动后再做一次启动测试
  --pre                      允许选择预发布版本
  --registry <地址>           npm 仓库（默认 registry.npmjs.org）
  -y, --yes                  有警告时不再询问，直接继续
  --force                    有阻止级别的检查结果时也继续（不推荐）
  --dry-run                  只检查，不改动
  --json                     输出 JSON

环境变量
  DSH_HOME                   DSH 的数据目录（默认 ~/.dsh）
  DSDOCK_DSH                 dsh 的 lib/bin.js 或可执行文件（优先于 DSDock 管理的版本）
  DSDOCK_DSH_VERSION         临时指定用哪个 DSDock 管理的版本
  DSDOCK_NODE                运行 bin.js 的 node
  DSDOCK_HOME                DSDock 自己的数据目录
`

// Main runs the CLI and returns the process exit code.
func Main(argv []string) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	return run(ctx, argv, os.Stdout, os.Stderr, os.Stdin)
}

type app struct {
	a      *args
	out    io.Writer
	errw   io.Writer
	in     io.Reader
	core   *core.Core
	home   string
	env    *dshenv.Env // for commands that work on one profile
	envWhy string
	engine *txn.Engine
}

func run(ctx context.Context, argv []string, out, errw io.Writer, in io.Reader) int {
	a, err := parseArgs(argv)
	if err != nil {
		fmt.Fprintln(errw, "dsdock:", err)
		return 2
	}
	if len(a.pos) == 0 || a.is("help") || a.pos[0] == "help" {
		fmt.Fprint(out, usage)
		return 0
	}
	if a.pos[0] == "version" {
		fmt.Fprintln(out, "dsdock", Version)
		return 0
	}
	ap := &app{a: a, out: out, errw: errw, in: in}
	defer ap.close()
	if err := ap.dispatch(ctx); err != nil {
		var ue usageError
		if errors.As(err, &ue) {
			fmt.Fprintln(errw, "dsdock:", err)
			return 2
		}
		// An exitError is an outcome that has already been reported.
		var ex exitError
		if errors.As(err, &ex) {
			return int(ex)
		}
		if ap.a.is("json") {
			ap.json(map[string]string{"error": err.Error()})
		} else {
			fmt.Fprintln(errw, "dsdock:", err)
		}
		return 1
	}
	return 0
}

type usageError string

func (u usageError) Error() string { return string(u) }

// exitError carries a specific exit code for outcomes that are not crashes,
// such as a change that was rolled back.
type exitError int

func (e exitError) Error() string { return fmt.Sprintf("exit %d", int(e)) }

func (ap *app) dispatch(ctx context.Context) error {
	cmd, rest := ap.a.pos[0], ap.a.pos[1:]
	type handler struct {
		run     func(context.Context, []string) error
		needEnv bool // resolves the profile's dsh before the command starts
	}
	handlers := map[string]handler{
		"status":   {ap.cmdStatus, false},
		"outdated": {ap.cmdOutdated, false},
		"check":    {ap.cmdCheck, false},
		"add":      {ap.cmdAdd, false},
		"remove":   {ap.cmdRemove, false},
		"update":   {ap.cmdUpdate, false},
		"disable":  {func(ctx context.Context, r []string) error { return ap.cmdToggle(ctx, r, true) }, false},
		"enable":   {func(ctx context.Context, r []string) error { return ap.cmdToggle(ctx, r, false) }, false},
		"snap":     {ap.cmdSnap, true},
		"smoke":    {ap.cmdSmoke, true},
		"rescue":   {ap.cmdRescue, true},
		"log":      {ap.cmdLog, false},
		"dsh":      {ap.cmdDSH, false},
		"gui":      {ap.cmdGUI, false},
		"app":      {ap.cmdApp, false},
	}
	h, ok := handlers[cmd]
	if !ok {
		return usageError(fmt.Sprintf("未知命令 %q，运行 dsdock help 查看用法", cmd))
	}
	c, err := core.Open(ap.a.str("registry"))
	if err != nil {
		return err
	}
	ap.core, ap.home = c, c.Home
	if !ap.a.is("json") {
		c.SetProgress(func(msg string) { fmt.Fprintln(ap.errw, "  ·", msg) })
	}
	if h.needEnv {
		env, why, err := c.EnvFor(ap.a.str("profile"))
		if err != nil {
			return err
		}
		ap.env, ap.envWhy, ap.engine = env, why, c.Engine(env)
	}
	return h.run(ctx, rest)
}

func (ap *app) close() {
	if ap.core != nil {
		ap.core.Close()
	}
}

func (ap *app) profileName() (string, error) {
	name := ap.a.str("profile")
	if name == "" {
		return "", usageError("需要用 -p <profile> 指定 profile")
	}
	return name, nil
}

func (ap *app) json(v any) {
	enc := json.NewEncoder(ap.out)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

func (ap *app) printf(format string, args ...any) {
	if !ap.a.is("json") {
		fmt.Fprintf(ap.out, format, args...)
	}
}

// confirm asks before going on with warnings. Without a terminal it refuses
// unless --yes was given, so scripts never hang on a prompt.
func (ap *app) confirm(question string) bool {
	if ap.a.is("yes") {
		return true
	}
	if f, ok := ap.in.(*os.File); !ok || !isTerminal(f) {
		fmt.Fprintln(ap.errw, "需要确认才能继续；在非交互环境里请加 --yes")
		return false
	}
	fmt.Fprintf(ap.errw, "%s [y/N] ", question)
	line, _ := bufio.NewReader(ap.in).ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes", "是":
		return true
	}
	return false
}

// isTerminal must not trust ModeCharDevice: /dev/null is a character device
// too, and a script that redirects stdin from it must never be prompted.
func isTerminal(f *os.File) bool {
	return isatty.IsTerminal(f.Fd()) || isatty.IsCygwinTerminal(f.Fd())
}

// report prints a change's outcome and turns failures into exit codes:
// 0 applied, 3 rolled back, 4 failed and could not be rolled back.
func (ap *app) report(out *txn.Outcome) error {
	if ap.a.is("json") {
		ap.json(out)
	} else {
		switch {
		case out.Applied:
			fmt.Fprintf(ap.out, "✓ 完成（快照 %s 可撤销这次改动）\n", out.Before.ID)
		case out.RolledBack:
			fmt.Fprintf(ap.out, "✗ 已回滚：%s\n", out.Reason)
		default:
			fmt.Fprintf(ap.out, "✗ 失败：%s\n", out.Reason)
		}
		for _, w := range out.Warnings {
			fmt.Fprintf(ap.out, "  ⚠ %s\n", w)
		}
		if out.Smoke != nil && out.Applied {
			fmt.Fprintf(ap.out, "  启动测试通过（%s）\n", out.Smoke.Duration.Round(1e6))
		}
		for _, r := range out.Running {
			fmt.Fprintf(ap.out, "  ↻ profile %s 正在运行（PID %d），重启后才会生效\n", r.Profile, r.PID)
		}
		if !out.Applied {
			fmt.Fprintf(ap.out, "  详细日志：dsdock log -p %s\n", out.Op.Profile)
		}
	}
	switch {
	case out.Applied:
		return nil
	case out.RolledBack:
		return exitError(3)
	default:
		return exitError(4)
	}
}

func (ap *app) txnOptions() txn.Options {
	return txn.Options{Smoke: ap.a.is("smoke"), SmokeOpts: proc.DefaultSmoke}
}
