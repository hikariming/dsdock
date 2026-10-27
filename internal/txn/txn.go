// Package txn is the only way DSDock changes a profile.
//
//	lock → baseline → snapshot → apply → verify → (record | roll back)
//
// Invariant: no code path writes to a profile except through Engine.Run.
package txn

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gofrs/flock"

	"github.com/hikariming/dsdock/internal/dshenv"
	"github.com/hikariming/dsdock/internal/layer"
	"github.com/hikariming/dsdock/internal/proc"
	"github.com/hikariming/dsdock/internal/profile"
	"github.com/hikariming/dsdock/internal/redact"
	"github.com/hikariming/dsdock/internal/snapshot"
)

// Change describes one modification.
type Change struct {
	Kind    string // add, remove, update, disable, enable, restore
	Profile string
	Spec    string // what the user asked for, for the record
	// Package is the bundle the change is about. A smoke test failure that
	// names it rolls the change back; other failures are only reported.
	Package string
	// TouchesDeps means the change edits package.json or the lockfile, so a
	// rollback must reinstall and a running profile must restart.
	TouchesDeps bool
	// Apply performs the change. It returns the dsh run it made, if any.
	Apply func(ctx context.Context) (*dshenv.Result, error)
	// Expect checks the new composition for the intended effect.
	Expect func(c *Composition) error
}

// Options tune verification.
type Options struct {
	Smoke     bool // also boot the profile (level-2 check)
	SmokeOpts proc.SmokeOptions
}

// Outcome is what happened.
type Outcome struct {
	Op         *snapshot.Op       `json:"op"`
	Before     *snapshot.Snapshot `json:"before"`
	After      *snapshot.Snapshot `json:"after,omitempty"`
	Applied    bool               `json:"applied"`
	RolledBack bool               `json:"rolledBack"`
	Reason     string             `json:"reason,omitempty"` // why it was rolled back
	Warnings   []string           `json:"warnings,omitempty"`
	Smoke      *proc.SmokeResult  `json:"smoke,omitempty"`
	Running    []proc.Running     `json:"running,omitempty"` // instances that need a restart
}

// Engine runs changes.
type Engine struct {
	Env      *dshenv.Env
	Store    *snapshot.Store
	LockDir  string
	Progress func(msg string) // optional; receives short status lines
}

func (e *Engine) say(format string, args ...any) {
	if e.Progress != nil {
		e.Progress(fmt.Sprintf(format, args...))
	}
}

// ErrBusy means another DSDock is changing the same profile.
var ErrBusy = errors.New("another DSDock operation is changing this profile")

// Lock takes the per-profile lock. Callers that change a profile outside Run
// (none should) and long reads that must not race a change use it directly.
func (e *Engine) Lock(ctx context.Context, name string) (func(), error) {
	if err := os.MkdirAll(e.LockDir, 0o755); err != nil {
		return nil, err
	}
	h := sha256.Sum256([]byte(e.Env.Home))
	l := flock.New(filepath.Join(e.LockDir, hex.EncodeToString(h[:6])+"-"+name+".lock"))
	lctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	ok, err := l.TryLockContext(lctx, 200*time.Millisecond)
	if err != nil && !errors.Is(err, context.DeadlineExceeded) {
		return nil, err
	}
	if !ok {
		return nil, ErrBusy
	}
	return func() { _ = l.Unlock() }, nil
}

// Composition is a parsed dump plus the facts verification compares.
type Composition struct {
	OK       bool
	Hash     string
	Tree     *layer.Tree
	Warnings layer.Warnings
	Result   *dshenv.Result
}

// Compose runs the level-1 check: `dsh --dump-config`.
func (e *Engine) Compose(ctx context.Context, name string) (*Composition, error) {
	r, err := e.Env.Dump(ctx, name)
	if err != nil {
		return nil, err
	}
	c := &Composition{Result: r, Warnings: layer.ParseWarnings(r.Stderr)}
	if !r.OK() {
		return c, nil
	}
	tree, err := layer.Parse(r.Stdout)
	if err != nil {
		return c, nil // an unparseable dump is treated as unhealthy
	}
	c.OK, c.Tree, c.Hash = true, tree, snapshot.Hash(r.Stdout)
	return c, nil
}

