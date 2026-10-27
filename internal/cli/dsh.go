package cli

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/hikariming/dsdock/internal/proc"
	"github.com/hikariming/dsdock/internal/profile"
	"github.com/hikariming/dsdock/internal/shim"
	"github.com/hikariming/dsdock/internal/versions"
)

func (ap *app) cmdDSH(ctx context.Context, rest []string) error {
	if len(rest) == 0 {
		return usageError("用法：dsdock dsh list|install|use|pin|unpin|which|adopt|uninstall|shim")
	}
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
		return ap.dshUse(ctx, args[0])
	case "pin":
		if len(args) != 1 {
			return usageError("用法：dsdock dsh pin <版本> -p <profile>")
		}
		return ap.dshPin(ctx, args[0])
	case "unpin":
		name, err := ap.profileName()
		if err != nil {
			return err
		}
		if err := ap.vers.Unpin(ap.home, name); err != nil {
			return err
		}
		ap.printf("✓ %s 不再固定版本，跟随默认版本\n", name)
		return nil
	case "which":
		return ap.dshWhich()
	case "adopt":
		path := ""
		if len(args) == 1 {
			path = args[0]
		}
		inst, err := ap.vers.Adopt(ctx, path, ap.shimDir())
		if err != nil {
			return err
		}
		if ap.a.is("json") {
			ap.json(inst)
			return nil
		}
		fmt.Fprintf(ap.out, "✓ 已接管 DSH %s（%s），名字是 %q。原来的安装没有移动、也没有删除\n", inst.Version, inst.Dir, versions.External)
		if def, _ := ap.vers.Default(); def == "" {
			fmt.Fprintln(ap.out, "  没有设置默认版本时，DSDock 会用它")
		}
		return nil
	case "uninstall", "rm":
		if len(args) != 1 {
			return usageError("用法：dsdock dsh uninstall <版本>")
		}
		if err := ap.vers.Uninstall(args[0]); err != nil {
			return err
		}
		ap.printf("✓ 已卸载 DSH %s\n", args[0])
		return nil
	case "shim":
		p, err := shim.Install(ap.shimDir())
		if err != nil {
			return err
		}
		if ap.a.is("json") {
			ap.json(map[string]string{"shim": p, "dir": ap.shimDir()})
			return nil
		}
		fmt.Fprintf(ap.out, "✓ 转发器已写入 %s\n%s\n", p, shim.PathHint(ap.shimDir()))
		return nil
	}
	return usageError(fmt.Sprintf("未知的 dsh 子命令 %q", sub))
}

func (ap *app) dshList(ctx context.Context) error {
	installed, err := ap.vers.List()
	if err != nil {
		return err
	}
	def, _ := ap.vers.Default()
	pins, _ := ap.vers.Pins("")
	pinned := map[string][]string{}
	for _, p := range pins {
		if p.Home == ap.home {
			pinned[p.Name] = append(pinned[p.Name], p.Profile)
		}
	}
	var avail []versions.Release
	if ap.a.is("available") || ap.a.is("pre") {
		if avail, err = ap.vers.Available(ctx); err != nil {
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
			tags := ""
			if len(r.Tags) > 0 {
				tags = "  [" + strings.Join(r.Tags, ", ") + "]"
			}
			done := ""
			if r.Installed {
				done = "  已安装"
			}
			fmt.Fprintf(ap.out, "  %-16s %s%s%s\n", r.Version, r.Published.Local().Format("2006-01-02"), tags, done)
			shown++
			if shown >= 15 {
				break
			}
		}
	}
	return nil
}

