package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/hikariming/dsdock/internal/catalog"
	"github.com/hikariming/dsdock/internal/dshenv"
	"github.com/hikariming/dsdock/internal/layer"
	"github.com/hikariming/dsdock/internal/preflight"
	"github.com/hikariming/dsdock/internal/proc"
	"github.com/hikariming/dsdock/internal/profile"
	"github.com/hikariming/dsdock/internal/snapshot"
	"github.com/hikariming/dsdock/internal/txn"
	"github.com/hikariming/dsdock/internal/userpatch"
)

// Plan is a checked change waiting for the user's go-ahead.
type Plan struct {
	Kind    string `json:"kind"` // add, update, remove, enable, disable
	Profile string `json:"profile"`
	Target  string `json:"target"` // spec, package or row id
	// Report is the preflight result for add and update.
	Report *preflight.Report `json:"report,omitempty"`
	// Notes are further reasons to confirm, beyond Report's warnings.
	Notes []string `json:"notes,omitempty"`
	// From and To are versions, for update.
	From string `json:"from,omitempty"`
	To   string `json:"to,omitempty"`
	// NoChange means there is nothing to do; Reason says why.
	NoChange bool   `json:"noChange,omitempty"`
	Reason   string `json:"reason,omitempty"`
	// Preview is the patch file after a toggle.
	Preview string `json:"preview,omitempty"`

	change txn.Change
	engine *txn.Engine
}

// Blocked reports a preflight finding that stops the change.
func (p *Plan) Blocked() bool { return p.Report != nil && p.Report.Blocked() }

// NeedsConfirmation reports anything the user should agree to first.
func (p *Plan) NeedsConfirmation() bool {
	return len(p.Notes) > 0 || (p.Report != nil && p.Report.NeedsConfirmation())
}

// Apply carries out the plan through a transaction. Callers check Blocked
// and NeedsConfirmation first; Apply does not ask again.
func (p *Plan) Apply(ctx context.Context, smoke bool) (*txn.Outcome, error) {
	if p.NoChange {
		return nil, errors.New("nothing to apply: " + p.Reason)
	}
	return p.engine.Run(ctx, p.change, txn.Options{Smoke: smoke, SmokeOpts: proc.DefaultSmoke})
}

// checker gathers what preflight rules need about a profile.
func (c *Core) checker(ctx context.Context, eng *txn.Engine, p *profile.Profile, allowPre bool) (*preflight.Checker, error) {
	version, err := eng.Env.Version(ctx)
	if err != nil {
		return nil, err
	}
	ch := &preflight.Checker{Registry: c.Reg, DSHVersion: version, Profile: p, AllowPre: allowPre}
	if v, err := nodeVersion(ctx, eng.Env); err == nil {
		ch.NodeVersion = v
	}
	if comp, err := eng.Compose(ctx, p.Name); err == nil && comp.OK {
		ch.Tree = comp.Tree
	}
	if ix, err := catalog.Load(ctx, filepath.Join(c.DataDir, "cache")); err == nil {
		ch.Catalog = ix
	}
	return ch, nil
}

// Check runs preflight on a spec without planning a change.
func (c *Core) Check(ctx context.Context, profileName, spec, cwd string, allowPre bool) (*preflight.Report, error) {
	p, err := c.LoadProfile(profileName)
	if err != nil {
		return nil, err
	}
	eng, err := c.EngineFor(profileName)
	if err != nil {
		return nil, err
	}
	return c.preflight(ctx, eng, p, spec, cwd, preflight.ModeAdd, allowPre)
}

func (c *Core) preflight(ctx context.Context, eng *txn.Engine, p *profile.Profile, raw, cwd string, mode preflight.Mode, allowPre bool) (*preflight.Report, error) {
	spec, err := preflight.ParseSpec(raw, cwd)
	if err != nil {
		return nil, err
	}
	ch, err := c.checker(ctx, eng, p, allowPre)
	if err != nil {
		return nil, err
	}
	cand, err := ch.Resolve(ctx, spec)
	if err != nil {
		return nil, err
	}
	return ch.Check(cand, mode), nil
}