// Run applies a change with full protection.
func (e *Engine) Run(ctx context.Context, ch Change, opt Options) (*Outcome, error) {
	unlock, err := e.Lock(ctx, ch.Profile)
	if err != nil {
		return nil, err
	}
	defer unlock()

	version, err := e.Env.Version(ctx)
	if err != nil {
		return nil, err
	}
	op, err := e.Store.BeginOp(ch.Kind, e.Env.Home, ch.Profile, ch.Spec)
	if err != nil {
		return nil, err
	}
	var log strings.Builder
	out := &Outcome{Op: op}
	finish := func(status string) (*Outcome, error) {
		op.Status, op.Log = status, redact.Text(log.String())
		if err := e.Store.EndOp(op); err != nil {
			return out, err
		}
		return out, nil
	}

	// Baseline: what the profile composes to now.
	e.say("检查当前状态")
	base, err := e.Compose(ctx, ch.Profile)
	if err != nil {
		return finish(snapshot.OpFailed)
	}
	if !base.OK {
		out.Warnings = append(out.Warnings, "改动之前这个 profile 就无法组装："+firstLine(base.Result.Stderr))
	}

	e.say("拍快照")
	before, err := e.Store.Take(e.Env.Home, ch.Profile, "before "+ch.Kind+" "+ch.Spec, version, false)
	if err != nil {
		fmt.Fprintf(&log, "snapshot: %v\n", err)
		return finish(snapshot.OpFailed)
	}
	if base.OK {
		if err := e.Store.MarkVerified(before.ID, base.Hash); err != nil {
			return finish(snapshot.OpFailed)
		}
		before.Verified, before.DumpHash = true, base.Hash
	}
	op.SnapshotID, out.Before = before.ID, before

	e.say("执行")
	r, applyErr := ch.Apply(ctx)
	logRun(&log, r)
	if applyErr != nil || (r != nil && !r.OK()) {
		reason := "执行失败"
		switch {
		case applyErr != nil:
			reason += "：" + applyErr.Error()
		case r != nil:
			reason += "：" + lastLines(r.Stderr+r.Stdout, 6)
		}
		return e.rollback(ctx, ch, out, &log, finish, reason)
	}

	e.say("验证")
	after, err := e.Compose(ctx, ch.Profile)
	if err != nil {
		return e.rollback(ctx, ch, out, &log, finish, "验证失败："+err.Error())
	}
	logRun(&log, after.Result)
	if !after.OK {
		return e.rollback(ctx, ch, out, &log, finish, "改动后 profile 无法组装："+lastLines(after.Result.Stderr, 6))
	}
	if ch.Expect != nil {
		if err := ch.Expect(after); err != nil {
			return e.rollback(ctx, ch, out, &log, finish, err.Error())
		}
	}
	out.Warnings = append(out.Warnings, newWarnings(base, after)...)

	if opt.Smoke {
		e.say("启动测试")
		p, err := profile.Load(e.Env.Home, ch.Profile)
		if err != nil {
			return e.rollback(ctx, ch, out, &log, finish, err.Error())
		}
		s, err := proc.Smoke(ctx, e.Env, p, opt.SmokeOpts)
		if err != nil {
			return e.rollback(ctx, ch, out, &log, finish, "启动测试失败："+err.Error())
		}
		out.Smoke = s
		fmt.Fprintf(&log, "$ smoke ready=%v exited=%v timedOut=%v %s\n%s%s", s.Ready, s.Exited, s.TimedOut, s.Duration.Round(time.Millisecond), s.Stdout, s.Stderr)
		if reason := smokeFailure(s, ch.Package); reason != "" {
			return e.rollback(ctx, ch, out, &log, finish, reason)
		}
		for _, in := range s.Inactive {
			out.Warnings = append(out.Warnings, fmt.Sprintf("插件 %s（%s）没有激活：%s", in.ID, in.Module, in.Reason))
		}
	}

	afterSnap, err := e.Store.Take(e.Env.Home, ch.Profile, "after "+ch.Kind+" "+ch.Spec, version, false)
	if err == nil && e.Store.MarkVerified(afterSnap.ID, after.Hash) == nil {
		afterSnap.Verified, afterSnap.DumpHash = true, after.Hash
		out.After = afterSnap
	}
	out.Applied = true
	if ch.TouchesDeps {
		out.Running = e.running(ctx, ch.Profile)
	}
	return finish(snapshot.OpApplied)
}

// smokeFailure decides whether a boot result undoes the change. Plugins that
// were already broken are reported but do not block an unrelated change.
func smokeFailure(s *proc.SmokeResult, pkg string) string {
	switch {
	case s.Exited:
		return fmt.Sprintf("启动后 DSH 退出了（退出码 %d）：%s", s.ExitCode, lastLines(s.Stderr, 6))
	case s.TimedOut:
		return "启动测试超时，DSH 没有就绪"
	}
	for _, in := range s.Inactive {
		if pkg != "" && (in.Module == pkg || strings.HasPrefix(in.Module, pkg+"/")) {
			return fmt.Sprintf("插件 %s 没有激活：%s", in.Module, in.Reason)
		}
	}
	return ""
}

