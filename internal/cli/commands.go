package cli

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/hikariming/dsdock/internal/core"
	"github.com/hikariming/dsdock/internal/preflight"
	"github.com/hikariming/dsdock/internal/proc"
	"github.com/hikariming/dsdock/internal/versions"
)

// sourceText explains where a dsh came from.
var sourceText = map[string]string{
	versions.ByPin: "这个 profile 固定了版本", versions.ByDefault: "默认版本",
	versions.ByExternal: "接管的外部安装", versions.ByEnv: "环境变量 DSDOCK_DSH_VERSION",
	core.ByEnvEntry: "环境变量 DSDOCK_DSH", core.ByPath: "PATH 里的 dsh，DSDock 还没有管理任何版本",
}

// ---------- status / outdated ----------

func (ap *app) cmdStatus(ctx context.Context, _ []string) error {
	st, err := ap.core.Status(ctx, ap.a.str("profile"))
	if err != nil {
		return err
	}
	if ap.a.is("json") {
		ap.json(st)
		return nil
	}
	if st.DSH.Error != "" {
		fmt.Fprintf(ap.out, "DSH：%s\n", st.DSH.Error)
	} else {
		fmt.Fprintf(ap.out, "DSH %s（%s）   DSH_HOME %s\n", st.DSH.Version, sourceText[st.DSH.Source], st.DSH.Home)
		if st.DSH.NodeProblem != "" {
			fmt.Fprintf(ap.out, "⚠ %s\n", st.DSH.NodeProblem)
		}
	}
	if len(st.Profiles) == 0 {
		fmt.Fprintln(ap.out, "\n还没有 profile。")
		return nil
	}
	for _, ps := range st.Profiles {
		mark := "✓"
		if !ps.Healthy {
			mark = "✗"
		} else if ps.Warnings() > 0 {
			mark = "⚠"
		}
		web, pin := "", ""
		if ps.Web {
			web = "  [web]"
		}
		if ps.DSHSource == versions.ByPin {
			pin = fmt.Sprintf("  固定 DSH %s", ps.DSH)
		}
		fmt.Fprintf(ap.out, "\n%s %s%s%s\n", mark, ps.Name, web, pin)
		for _, pl := range ps.Plugins {
			v := pl.Version
			if v == "" {
				v = pl.Spec
			}
			fmt.Fprintf(ap.out, "    %s  %s\n", pl.Name, v)
		}
		if len(ps.Plugins) == 0 {
			fmt.Fprintln(ap.out, "    （没有装外部插件）")
		}
		if ps.Error != "" {
			fmt.Fprintf(ap.out, "    ✗ 无法组装：%s\n", ps.Error)
		}
		for _, d := range ps.Duplicates {
			fmt.Fprintf(ap.out, "    ⚠ id %q 被 %s 重复插入，只有 %s 生效\n", d.ID, strings.Join(d.Sources, "、"), d.Winner())
		}
		for _, sk := range ps.Skipped {
			fmt.Fprintf(ap.out, "    ⚠ %s 被 DSH 跳过，没有生效：%s\n", sk.Package, sk.Short())
		}
		for _, u := range ps.Unmatched {
			fmt.Fprintf(ap.out, "    ⚠ 覆盖找不到目标 %q（%s）\n", u.ID, u.File)
		}
		for _, r := range ps.Running {
			fmt.Fprintf(ap.out, "    ▶ 正在运行，PID %d\n", r.PID)
		}
		if ps.LastSnap != "" {
			fmt.Fprintf(ap.out, "    最近快照 %s\n", ps.LastSnap)
		}
	}
	return nil
}

func (ap *app) cmdOutdated(ctx context.Context, _ []string) error {
	name, err := ap.profileName()
	if err != nil {
		return err
	}
	ups, err := ap.core.Outdated(ctx, name, ap.a.is("pre"))
	if err != nil {
		return err
	}
	if ap.a.is("json") {
		ap.json(ups)
		return nil
	}
	if len(ups) == 0 {
		fmt.Fprintln(ap.out, "✓ 所有 npm 插件都已经是兼容当前 DSH 的最新版本")
		return nil
	}
	for _, u := range ups {
		if u.Error != "" {
			fmt.Fprintf(ap.out, "  %s  %s  （查不到：%s）\n", u.Name, u.Current, u.Error)
			continue
		}
		fmt.Fprintf(ap.out, "  %s  %s → %s\n", u.Name, u.Current, u.Latest)
	}
	fmt.Fprintf(ap.out, "更新：dsdock update <包名> -p %s\n", name)
	return nil
}

// ---------- plans: check / add / update / remove / toggle ----------

