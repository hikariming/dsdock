package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/hikariming/dsdock/internal/dshenv"
	"github.com/hikariming/dsdock/internal/layer"
	"github.com/hikariming/dsdock/internal/preflight"
	"github.com/hikariming/dsdock/internal/proc"
	"github.com/hikariming/dsdock/internal/profile"
	"github.com/hikariming/dsdock/internal/txn"
	"github.com/hikariming/dsdock/internal/userpatch"
)

// ---------- status ----------

type profileStatus struct {
	Profile    *profile.Profile  `json:"profile"`
	Versions   map[string]string `json:"versions"`
	Healthy    bool              `json:"healthy"`
	Error      string            `json:"error,omitempty"`
	Duplicates []layer.Duplicate `json:"duplicates,omitempty"`
	Unmatched  []layer.Unmatched `json:"unmatched,omitempty"`
	Running    []proc.Running    `json:"running,omitempty"`
	LastSnap   string            `json:"lastSnapshot,omitempty"`
}

func (ap *app) cmdStatus(ctx context.Context, _ []string) error {
	version, err := ap.env.Version(ctx)
	if err != nil {
		return err
	}
	nodeProblem, _ := ap.env.CheckNode(ctx)
	var profiles []*profile.Profile
	if name := ap.a.str("profile"); name != "" {
		p, err := ap.loadProfile()
		if err != nil {
			return err
		}
		profiles = []*profile.Profile{p}
	} else if profiles, err = profile.List(ap.env.Home); err != nil {
		return err
	}
	running, _ := proc.FindRunning(ctx)

	var list []profileStatus
	for _, p := range profiles {
		st := profileStatus{Profile: p, Versions: map[string]string{}}
		for _, b := range p.External() {
			st.Versions[b] = p.InstalledVersion(b)
		}
		c, err := ap.engine.Compose(ctx, p.Name)
		switch {
		case err != nil:
			st.Error = err.Error()
		case !c.OK:
			st.Error = firstNonEmpty(c.Result.Stderr, fmt.Sprintf("dump-config 退出码 %d", c.Result.ExitCode))
		default:
			st.Healthy = true
			st.Duplicates = c.Tree.Duplicates()
			st.Unmatched = c.Warnings.Unmatched
		}
		for _, r := range running {
			if r.Profile == p.Name {
				st.Running = append(st.Running, r)
			}
		}
		if snaps, _ := ap.store.List(ap.env.Home, p.Name, 1); len(snaps) > 0 {
			st.LastSnap = snaps[0].CreatedAt.Local().Format("2006-01-02 15:04")
		}
		list = append(list, st)
	}

	if ap.a.is("json") {
		ap.json(map[string]any{"dsh": map[string]any{"version": version, "home": ap.env.Home, "entry": ap.env.Entry, "node": ap.env.Node, "nodeProblem": nodeProblem}, "profiles": list})
		return nil
	}
	fmt.Fprintf(ap.out, "DSH %s   DSH_HOME %s\n", version, ap.env.Home)
	if nodeProblem != "" {
		fmt.Fprintf(ap.out, "⚠ %s\n", nodeProblem)
	}
	if len(list) == 0 {
		fmt.Fprintln(ap.out, "\n还没有 profile。")
		return nil
	}
	for _, st := range list {
		mark := "✓"
		if !st.Healthy {
			mark = "✗"
		} else if len(st.Duplicates)+len(st.Unmatched) > 0 {
			mark = "⚠"
		}
		web := ""
		if st.Profile.HasWeb() {
			web = "  [web]"
		}
		fmt.Fprintf(ap.out, "\n%s %s%s\n", mark, st.Profile.Name, web)
		for _, b := range st.Profile.External() {
			v := st.Versions[b]
			if v == "" {
				v = st.Profile.Dependencies[b]
			}
			fmt.Fprintf(ap.out, "    %s  %s\n", b, v)
		}
		if len(st.Profile.External()) == 0 {
			fmt.Fprintln(ap.out, "    （没有装外部插件）")
		}
		if st.Error != "" {
			fmt.Fprintf(ap.out, "    ✗ 无法组装：%s\n", firstLine(st.Error))
		}
		for _, d := range st.Duplicates {
			fmt.Fprintf(ap.out, "    ⚠ id %q 被 %s 重复插入，只有 %s 生效\n", d.ID, strings.Join(d.Sources, "、"), d.Winner())
		}
		for _, u := range st.Unmatched {
			fmt.Fprintf(ap.out, "    ⚠ 覆盖找不到目标 %q（%s）\n", u.ID, u.File)
		}
		for _, r := range st.Running {
			fmt.Fprintf(ap.out, "    ▶ 正在运行，PID %d\n", r.PID)
		}
		if st.LastSnap != "" {
			fmt.Fprintf(ap.out, "    最近快照 %s\n", st.LastSnap)
		}
	}
	return nil
}

