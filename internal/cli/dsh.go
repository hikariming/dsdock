package cli

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/hikariming/dsdock/internal/core"
	"github.com/hikariming/dsdock/internal/shim"
	"github.com/hikariming/dsdock/internal/versions"
)

func (ap *app) cmdDSH(ctx context.Context, rest []string) error {
	if len(rest) == 0 {
		return usageError("用法：dsdock dsh list|install|use|pin|unpin|which|adopt|uninstall|shim")
	}
	vers := ap.core.Vers
	sub, args := rest[0], rest[1:]
	switch sub {
	case "list", "ls":
		return ap.dshList(ctx)
	case "install":
		if len(args) != 1 {
			return usageError("用法：dsdock dsh install <版本|latest|next|alpha>")
		}
		return ap.dshInstall(ctx, args[0])
	case "use":
		if len(args) != 1 {
			return usageError("用法：dsdock dsh use <版本>")
		}
		return ap.dshSwitch(ctx, args[0], "")
	case "pin":
		if len(args) != 1 {
			return usageError("用法：dsdock dsh pin <版本> -p <profile>")
		}
		name, err := ap.profileName()
		if err != nil {
			return err
		}
		return ap.dshSwitch(ctx, args[0], name)
	case "unpin":
		name, err := ap.profileName()
		if err != nil {
			return err
		}
		if err := vers.Unpin(ap.home, name); err != nil {
			return err
		}
		ap.printf("✓ %s 不再固定版本，跟随默认版本\n", name)
		return nil
	case "which":
		return ap.dshWhich(ctx)
	case "adopt":
		path := ""
		if len(args) == 1 {
			path = args[0]
		}
		inst, err := vers.Adopt(ctx, path, ap.core.ShimDir())
		if err != nil {
			return err
		}
		if ap.a.is("json") {
			ap.json(inst)
			return nil
		}
		fmt.Fprintf(ap.out, "✓ 已接管 DSH %s（%s），名字是 %q。原来的安装没有移动、也没有删除\n", inst.Version, inst.Dir, versions.External)
		if def, _ := vers.Default(); def == "" {
			fmt.Fprintln(ap.out, "  没有设置默认版本时，DSDock 会用它")
		}
		return nil
	case "uninstall", "rm":
		if len(args) != 1 {
			return usageError("用法：dsdock dsh uninstall <版本>")
		}
		if err := vers.Uninstall(args[0]); err != nil {
			return err
		}
		ap.printf("✓ 已卸载 DSH %s\n", args[0])
		return nil
	case "shim":
		p, err := shim.Install(ap.core.ShimDir())
		if err != nil {
			return err
		}
		if ap.a.is("json") {
			ap.json(map[string]string{"shim": p, "dir": ap.core.ShimDir()})
			return nil
		}
		fmt.Fprintf(ap.out, "✓ 转发器已写入 %s\n%s\n", p, shim.PathHint(ap.core.ShimDir()))
		return nil
	}
	return usageError(fmt.Sprintf("未知的 dsh 子命令 %q", sub))
}

func (ap *app) dshList(ctx context.Context) error {
	vers := ap.core.Vers
	installed, err := vers.List()
	if err != nil {
		return err
	}
	def, _ := vers.Default()
	pins, _ := vers.Pins("")
	pinned := map[string][]string{}
	for _, p := range pins {
		if p.Home == ap.home {
			pinned[p.Name] = append(pinned[p.Name], p.Profile)
		}
	}
	var avail []versions.Release
	if ap.a.is("available") || ap.a.is("pre") {
		if avail, err = vers.Available(ctx); err != nil {
			return err
		}
	}
	if ap.a.is("json") {
		ap.json(map[string]any{"installed": installed, "default": def, "pins": pins, "available": avail})
		return nil
	}
	if len(installed) == 0 {
		fmt.Fprintln(ap.out, "DSDock 还没有管理任何 DSH 版本。安装：dsdock dsh install latest；接管现有安装：dsdock dsh adopt")
	}
	for _, i := range installed {
		mark := " "
		if i.Name == def {
			mark = "*"
		}
		extra := ""
		if i.Name == versions.External {
			extra = "  [外部：" + i.Dir + "]"
		}
		if ps := pinned[i.Name]; len(ps) > 0 {
			extra += "  固定：" + strings.Join(ps, "、")
		}
		if i.SessionFormat > 0 {
			extra += fmt.Sprintf("  会话格式 v%d", i.SessionFormat)
		}
		fmt.Fprintf(ap.out, "%s %-14s %s%s\n", mark, i.Name, i.InstalledAt.Local().Format("2006-01-02"), extra)
	}
	if def != "" {
		fmt.Fprintln(ap.out, "（* 是默认版本）")
	}
	if len(avail) > 0 {
		fmt.Fprintln(ap.out, "\n可以安装的版本：")
		shown := 0
		for _, r := range avail {
			if r.Deprecated || (!ap.a.is("pre") && len(r.Tags) == 0 && shown >= 8) {
				continue
			}
			tags, done := "", ""
			if len(r.Tags) > 0 {
				tags = "  [" + strings.Join(r.Tags, ", ") + "]"
			}
			if r.Installed {
				done = "  已安装"
			}
			fmt.Fprintf(ap.out, "  %-16s %s%s%s\n", r.Version, r.Published.Local().Format("2006-01-02"), tags, done)
			if shown++; shown >= 15 {
				break
			}
		}
	}
	return nil
}