func (e *Engine) rollback(ctx context.Context, ch Change, out *Outcome, log *strings.Builder, finish func(string) (*Outcome, error), reason string) (*Outcome, error) {
	out.Reason = reason
	e.say("回滚：%s", reason)
	fmt.Fprintf(log, "rollback: %s\n", reason)
	if err := e.restoreFiles(ctx, out.Before, ch.TouchesDeps, log); err != nil {
		out.Warnings = append(out.Warnings, "回滚失败："+err.Error())
		return finish(snapshot.OpFailed)
	}
	if out.Before.Verified {
		c, err := e.Compose(ctx, ch.Profile)
		if err != nil || !c.OK || c.Hash != out.Before.DumpHash {
			out.Warnings = append(out.Warnings, "回滚后配置和改动前不一致，请运行 dsdock status 检查")
			return finish(snapshot.OpFailed)
		}
	}
	out.RolledBack = true
	return finish(snapshot.OpRolledBack)
}

// restoreFiles writes a snapshot back and, when dependencies changed,
// reinstalls from its lockfile: offline first, from the network if needed.
func (e *Engine) restoreFiles(ctx context.Context, sn *snapshot.Snapshot, reinstall bool, log *strings.Builder) error {
	if err := e.Store.WriteBack(sn.ID); err != nil {
		return err
	}
	if !reinstall {
		return nil
	}
	r, err := e.Env.Plugin(ctx, sn.Profile, "install", "--frozen-lockfile", "--offline")
	logRun(log, r)
	if err == nil && r.OK() {
		return nil
	}
	r, err = e.Env.Plugin(ctx, sn.Profile, "install", "--frozen-lockfile")
	logRun(log, r)
	if err != nil {
		return err
	}
	if !r.OK() {
		return fmt.Errorf("pnpm install exited %d: %s", r.ExitCode, lastLines(r.Stderr, 4))
	}
	return nil
}

// Restore returns a profile to a snapshot as a change of its own, so it is
// itself snapshotted and can be undone.
func (e *Engine) Restore(ctx context.Context, snapID string, opt Options) (*Outcome, error) {
	target, err := e.Store.Get(snapID)
	if err != nil {
		return nil, err
	}
	if target.Home != e.Env.Home {
		return nil, fmt.Errorf("snapshot %s belongs to DSH_HOME %s", snapID, target.Home)
	}
	var log strings.Builder
	return e.Run(ctx, Change{
		Kind: "restore", Profile: target.Profile, Spec: snapID, TouchesDeps: true,
		Apply: func(ctx context.Context) (*dshenv.Result, error) {
			return nil, e.restoreFiles(ctx, target, true, &log)
		},
		Expect: func(c *Composition) error {
			if target.Verified && c.Hash != target.DumpHash {
				return errors.New("恢复后的配置和快照记录的不一致")
			}
			return nil
		},
	}, opt)
}

func (e *Engine) running(ctx context.Context, name string) []proc.Running {
	all, err := proc.FindRunning(ctx)
	if err != nil {
		return nil
	}
	var out []proc.Running
	for _, r := range all {
		if r.Profile == name {
			out = append(out, r)
		}
	}
	return out
}

// newWarnings reports problems the change introduced: duplicate ids (dsh
// silently drops all but the last row) and patch targets that disappeared.
func newWarnings(base, after *Composition) []string {
	var out []string
	seen := map[string]bool{}
	if base.OK {
		for _, d := range base.Tree.Duplicates() {
			seen["dup:"+d.ID] = true
		}
	}
	for _, d := range after.Tree.Duplicates() {
		if !seen["dup:"+d.ID] {
			out = append(out, fmt.Sprintf("id %q 被多个插件插入（%s），只有 %s 会生效", d.ID, strings.Join(d.Sources, "、"), d.Winner()))
		}
	}
	for _, u := range base.Warnings.Unmatched {
		seen["um:"+u.File+u.ID] = true
	}
	for _, u := range after.Warnings.Unmatched {
		if !seen["um:"+u.File+u.ID] {
			out = append(out, fmt.Sprintf("%s 里的覆盖找不到目标 %q（对应的插件可能已被移除）", u.File, u.ID))
		}
	}
	return out
}

func logRun(log *strings.Builder, r *dshenv.Result) {
	if r == nil {
		return
	}
	fmt.Fprintf(log, "$ dsh %s  (exit %d, %s)\n%s%s", strings.Join(r.Args, " "), r.ExitCode, r.Duration.Round(time.Millisecond), r.Stdout, r.Stderr)
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(redact.Text(s)), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