func (ap *app) printReport(r *preflight.Report) {
	if ap.a.is("json") || r == nil {
		return
	}
	c := r.Candidate
	what := c.Arg()
	if c.Name != "" && c.Version != "" {
		what = c.Name + "@" + c.Version
	}
	fmt.Fprintf(ap.out, "%s（%s）\n", what, c.Spec.Kind)
	if len(r.Findings) == 0 {
		fmt.Fprintln(ap.out, "  ✓ 检查全部通过")
	}
	for _, f := range r.Findings {
		mark := map[preflight.Level]string{preflight.Block: "✗", preflight.Warn: "⚠", preflight.Info: "·"}[f.Level]
		fmt.Fprintf(ap.out, "  %s %s %s\n", mark, f.Rule, f.Message)
	}
}

// gate turns a plan into go / no-go, asking when the plan needs it.
func (ap *app) gate(p *core.Plan) error {
	for _, n := range p.Notes {
		ap.printf("  ⚠ %s\n", n)
	}
	if p.Blocked() && !ap.a.is("force") {
		return exitError(5)
	}
	if p.Blocked() {
		fmt.Fprintln(ap.errw, "⚠ 已用 --force 忽略阻止级别的检查结果")
	}
	if p.NeedsConfirmation() && !ap.confirm("有警告，仍然继续吗？") {
		return exitError(5)
	}
	return nil
}

// apply runs a confirmed plan, honouring --dry-run and --json.
func (ap *app) apply(ctx context.Context, p *core.Plan) error {
	if err := ap.gate(p); err != nil {
		if ap.a.is("json") {
			ap.json(p)
		}
		return err
	}
	if ap.a.is("dry-run") {
		if ap.a.is("json") {
			ap.json(p)
		} else if p.Preview != "" {
			fmt.Fprint(ap.out, p.Preview)
		}
		return nil
	}
	out, err := p.Apply(ctx, ap.a.is("smoke"))
	if err != nil {
		return err
	}
	return ap.report(out)
}

func (ap *app) cmdCheck(ctx context.Context, rest []string) error {
	if len(rest) != 1 {
		return usageError("用法：dsdock check <规格> -p <profile>")
	}
	name, err := ap.profileName()
	if err != nil {
		return err
	}
	cwd, _ := os.Getwd()
	r, err := ap.core.Check(ctx, name, rest[0], cwd, ap.a.is("pre"))
	if err != nil {
		return err
	}
	if ap.a.is("json") {
		ap.json(r)
	}
	ap.printReport(r)
	if r.Blocked() {
		return exitError(5)
	}
	return nil
}

func (ap *app) cmdAdd(ctx context.Context, rest []string) error {
	if len(rest) != 1 {
		return usageError("用法：dsdock add <规格> -p <profile>")
	}
	name, err := ap.profileName()
	if err != nil {
		return err
	}
	cwd, _ := os.Getwd()
	p, err := ap.core.PlanAdd(ctx, name, rest[0], cwd, ap.a.is("pre"))
	if err != nil {
		return err
	}
	ap.printReport(p.Report)
	return ap.apply(ctx, p)
}

func (ap *app) cmdUpdate(ctx context.Context, rest []string) error {
	if len(rest) != 1 {
		return usageError("用法：dsdock update <包名> -p <profile> [--to <版本>]")
	}
	name, err := ap.profileName()
	if err != nil {
		return err
	}
	p, err := ap.core.PlanUpdate(ctx, name, rest[0], ap.a.str("to"), ap.a.is("pre"))
	if err != nil {
		return err
	}
	if p.NoChange {
		if ap.a.is("json") {
			ap.json(map[string]string{"name": rest[0], "version": p.From, "status": "up-to-date"})
		} else {
			fmt.Fprintln(ap.out, p.Reason)
		}
		return nil
	}
	ap.printf("%s：%s → %s\n", rest[0], p.From, p.To)
	ap.printReport(p.Report)
	return ap.apply(ctx, p)
}

func (ap *app) cmdRemove(ctx context.Context, rest []string) error {
	if len(rest) != 1 {
		return usageError("用法：dsdock remove <包名> -p <profile>")
	}
	name, err := ap.profileName()
	if err != nil {
		return err
	}
	p, err := ap.core.PlanRemove(ctx, name, rest[0])
	if err != nil {
		return err
	}
	return ap.apply(ctx, p)
}

func (ap *app) cmdToggle(ctx context.Context, rest []string, disable bool) error {
	if len(rest) != 1 {
		verb := map[bool]string{true: "disable", false: "enable"}[disable]
		return usageError(fmt.Sprintf("用法：dsdock %s <id> -p <profile>", verb))
	}
	name, err := ap.profileName()
	if err != nil {
		return err
	}
	p, err := ap.core.PlanToggle(ctx, name, rest[0], disable)
	if err != nil {
		return err
	}
	if p.NoChange {
		ap.printf("%s\n", p.Reason)
		return nil
	}
	return ap.apply(ctx, p)
}

// ---------- snap ----------