func (ap *app) dshInstall(ctx context.Context, want string) error {
	vers := ap.core.Vers
	inst, fresh, err := vers.Install(ctx, want)
	if err != nil {
		return err
	}
	def, _ := vers.Default()
	madeDefault := false
	if def == "" {
		if err := vers.SetDefault(inst.Name); err != nil {
			return err
		}
		madeDefault = true
	}
	if ap.a.is("json") {
		ap.json(map[string]any{"installed": inst, "new": fresh, "default": madeDefault})
		return nil
	}
	if fresh {
		fmt.Fprintf(ap.out, "✓ 已安装 DSH %s\n", inst.Version)
	} else {
		fmt.Fprintf(ap.out, "DSH %s 已经装过了\n", inst.Version)
	}
	if madeDefault {
		fmt.Fprintln(ap.out, "  已设为默认版本。要在终端里直接用 dsh，运行 dsdock dsh shim")
	} else if def != inst.Name {
		fmt.Fprintf(ap.out, "  切换过去：dsdock dsh use %s\n", inst.Name)
	}
	return nil
}

// dshSwitch is `use` (profileName empty) or `pin`.
func (ap *app) dshSwitch(ctx context.Context, name, profileName string) error {
	inst, installed, err := ap.core.Ensure(ctx, name)
	if err != nil {
		return err
	}
	if installed {
		ap.printf("✓ 已安装 DSH %s\n", inst.Version)
	}
	var plan *core.SwitchPlan
	if profileName == "" {
		plan, err = ap.core.PlanUse(ctx, inst.Name)
	} else {
		plan, err = ap.core.PlanPin(ctx, profileName, inst.Name)
	}
	if err != nil {
		return err
	}
	if plan.Same {
		if profileName == "" {
			ap.printf("默认版本已经是 %s\n", inst.Name)
		} else {
			ap.printf("%s 已经固定在 %s\n", profileName, inst.Name)
		}
		return nil
	}
	ap.printSwitch(plan.Report)
	if plan.NeedsConfirmation() && !ap.confirm(map[bool]string{true: "仍然切换吗？", false: "仍然固定到这个版本吗？"}[profileName == ""]) {
		return exitError(5)
	}
	if err := plan.Apply(); err != nil {
		return err
	}
	if ap.a.is("json") {
		ap.json(plan.Report)
		return nil
	}
	if profileName == "" {
		fmt.Fprintf(ap.out, "✓ 默认版本：%s\n", inst.Name)
		if plan.Report.From != "" {
			fmt.Fprintf(ap.out, "  撤销：dsdock dsh use %s\n", plan.Report.From)
		}
	} else {
		fmt.Fprintf(ap.out, "✓ %s 固定使用 DSH %s（撤销：dsdock dsh unpin -p %s）\n", profileName, inst.Name, profileName)
	}
	for _, r := range ap.core.Running(ctx, plan.Affected()) {
		fmt.Fprintf(ap.out, "  ↻ %s 正在运行（PID %d），重启后才会换成新版本\n", r.Profile, r.PID)
	}
	return nil
}

func (ap *app) printSwitch(r *versions.SwitchReport) {
	if ap.a.is("json") {
		return
	}
	from := r.From
	if from == "" {
		from = "（无）"
	}
	fmt.Fprintf(ap.out, "%s → %s\n", from, r.To)
	if r.SessionDowngrade {
		fmt.Fprintf(ap.out, "  ⚠ 降级：目标版本使用会话格式 v%d，当前是 v%d。在新版本里创建或继续过的会话，旧版本打不开；更早的会话还保留着旧格式的原文件，可以看，但新版本里聊过的部分看不到\n", r.ToFormat, r.FromFormat)
	}
	if len(r.Profiles) == 0 {
		fmt.Fprintln(ap.out, "  没有受影响的 profile")
	}
	for _, pc := range r.Profiles {
		if pc.OK() {
			fmt.Fprintf(ap.out, "  ✓ %s\n", pc.Profile)
			continue
		}
		fmt.Fprintf(ap.out, "  ✗ %s\n", pc.Profile)
		for _, b := range sortedKeys(pc.Skipped) {
			fmt.Fprintf(ap.out, "      %s 会被 DSH 跳过：%s\n", b, pc.Skipped[b])
		}
		names := make([]string, 0, len(pc.Incompatible))
		for b := range pc.Incompatible {
			names = append(names, b)
		}
		sort.Strings(names)
		for _, b := range names {
			fmt.Fprintf(ap.out, "      %s 不兼容：要求 %s，DSH 会拒绝加载它\n", b, pc.Incompatible[b])
		}
		for _, b := range pc.Unreadable {
			fmt.Fprintf(ap.out, "      %s 的兼容性声明无法读取，DSH 会拒绝加载它\n", b)
		}
		if pc.ComposeError != "" {
			fmt.Fprintf(ap.out, "      用目标版本无法组装：%s\n", pc.ComposeError)
		}
	}
}

func (ap *app) dshWhich(ctx context.Context) error {
	name := ap.a.str("profile")
	env, why, err := ap.core.EnvFor(name)
	if err != nil {
		return err
	}
	ver, _ := env.Version(ctx)
	if ap.a.is("json") {
		ap.json(map[string]string{"profile": name, "version": ver, "entry": env.Entry, "reason": why})
		return nil
	}
	target := "默认"
	if name != "" {
		target = name
	}
	fmt.Fprintf(ap.out, "%s → DSH %s（%s）\n  %s\n", target, ver, sourceText[why], env.Entry)
	return nil
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
