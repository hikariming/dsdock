// Package rescue finds out why a profile does not work and what would
// bring it back.
//
// It looks in the order problems usually happen: a hand-edited patch file
// that no longer parses, a composition that fails, bundles dsh skips, then a
// real boot. Each problem comes with a concrete fix; the general fix is to
// return to the last verified state that differs from the current one.
package rescue

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/hikariming/dsdock/internal/proc"
	"github.com/hikariming/dsdock/internal/profile"
	"github.com/hikariming/dsdock/internal/snapshot"
	"github.com/hikariming/dsdock/internal/txn"
	"github.com/hikariming/dsdock/internal/userpatch"
)

// Kind classifies a problem.
type Kind string

const (
	BadUserPatch  Kind = "bad-user-patch"
	BadHomePatch  Kind = "bad-home-patch"
	ComposeFailed Kind = "compose-failed"
	Skipped       Kind = "skipped"
	Duplicate     Kind = "duplicate"
	Unmatched     Kind = "unmatched"
	BootExited    Kind = "boot-exited"
	BootTimeout   Kind = "boot-timeout"
	Inactive      Kind = "inactive"
)

// Severity of a problem. Errors make a profile unhealthy.
const (
	Error = "error"
	Warn  = "warn"
)

// Problem is one thing wrong with a profile.
type Problem struct {
	Kind     Kind   `json:"kind"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
	Package  string `json:"package,omitempty"`
	ID       string `json:"id,omitempty"`
	Fix      string `json:"fix,omitempty"`
}

// Diagnosis is everything rescue found.
type Diagnosis struct {
	Profile  string             `json:"profile"`
	Healthy  bool               `json:"healthy"`
	Problems []Problem          `json:"problems"`
	Composed bool               `json:"composed"`
	Booted   bool               `json:"booted"` // a boot test ran
	Smoke    *proc.SmokeResult  `json:"smoke,omitempty"`
	LastGood *snapshot.Snapshot `json:"lastGood,omitempty"`
}

// Options tune a diagnosis.
type Options struct {
	Boot      bool // also start the profile (slower, finds load-time failures)
	SmokeOpts proc.SmokeOptions
}

func (d *Diagnosis) add(p Problem) {
	d.Problems = append(d.Problems, p)
	if p.Severity == Error {
		d.Healthy = false
	}
}

// Diagnose examines one profile. It never changes anything.
func Diagnose(ctx context.Context, eng *txn.Engine, name string, opt Options) (*Diagnosis, error) {
	p, err := profile.Load(eng.Env.Home, name)
	if err != nil {
		return nil, err
	}
	d := &Diagnosis{Profile: name, Healthy: true}

	badPatch := false
	for _, f := range []struct {
		path string
		kind Kind
	}{{p.UserPatchPath(), BadUserPatch}, {filepath.Join(eng.Env.Home, "cordis.patch.yml"), BadHomePatch}} {
		src, err := os.ReadFile(f.path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err == nil {
			err = userpatch.Validate(src)
		}
		if err != nil {
			badPatch = true
			d.add(Problem{Kind: f.kind, Severity: Error,
				Message: fmt.Sprintf("%s 无法读取：%v", f.path, err),
				Fix:     "按提示的行号修正这个文件；或者回到上一个正常的状态（会恢复这个文件）"})
		}
	}

	c, err := eng.Compose(ctx, name)
	if err != nil {
		return nil, err
	}
	currentHash := ""
	if !c.OK {
		if !badPatch { // a broken patch file already explains this
			d.add(Problem{Kind: ComposeFailed, Severity: Error, Message: "配置无法组装：" + txn.ErrorLine(c.Result.Stderr),
				Fix: "回到上一个正常的状态"})
		}
	} else {
		d.Composed, currentHash = true, c.Hash
		for _, s := range c.Warnings.Skipped {
			d.add(Problem{Kind: Skipped, Severity: Error, Package: s.Package,
				Message: fmt.Sprintf("%s 被 DSH 跳过，没有生效：%s", s.Package, s.Short()),
				Fix:     fmt.Sprintf("更新它（dsdock update %s -p %s），或者让这个 profile 固定使用兼容的 DSH 版本（dsdock dsh pin <版本> -p %s）", s.Package, name, name)})
		}
		for _, dup := range c.Tree.Duplicates() {
			d.add(Problem{Kind: Duplicate, Severity: Error, ID: dup.ID, Package: strings.Join(dup.Losers(), "、"),
				Message: fmt.Sprintf("id %q 被 %s 重复插入，只有 %s 生效", dup.ID, strings.Join(dup.Sources, "、"), dup.Winner()),
				Fix:     fmt.Sprintf("卸载其中一个：dsdock remove <包名> -p %s", name)})
		}
		for _, u := range c.Warnings.Unmatched {
			d.add(Problem{Kind: Unmatched, Severity: Warn, ID: u.ID,
				Message: fmt.Sprintf("%s 里的覆盖找不到目标 %q", u.File, u.ID),
				Fix:     "对应的插件可能已经卸载；从这个文件里删掉这条覆盖"})
		}
	}

	if d.Composed && opt.Boot {
		s, err := proc.Smoke(ctx, eng.Env, p, opt.SmokeOpts)
		if err != nil {
			return nil, err
		}
		d.Booted, d.Smoke = true, s
		switch {
		case s.Exited:
			d.add(Problem{Kind: BootExited, Severity: Error,
				Message: fmt.Sprintf("DSH 启动后退出了（退出码 %d）：%s", s.ExitCode, lastLines(s.Stderr, 4)),
				Fix:     "回到上一个正常的状态"})
		case s.TimedOut:
			d.add(Problem{Kind: BootTimeout, Severity: Error, Message: "DSH 在规定时间内没有就绪",
				Fix: "如果是刚装的 DSH 版本，第一次启动可能比较慢，可以再试一次；否则回到上一个正常的状态"})
		}
		for _, in := range s.Inactive {
			pr := Problem{Kind: Inactive, Severity: Error, ID: in.ID, Package: in.Module,
				Message: fmt.Sprintf("插件 %s（%s）没有激活：%s", in.ID, in.Module, in.Reason),
				Fix:     fmt.Sprintf("禁用它（dsdock disable %s -p %s）或卸载它（dsdock remove %s -p %s）", in.ID, name, rootPackage(in.Module), name)}
			if in.Pending() {
				pr.Fix = "它在等一个没有启动的服务。如果等的是 webServer，这个插件需要装在带 Web 界面的 profile 里（比如 web）"
			}
			d.add(pr)
		}
	}

	if !d.Healthy {
		lg, err := eng.Store.LastGoodOther(eng.Env.Home, name, currentHash)
		if err != nil {
			return nil, err
		}
		d.LastGood = lg
	}
	return d, nil
}

// rootPackage turns a plugin module ("pkg/sub", "@s/pkg/sub") into the
// package that installs it.
func rootPackage(module string) string {
	parts := strings.Split(module, "/")
	if strings.HasPrefix(module, "@") && len(parts) >= 2 {
		return parts[0] + "/" + parts[1]
	}
	return parts[0]
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