func (ap *app) cmdSnap(ctx context.Context, rest []string) error {
	if len(rest) == 0 {
		return usageError("用法：dsdock snap list|take|restore")
	}
	store := ap.core.Store
	switch rest[0] {
	case "list":
		name, err := ap.profileName()
		if err != nil {
			return err
		}
		limit, _ := strconv.Atoi(ap.a.str("limit"))
		if limit == 0 {
			limit = 20
		}
		snaps, err := store.List(ap.home, name, limit)
		if err != nil {
			return err
		}
		if ap.a.is("json") {
			ap.json(snaps)
			return nil
		}
		if len(snaps) == 0 {
			fmt.Fprintln(ap.out, "还没有快照")
		}
		for _, s := range snaps {
			mark := " "
			if s.Verified {
				mark = "✓"
			}
			kind := ""
			if s.Manual {
				kind = "  [手动]"
			}
			fmt.Fprintf(ap.out, "%s %s  %s  DSH %s  %s%s\n", mark, s.ID, s.CreatedAt.Local().Format("01-02 15:04:05"), s.DSHVersion, s.Reason, kind)
		}
		return nil
	case "take":
		name, err := ap.profileName()
		if err != nil {
			return err
		}
		if _, err := ap.core.LoadProfile(name); err != nil {
			return err
		}
		s, err := ap.core.TakeSnapshot(ctx, ap.engine, name)
		if err != nil {
			return err
		}
		if ap.a.is("json") {
			ap.json(s)
			return nil
		}
		fmt.Fprintf(ap.out, "✓ 快照 %s\n", s.ID)
		return nil
	case "restore":
		if len(rest) != 2 {
			return usageError("用法：dsdock snap restore <快照id>")
		}
		target, err := store.Get(rest[1])
		if err != nil {
			return err
		}
		ap.printf("恢复 %s 到 %s 的状态（%s）\n", target.Profile, target.CreatedAt.Local().Format("01-02 15:04:05"), target.Reason)
		if !ap.confirm("确定恢复吗？") {
			return exitError(5)
		}
		eng, err := ap.core.EngineFor(target.Profile)
		if err != nil {
			return err
		}
		out, err := eng.Restore(ctx, target.ID, ap.txnOptions())
		if err != nil {
			return err
		}
		return ap.report(out)
	}
	return usageError("用法：dsdock snap list|take|restore")
}

// ---------- smoke / log ----------

func (ap *app) cmdSmoke(ctx context.Context, _ []string) error {
	name, err := ap.profileName()
	if err != nil {
		return err
	}
	p, err := ap.core.LoadProfile(name)
	if err != nil {
		return err
	}
	for _, r := range ap.core.Running(ctx, []string{p.Name}) {
		ap.printf("  · %s 已经在运行（PID %d），这次测试会用另一个端口再启动一份\n", p.Name, r.PID)
	}
	ap.printf("启动 %s …\n", p.Name)
	res, err := proc.Smoke(ctx, ap.env, p, proc.DefaultSmoke)
	if err != nil {
		return err
	}
	if ap.a.is("json") {
		ap.json(res)
	} else {
		switch {
		case res.OK():
			fmt.Fprintf(ap.out, "✓ 启动成功，所有插件都已激活（%s）\n", res.Duration.Round(time.Millisecond))
		case res.Ready:
			fmt.Fprintf(ap.out, "⚠ 启动成功，但有 %d 个插件没有激活：\n", len(res.Inactive))
		case res.Exited:
			fmt.Fprintf(ap.out, "✗ DSH 启动后退出了（退出码 %d）\n%s\n", res.ExitCode, lastLines(res.Stderr, 10))
		case res.TimedOut:
			fmt.Fprintln(ap.out, "✗ 超时：DSH 没有就绪")
		}
		for _, in := range res.Inactive {
			fmt.Fprintf(ap.out, "  - %s（%s）：%s\n", in.ID, in.Module, in.Reason)
		}
	}
	if !res.OK() {
		return exitError(6)
	}
	return nil
}

func (ap *app) cmdLog(ctx context.Context, _ []string) error {
	name := ap.a.str("profile")
	limit, _ := strconv.Atoi(ap.a.str("limit"))
	ops, err := ap.core.Store.Ops(ap.home, name, limit)
	if err != nil {
		return err
	}
	if ap.a.is("json") {
		ap.json(ops)
		return nil
	}
	if len(ops) == 0 {
		fmt.Fprintln(ap.out, "还没有操作记录")
	}
	for i, op := range ops {
		fmt.Fprintf(ap.out, "%s  %-8s %-10s %s  [%s]\n", op.StartedAt.Local().Format("01-02 15:04:05"), op.Kind, op.Profile, op.Spec, op.Status)
		if i == 0 && name != "" && op.Status != "applied" {
			fmt.Fprintln(ap.out, indent(lastLines(op.Log, 40), "    "))
		}
	}
	return nil
}

// ---------- helpers ----------

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

func indent(s, prefix string) string {
	return prefix + strings.ReplaceAll(s, "\n", "\n"+prefix)
}