// PlanAdd checks a plugin and prepares installing it. cwd anchors relative
// paths.
func (c *Core) PlanAdd(ctx context.Context, profileName, spec, cwd string, allowPre bool) (*Plan, error) {
	p, err := c.LoadProfile(profileName)
	if err != nil {
		return nil, err
	}
	eng, err := c.EngineFor(profileName)
	if err != nil {
		return nil, err
	}
	r, err := c.preflight(ctx, eng, p, spec, cwd, preflight.ModeAdd, allowPre)
	if err != nil {
		return nil, err
	}
	cand := r.Candidate
	before := map[string]bool{}
	for _, b := range p.Bundles {
		before[b] = true
	}
	plan := &Plan{Kind: "add", Profile: p.Name, Target: cand.Arg(), Report: r, To: cand.Version, engine: eng}
	plan.change = txn.Change{
		Kind: "add", Profile: p.Name, Spec: cand.Arg(), Package: cand.Name, TouchesDeps: true,
		Apply: func(ctx context.Context) (*dshenv.Result, error) {
			return eng.Env.Plugin(ctx, p.Name, "add", cand.Arg())
		},
		Expect: func(comp *txn.Composition) error {
			now, err := profile.Load(c.Home, p.Name)
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
				if reason, skipped := comp.Warnings.SkippedPackage(b); skipped {
					return fmt.Errorf("DSH 跳过了 %s：%s", b, reason)
				}
				if !comp.Tree.HasSource(b) {
					return fmt.Errorf("%s 已安装，但它的配置层没有出现在组装结果里", b)
				}
			}
			return nil
		},
	}
	return plan, nil
}

// ErrNotNpm means a plugin was not installed from the npm registry.
var ErrNotNpm = errors.New("not installed from npm")

// PlanUpdate prepares moving an installed npm plugin to `to`, or to the
// newest version the profile's dsh accepts when to is empty.
func (c *Core) PlanUpdate(ctx context.Context, profileName, pkg, to string, allowPre bool) (*Plan, error) {
	p, err := c.LoadProfile(profileName)
	if err != nil {
		return nil, err
	}
	dep, ok := p.Dependencies[pkg]
	if !ok {
		return nil, fmt.Errorf("%s 没有装在 %s 里", pkg, p.Name)
	}
	if !isNpmSpec(dep) {
		return nil, fmt.Errorf("%s 来自 %s，暂时只支持更新 npm 上的插件: %w", pkg, dep, ErrNotNpm)
	}
	eng, err := c.EngineFor(profileName)
	if err != nil {
		return nil, err
	}
	raw := pkg
	if to != "" {
		raw += "@" + to
	}
	r, err := c.preflight(ctx, eng, p, raw, "", preflight.ModeUpdate, allowPre)
	if err != nil {
		return nil, err
	}
	cand := r.Candidate
	from := p.InstalledVersion(pkg)
	plan := &Plan{Kind: "update", Profile: p.Name, Target: pkg, Report: r, From: from, To: cand.Version, engine: eng}
	if from == cand.Version {
		plan.NoChange, plan.Reason = true, fmt.Sprintf("%s 已经是兼容当前 DSH 的最新版本 %s", pkg, from)
		return plan, nil
	}
	// `add name@new` updates in place and keeps the bundle's position in the
	// stack (M0 V6); removing first would move it to the end.
	plan.change = txn.Change{
		Kind: "update", Profile: p.Name, Spec: cand.Arg(), Package: pkg, TouchesDeps: true,
		Apply: func(ctx context.Context) (*dshenv.Result, error) {
			return eng.Env.Plugin(ctx, p.Name, "add", cand.Arg())
		},
		Expect: func(comp *txn.Composition) error {
			now, err := profile.Load(c.Home, p.Name)
			if err != nil {
				return err
			}
			if v := now.InstalledVersion(pkg); v != cand.Version {
				return fmt.Errorf("更新后的版本是 %q，预期 %s", v, cand.Version)
			}
			if !equalStrings(now.Bundles, p.Bundles) {
				return errors.New("更新改变了组合包的顺序")
			}
			if reason, skipped := comp.Warnings.SkippedPackage(pkg); skipped {
				return fmt.Errorf("DSH 跳过了 %s：%s", pkg, reason)
			}
			return nil
		},
	}
	return plan, nil
}

// PlanRemove prepares uninstalling a plugin.
func (c *Core) PlanRemove(ctx context.Context, profileName, pkg string) (*Plan, error) {
	p, err := c.LoadProfile(profileName)
	if err != nil {
		return nil, err
	}
	if _, ok := p.Dependencies[pkg]; !ok {
		return nil, fmt.Errorf("%s 没有装在 %s 里", pkg, p.Name)
	}
	eng, err := c.EngineFor(profileName)
	if err != nil {
		return nil, err
	}
	plan := &Plan{Kind: "remove", Profile: p.Name, Target: pkg, From: p.InstalledVersion(pkg), engine: eng}
	// Overrides in the user layer that target this plugin's rows would be
	// left pointing at nothing.
	if comp, err := eng.Compose(ctx, p.Name); err == nil && comp.OK {
		var targeted []string
		for _, row := range comp.Tree.Rows {
			if row.Source == pkg && len(row.PatchedBy) > 0 {
				targeted = append(targeted, row.ID)
			}
		}
		if len(targeted) > 0 {
			plan.Notes = append(plan.Notes, fmt.Sprintf("你的覆盖配置还引用了这个插件的 %s，卸载后它们会找不到目标", strings.Join(targeted, "、")))
		}
	}
	plan.change = txn.Change{
		Kind: "remove", Profile: p.Name, Spec: pkg, Package: pkg, TouchesDeps: true,
		Apply: func(ctx context.Context) (*dshenv.Result, error) {
			return eng.Env.Plugin(ctx, p.Name, "remove", pkg)
		},
		Expect: func(comp *txn.Composition) error {
			if comp.Tree.HasSource(pkg) {
				return fmt.Errorf("卸载后 %s 的配置层仍然存在", pkg)
			}
			return nil
		},
	}
	return plan, nil
}