func (ap *app) dshInstall(ctx context.Context, want string) error {
	inst, fresh, err := ap.vers.Install(ctx, want)
	if err != nil {
		return err
	}
	def, _ := ap.vers.Default()
	madeDefault := false
	if def == "" {
		if err := ap.vers.SetDefault(inst.Name); err != nil {
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
		fmt.Fprintf(ap.out, "  已设为默认版本。要在终端里直接用 dsh，运行 dsdock dsh shim\n")
	} else if def != inst.Name {
		fmt.Fprintf(ap.out, "  切换过去：dsdock dsh use %s\n", inst.Name)
	}
	return nil
}

// ensure returns an installed release, installing a version or channel
// first when needed.
func (ap *app) ensure(ctx context.Context, name string) (*versions.Installed, error) {
	inst, err := ap.vers.Get(name)
	if err == nil {
		return inst, nil
	}
	if !errors.Is(err, versions.ErrNotInstalled) || name == versions.External {
		return nil, err
	}
	ap.printf("DSH %s 还没有安装，先安装\n", name)
	inst, _, err = ap.vers.Install(ctx, name)
	return inst, err
}

func (ap *app) dshUse(ctx context.Context, name string) error {
	to, err := ap.ensure(ctx, name)
	if err != nil {
		return err
	}
	from, _, err := ap.vers.Resolve(ap.home, "")
	if err != nil && !errors.Is(err, versions.ErrNoVersion) {
		return err
	}
	if from != nil && from.Name == to.Name {
		ap.printf("默认版本已经是 %s\n", to.Name)
		return nil
	}
	// Profiles with a pin keep their release; everything else follows the
	// default and is what this switch affects.
	all, err := profile.List(ap.home)
	if err != nil {
		return err
	}
	var affected []*profile.Profile
	for _, p := range all {
		if pin, _ := ap.vers.PinOf(ap.home, p.Name); pin == "" {
			affected = append(affected, p)
		}
	}
	report, err := versions.Precheck(ctx, ap.home, affected, from, to)
	if err != nil {
		return err
	}
	ap.printSwitch(report)
	if report.Problems() && !ap.confirm("仍然切换吗？") {
		return exitError(5)
	}
	if err := ap.vers.SetDefault(to.Name); err != nil {
		return err
	}
	if ap.a.is("json") {
		ap.json(report)
		return nil
	}
	fmt.Fprintf(ap.out, "✓ 默认版本：%s\n", to.Name)
	if from != nil {
		fmt.Fprintf(ap.out, "  撤销：dsdock dsh use %s\n", from.Name)
	}
	ap.warnRunning(ctx, affected)
	return nil
}

func (ap *app) dshPin(ctx context.Context, name string) error {
	p, err := ap.loadProfileIn(ap.home)
	if err != nil {
		return err
	}
	to, err := ap.ensure(ctx, name)
	if err != nil {
		return err
	}
	from, _, err := ap.vers.Resolve(ap.home, p.Name)
	if err != nil && !errors.Is(err, versions.ErrNoVersion) {
		return err
	}
	report, err := versions.Precheck(ctx, ap.home, []*profile.Profile{p}, from, to)
	if err != nil {
		return err
	}
	ap.printSwitch(report)
	if report.Problems() && !ap.confirm("仍然固定到这个版本吗？") {
		return exitError(5)
	}
	if err := ap.vers.Pin(ap.home, p.Name, to.Name); err != nil {
		return err
	}
	if ap.a.is("json") {
		ap.json(report)
		return nil
	}
	fmt.Fprintf(ap.out, "✓ %s 固定使用 DSH %s（撤销：dsdock dsh unpin -p %s）\n", p.Name, to.Name, p.Name)
	ap.warnRunning(ctx, []*profile.Profile{p})
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
		skipped := make([]string, 0, len(pc.Skipped))
		for b := range pc.Skipped {
			skipped = append(skipped, b)
		}
		sort.Strings(skipped)
		for _, b := range skipped {
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

func (ap *app) warnRunning(ctx context.Context, ps []*profile.Profile) {
	running, _ := proc.FindRunning(ctx)
	names := map[string]bool{}
	for _, p := range ps {
		names[p.Name] = true
	}
	for _, r := range running {
		if names[r.Profile] {
			fmt.Fprintf(ap.out, "  ↻ %s 正在运行（PID %d），重启后才会换成新版本\n", r.Profile, r.PID)
		}
	}
}

func (ap *app) dshWhich() error {
	name := ap.a.str("profile")
	env, why, err := ap.envFor(name)
	if err != nil {
		return err
	}
	ver, _ := env.Version(context.Background())
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

// sourceText explains where a dsh came from.
var sourceText = map[string]string{
	versions.ByPin: "这个 profile 固定了版本", versions.ByDefault: "默认版本",
	versions.ByExternal: "接管的外部安装", versions.ByEnv: "环境变量 DSDOCK_DSH_VERSION",
	"DSDOCK_DSH": "环境变量 DSDOCK_DSH", "PATH": "PATH 里的 dsh，DSDock 还没有管理任何版本",
}

func (ap *app) loadProfileIn(home string) (*profile.Profile, error) {
	name, err := ap.profileName()
	if err != nil {
		return nil, err
	}
	p, err := profile.Load(home, name)
	if errors.Is(err, profile.ErrNotExist) {
		return nil, fmt.Errorf("profile %s 不存在", name)
	}
	return p, err
}