// ---------- check / add / update ----------

func (ap *app) checker(ctx context.Context, p *profile.Profile) (*preflight.Checker, error) {
	version, err := ap.env.Version(ctx)
	if err != nil {
		return nil, err
	}
	c := &preflight.Checker{Registry: ap.reg, DSHVersion: version, Profile: p, AllowPre: ap.a.is("pre")}
	if node := nodeVersion(ctx, ap.env); node != "" {
		c.NodeVersion = node
	}
	if comp, err := ap.engine.Compose(ctx, p.Name); err == nil && comp.OK {
		c.Tree = comp.Tree
	}
	return c, nil
}

func (ap *app) preflight(ctx context.Context, p *profile.Profile, raw string, mode preflight.Mode) (*preflight.Report, error) {
	cwd, _ := os.Getwd()
	spec, err := preflight.ParseSpec(raw, cwd)
	if err != nil {
		return nil, err
	}
	c, err := ap.checker(ctx, p)
	if err != nil {
		return nil, err
	}
	cand, err := c.Resolve(ctx, spec)
	if err != nil {
		return nil, err
	}
	return c.Check(cand, mode), nil
}

func (ap *app) printReport(r *preflight.Report) {
	if ap.a.is("json") {
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

// gate turns a report into go / no-go.
func (ap *app) gate(r *preflight.Report) error {
	if r.Blocked() && !ap.a.is("force") {
		return exitError(5)
	}
	if r.Blocked() {
		fmt.Fprintln(ap.errw, "⚠ 已用 --force 忽略阻止级别的检查结果")
	}
	if r.NeedsConfirmation() && !ap.confirm("有警告，仍然继续吗？") {
		return exitError(5)
	}
	return nil
}

func (ap *app) cmdCheck(ctx context.Context, rest []string) error {
	if len(rest) != 1 {
		return usageError("用法：dsdock check <规格> -p <profile>")
	}
	p, err := ap.loadProfile()
	if err != nil {
		return err
	}
	r, err := ap.preflight(ctx, p, rest[0], preflight.ModeAdd)
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
	p, err := ap.loadProfile()
	if err != nil {
		return err
	}
	r, err := ap.preflight(ctx, p, rest[0], preflight.ModeAdd)
	if err != nil {
		return err
	}
	ap.printReport(r)
	if err := ap.gate(r); err != nil {
		if ap.a.is("json") {
			ap.json(r)
		}
		return err
	}
	if ap.a.is("dry-run") {
		if ap.a.is("json") {
			ap.json(r)
		}
		return nil
	}
	cand := r.Candidate
	before := map[string]bool{}
	for _, b := range p.Bundles {
		before[b] = true
	}
	out, err := ap.engine.Run(ctx, txn.Change{
		Kind: "add", Profile: p.Name, Spec: cand.Arg(), Package: cand.Name, TouchesDeps: true,
		Apply: func(ctx context.Context) (*dshenv.Result, error) {
			return ap.env.Plugin(ctx, p.Name, "add", cand.Arg())
		},
		Expect: func(c *txn.Composition) error {
			now, err := profile.Load(ap.env.Home, p.Name)
			if err != nil {
				return err
			}
			var added []string
			for _, b := range now.Bundles {
				if !before[b] {
					added = append(added, b)
				}
			}
			if len(added) == 0 {
				return errors.New("安装后没有新增任何组合包（这个包可能没有声明 dsh.bundle）")
			}
			for _, b := range added {
				if !c.Tree.HasSource(b) {
					return fmt.Errorf("%s 已安装，但它的配置层没有出现在组装结果里", b)
				}
			}
			return nil
		},
	}, ap.txnOptions())
	if err != nil {
		return err
	}
	return ap.report(out)
}

func (ap *app) cmdUpdate(ctx context.Context, rest []string) error {
	if len(rest) != 1 {
		return usageError("用法：dsdock update <包名> -p <profile> [--to <版本>]")
	}
	p, err := ap.loadProfile()
	if err != nil {
		return err
	}
	name := rest[0]
	dep, ok := p.Dependencies[name]
	if !ok {
		return fmt.Errorf("%s 没有装在 %s 里", name, p.Name)
	}
	if strings.HasPrefix(dep, "link:") || strings.HasPrefix(dep, "file:") || strings.Contains(dep, "git") {
		return fmt.Errorf("%s 来自 %s，暂时只支持更新 npm 上的插件", name, dep)
	}
	raw := name
	if to := ap.a.str("to"); to != "" {
		raw += "@" + to
	}
	r, err := ap.preflight(ctx, p, raw, preflight.ModeUpdate)
	if err != nil {
		return err
	}
	cand := r.Candidate
	from := p.InstalledVersion(name)
	if from == cand.Version {
		ap.printf("%s 已经是兼容当前 DSH 的最新版本 %s\n", name, from)
		if ap.a.is("json") {
			ap.json(map[string]string{"name": name, "version": from, "status": "up-to-date"})
		}
		return nil
	}
	ap.printf("%s：%s → %s\n", name, from, cand.Version)
	ap.printReport(r)
	if err := ap.gate(r); err != nil {
		return err
	}
	if ap.a.is("dry-run") {
		return nil
	}
	// `add name@new` updates in place and keeps the bundle's position in the
	// stack (M0 V6); removing first would move it to the end.
	out, err := ap.engine.Run(ctx, txn.Change{
		Kind: "update", Profile: p.Name, Spec: cand.Arg(), Package: name, TouchesDeps: true,
		Apply: func(ctx context.Context) (*dshenv.Result, error) {
			return ap.env.Plugin(ctx, p.Name, "add", cand.Arg())
		},
		Expect: func(c *txn.Composition) error {
			now, err := profile.Load(ap.env.Home, p.Name)
			if err != nil {
				return err
			}
			if v := now.InstalledVersion(name); v != cand.Version {
				return fmt.Errorf("更新后的版本是 %q，预期 %s", v, cand.Version)
			}
			if !equalStrings(now.Bundles, p.Bundles) {
				return errors.New("更新改变了组合包的顺序")
			}
			return nil
		},
	}, ap.txnOptions())
	if err != nil {
		return err
	}
	return ap.report(out)
}

// ---------- remove ----------

func (ap *app) cmdRemove(ctx context.Context, rest []string) error {
	if len(rest) != 1 {
		return usageError("用法：dsdock remove <包名> -p <profile>")
	}
	p, err := ap.loadProfile()
	if err != nil {
		return err
	}
	name := rest[0]
	if _, ok := p.Dependencies[name]; !ok {
		return fmt.Errorf("%s 没有装在 %s 里", name, p.Name)
	}
	// Overrides in the user layer that target this plugin's rows would be
	// left pointing at nothing.
	if c, err := ap.engine.Compose(ctx, p.Name); err == nil && c.OK {
		var targeted []string
		for _, row := range c.Tree.Rows {
			if row.Source == name && len(row.PatchedBy) > 0 {
				targeted = append(targeted, row.ID)
			}
		}
		if len(targeted) > 0 {
			ap.printf("  ⚠ 你的覆盖配置还引用了这个插件的 %s，卸载后它们会找不到目标\n", strings.Join(targeted, "、"))
			if !ap.confirm("仍然卸载吗？") {
				return exitError(5)
			}
		}
	}
	if ap.a.is("dry-run") {
		return nil
	}
	out, err := ap.engine.Run(ctx, txn.Change{
		Kind: "remove", Profile: p.Name, Spec: name, Package: name, TouchesDeps: true,
		Apply: func(ctx context.Context) (*dshenv.Result, error) {
			return ap.env.Plugin(ctx, p.Name, "remove", name)
		},
		Expect: func(c *txn.Composition) error {
			if c.Tree.HasSource(name) {
				return fmt.Errorf("卸载后 %s 的配置层仍然存在", name)
			}
			return nil
		},
	}, ap.txnOptions())
	if err != nil {
		return err
	}
	return ap.report(out)
}

// ---------- disable / enable ----------

func (ap *app) cmdToggle(ctx context.Context, rest []string, disable bool) error {
	verb := map[bool]string{true: "disable", false: "enable"}[disable]
	if len(rest) != 1 {
		return usageError(fmt.Sprintf("用法：dsdock %s <id> -p <profile>", verb))
	}
	p, err := ap.loadProfile()
	if err != nil {
		return err
	}
	id := rest[0]

	cur, err := ap.engine.Compose(ctx, p.Name)
	if err != nil {
		return err
	}
	if !cur.OK {
		return fmt.Errorf("profile %s 现在无法组装，请先运行 dsdock status 查看", p.Name)
	}
	row := findRow(cur.Tree, id)
	if row == nil {
		return fmt.Errorf("%s 里没有 id 为 %q 的配置行", p.Name, id)
	}
	if row.Disabled == layer.DisabledExpr {
		return fmt.Errorf("%q 的开关由表达式决定（!!js），DSDock 不改它", id)
	}

	base := false
	if r, err := ap.env.DumpDefault(ctx, p.Name); err == nil && r.OK() {
		if t, err := layer.Parse(r.Stdout); err == nil {
			if br := findRow(t, id); br != nil {
				base = br.Disabled == layer.Disabled
			}
		}
	}

	path := p.UserPatchPath()
	src, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	next, changed, err := userpatch.SetDisabled(src, id, disable, base)
	if err != nil {
		return err
	}
	if !changed {
		ap.printf("%q 已经是%s状态\n", id, map[bool]string{true: "禁用", false: "启用"}[disable])
		return nil
	}
	if ap.a.is("dry-run") {
		ap.printf("%s", next)
		return nil
	}
	out, err := ap.engine.Run(ctx, txn.Change{
		Kind: verb, Profile: p.Name, Spec: id, Package: row.Source,
		Apply: func(ctx context.Context) (*dshenv.Result, error) {
			return nil, writeFileAtomic(path, next)
		},
		Expect: func(c *txn.Composition) error {
			r := findRow(c.Tree, id)
			if r == nil {
				return fmt.Errorf("改动后找不到 %q", id)
			}
			if (r.Disabled == layer.Disabled) != disable {
				return fmt.Errorf("改动后 %q 的状态不对", id)
			}
			return nil
		},
	}, ap.txnOptions())
	if err != nil {
		return err
	}
	return ap.report(out)
}

func findRow(t *layer.Tree, id string) *layer.Row {
	var found *layer.Row
	for i := range t.Rows {
		if t.Rows[i].ID == id {
			found = &t.Rows[i] // the last row with an id is the one dsh loads
		}
	}
	return found
}

func writeFileAtomic(path string, b []byte) error {
	mode := os.FileMode(0o644)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	tmp := path + ".dsdock-tmp"
	if err := os.WriteFile(tmp, b, mode); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// ---------- snap ----------

func (ap *app) cmdSnap(ctx context.Context, rest []string) error {
	if len(rest) == 0 {
		return usageError("用法：dsdock snap list|take|restore")
	}
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
		snaps, err := ap.store.List(ap.env.Home, name, limit)
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
		p, err := ap.loadProfile()
		if err != nil {
			return err
		}
		unlock, err := ap.engine.Lock(ctx, p.Name)
		if err != nil {
			return err
		}
		defer unlock()
		version, _ := ap.env.Version(ctx)
		s, err := ap.store.Take(ap.env.Home, p.Name, "manual", version, true)
		if err != nil {
			return err
		}
		if c, err := ap.engine.Compose(ctx, p.Name); err == nil && c.OK {
			if ap.store.MarkVerified(s.ID, c.Hash) == nil {
				s.Verified, s.DumpHash = true, c.Hash
			}
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
		target, err := ap.store.Get(rest[1])
		if err != nil {
			return err
		}
		ap.printf("恢复 %s 到 %s 的状态（%s）\n", target.Profile, target.CreatedAt.Local().Format("01-02 15:04:05"), target.Reason)
		if !ap.confirm("确定恢复吗？") {
			return exitError(5)
		}
		out, err := ap.engine.Restore(ctx, target.ID, ap.txnOptions())
		if err != nil {
			return err
		}
		return ap.report(out)
	}
	return usageError("用法：dsdock snap list|take|restore")
}

// ---------- smoke / log ----------

func (ap *app) cmdSmoke(ctx context.Context, _ []string) error {
	p, err := ap.loadProfile()
	if err != nil {
		return err
	}
	if running, _ := proc.FindRunning(ctx); len(running) > 0 {
		for _, r := range running {
			if r.Profile == p.Name {
				ap.printf("  · %s 已经在运行（PID %d），这次测试会用另一个端口再启动一份\n", p.Name, r.PID)
			}
		}
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
	ops, err := ap.store.Ops(ap.env.Home, name, limit)
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

func nodeVersion(ctx context.Context, env *dshenv.Env) string {
	node := env.Node
	if node == "" {
		node = "node"
	}
	out, err := execOutput(ctx, node, "-v")
	if err != nil {
		return ""
	}
	return strings.TrimPrefix(strings.TrimSpace(out), "v")
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func firstNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}

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