// PlanToggle prepares disabling or enabling one row through the user patch,
// with the same rules as dsh's own plugin manager.
func (c *Core) PlanToggle(ctx context.Context, profileName, id string, disable bool) (*Plan, error) {
	p, err := c.LoadProfile(profileName)
	if err != nil {
		return nil, err
	}
	eng, err := c.EngineFor(profileName)
	if err != nil {
		return nil, err
	}
	kind := map[bool]string{true: "disable", false: "enable"}[disable]
	cur, err := eng.Compose(ctx, p.Name)
	if err != nil {
		return nil, err
	}
	if !cur.OK {
		return nil, fmt.Errorf("profile %s 现在无法组装，请先运行 dsdock rescue -p %s", p.Name, p.Name)
	}
	row := FindRow(cur.Tree, id)
	if row == nil {
		return nil, fmt.Errorf("%s 里没有 id 为 %q 的配置行", p.Name, id)
	}
	if row.Disabled == layer.DisabledExpr {
		return nil, fmt.Errorf("%q 的开关由表达式决定（!!js），DSDock 不改它", id)
	}
	base := false
	if r, err := eng.Env.DumpDefault(ctx, p.Name); err == nil && r.OK() {
		if t, err := layer.Parse(r.Stdout); err == nil {
			if br := FindRow(t, id); br != nil {
				base = br.Disabled == layer.Disabled
			}
		}
	}
	path := p.UserPatchPath()
	src, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	next, changed, err := userpatch.SetDisabled(src, id, disable, base)
	if err != nil {
		return nil, err
	}
	plan := &Plan{Kind: kind, Profile: p.Name, Target: id, engine: eng}
	if !changed {
		plan.NoChange, plan.Reason = true, fmt.Sprintf("%q 已经是%s状态", id, map[bool]string{true: "禁用", false: "启用"}[disable])
		return plan, nil
	}
	plan.Preview = string(next)
	plan.change = txn.Change{
		Kind: kind, Profile: p.Name, Spec: id, Package: row.Source,
		Apply: func(ctx context.Context) (*dshenv.Result, error) {
			return nil, writeFileAtomic(path, next)
		},
		Expect: func(comp *txn.Composition) error {
			r := FindRow(comp.Tree, id)
			if r == nil {
				return fmt.Errorf("改动后找不到 %q", id)
			}
			if (r.Disabled == layer.Disabled) != disable {
				return fmt.Errorf("改动后 %q 的状态不对", id)
			}
			return nil
		},
	}
	return plan, nil
}

// FindRow returns the row dsh loads for an id: the last one.
func FindRow(t *layer.Tree, id string) *layer.Row {
	var found *layer.Row
	for i := range t.Rows {
		if t.Rows[i].ID == id {
			found = &t.Rows[i]
		}
	}
	return found
}

func isNpmSpec(dep string) bool {
	return !strings.HasPrefix(dep, "link:") && !strings.HasPrefix(dep, "file:") && !strings.Contains(dep, "git") && !strings.Contains(dep, "://")
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

func envSet(k string) bool { return os.Getenv(k) != "" }

// TakeSnapshot saves a profile by hand, marked verified when it composes.
func (c *Core) TakeSnapshot(ctx context.Context, eng *txn.Engine, name string) (*snapshot.Snapshot, error) {
	unlock, err := eng.Lock(ctx, name)
	if err != nil {
		return nil, err
	}
	defer unlock()
	version, _ := eng.Env.Version(ctx)
	s, err := c.Store.Take(c.Home, name, "manual", version, true)
	if err != nil {
		return nil, err
	}
	if comp, err := eng.Compose(ctx, name); err == nil && comp.OK {
		if c.Store.MarkVerified(s.ID, comp.Hash) == nil {
			s.Verified, s.DumpHash = true, comp.Hash
		}
	}
	return s, nil
}
