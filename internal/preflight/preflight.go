// Package preflight decides whether a plugin is safe to install before
// anything touches the profile.
//
// Each rule has a stable id (PFxx) used in the design document, in the CLI
// output and in tests. A Block finding stops the change; a Warn finding needs
// the user's confirmation.
package preflight

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Masterminds/semver/v3"
	"gopkg.in/yaml.v3"

	"github.com/hikariming/dsdock/internal/catalog"
	"github.com/hikariming/dsdock/internal/layer"
	"github.com/hikariming/dsdock/internal/npmreg"
	"github.com/hikariming/dsdock/internal/profile"
)

// Level is how serious a finding is.
type Level string

const (
	Block Level = "block"
	Warn  Level = "warn"
	Info  Level = "info"
)

// Finding is one rule's verdict.
type Finding struct {
	Rule    string `json:"rule"`
	Level   Level  `json:"level"`
	Message string `json:"message"`
}

// Candidate is the package a spec resolved to.
type Candidate struct {
	Spec     Spec              `json:"spec"`
	Name     string            `json:"name,omitempty"`
	Version  string            `json:"version,omitempty"`
	Manifest *npmreg.Manifest  `json:"-"`
	Patches  map[string][]byte `json:"-"` // patch file path -> content
	// PatchErr is why the patch files could not be read before installing;
	// PF10 then waits for the post-install check.
	PatchErr string `json:"patchError,omitempty"`
}

// Arg is the argument for `dsh plugin add`.
func (c *Candidate) Arg() string { return c.Spec.Arg(c.Version) }

// Report is the result of checking one candidate.
type Report struct {
	Candidate *Candidate `json:"candidate"`
	Findings  []Finding  `json:"findings"`
}

// Blocked reports whether any finding stops the change.
func (r *Report) Blocked() bool { return r.count(Block) > 0 }

// NeedsConfirmation reports whether any finding is a warning.
func (r *Report) NeedsConfirmation() bool { return r.count(Warn) > 0 }

func (r *Report) count(l Level) int {
	n := 0
	for _, f := range r.Findings {
		if f.Level == l {
			n++
		}
	}
	return n
}

func (r *Report) add(rule string, l Level, format string, args ...any) {
	r.Findings = append(r.Findings, Finding{Rule: rule, Level: l, Message: fmt.Sprintf(format, args...)})
}

// Mode says whether the candidate is a new plugin or a new version of an
// installed one.
type Mode int

const (
	ModeAdd Mode = iota
	ModeUpdate
)

// Checker holds what rules need to know about the target.
type Checker struct {
	Registry    *npmreg.Client
	DSHVersion  string
	NodeVersion string // without the leading "v"; empty skips PF08
	Profile     *profile.Profile
	Tree        *layer.Tree    // current composition; nil skips PF10
	Catalog     *catalog.Index // dshfind data; nil skips PF06/PF07
	AllowPre    bool           // consider prerelease versions when picking one
	Now         time.Time      // for PF07; zero means time.Now
}

// Resolve turns a spec into a concrete candidate. For npm specs without an
// exact version it picks the highest version dsh will accept.
func (c *Checker) Resolve(ctx context.Context, s Spec) (*Candidate, error) {
	cand := &Candidate{Spec: s}
	switch s.Kind {
	case Local:
		b, err := os.ReadFile(filepath.Join(s.Path, "package.json"))
		if err != nil {
			return nil, err
		}
		var m npmreg.Manifest
		if err := json.Unmarshal(b, &m); err != nil {
			return nil, fmt.Errorf("%s/package.json: %w", s.Path, err)
		}
		cand.Manifest, cand.Name, cand.Version = &m, m.Name, m.Version
		cand.Patches = map[string][]byte{}
		for _, p := range patchPaths(m.DSH) {
			if !safePatchPath(p) {
				continue
			}
			if b, err := os.ReadFile(filepath.Join(s.Path, filepath.FromSlash(p))); err == nil {
				cand.Patches[path.Clean(p)] = b
			}
		}
		return cand, nil
	case Git:
		return cand, nil // inspected after pnpm fetches it
	}

	version, err := c.pickVersion(ctx, s)
	if err != nil {
		return nil, err
	}
	m, err := c.Registry.Manifest(ctx, s.Name, version)
	if err != nil {
		return nil, err
	}
	cand.Manifest, cand.Name, cand.Version = m, m.Name, m.Version
	var safe []string
	for _, p := range patchPaths(m.DSH) {
		if safePatchPath(p) {
			safe = append(safe, p)
		}
	}
	if len(safe) > 0 {
		files, err := c.Registry.ReadFiles(ctx, m, safe...)
		if err != nil {
			cand.PatchErr = err.Error() // not fatal: verification after install covers it
		} else {
			cand.Patches = files
		}
	}
	return cand, nil
}

// pickVersion resolves a dist-tag, range or nothing to one exact version.
// Exact versions are returned as given so PF02 can refuse them explicitly.
func (c *Checker) pickVersion(ctx context.Context, s Spec) (string, error) {
	if _, err := semver.StrictNewVersion(s.Version); err == nil {
		return s.Version, nil
	}
	p, err := c.Registry.Packument(ctx, s.Name)
	if err != nil {
		return "", err
	}
	want := s.Version
	if tagged, ok := p.DistTags[want]; ok && want != "" {
		return tagged, nil // an explicit tag is the user's choice
	}
	var rng *semver.Constraints
	if want != "" {
		rng, err = semver.NewConstraint(want)
		if err != nil {
			return "", fmt.Errorf("%s: %q is neither a version, a range nor a dist-tag", s.Name, want)
		}
	}

	// The latest tag wins when it is acceptable; otherwise the highest
	// compatible version, stable before prerelease.
	if latest := p.DistTags["latest"]; latest != "" && want == "" {
		if m := p.Versions[latest]; m != nil && c.accepts(m) {
			return latest, nil
		}
	}
	var best *semver.Version
	for v, m := range p.Versions {
		sv, err := semver.StrictNewVersion(v)
		if err != nil || m.Deprecated != "" {
			continue
		}
		if sv.Prerelease() != "" && !c.AllowPre {
			continue
		}
		if rng != nil && !rng.Check(sv) {
			continue
		}
		if !c.accepts(m) {
			continue
		}
		if best == nil || sv.GreaterThan(best) {
			best = sv
		}
	}
	if best == nil {
		return "", fmt.Errorf("%s: no version is compatible with dsh %s", s.Name, c.DSHVersion)
	}
	return best.Original(), nil
}

func (c *Checker) accepts(m *npmreg.Manifest) bool {
	u, err := Compat(m.Engines, m.PeerDependencies, c.DSHVersion)
	return err == nil && len(u) == 0
}

// Check runs every rule against a resolved candidate.
func (c *Checker) Check(cand *Candidate, mode Mode) *Report {
	r := &Report{Candidate: cand}
	if cand.Spec.Kind == Git {
		r.add("PF05", Warn, "从 git 安装：pnpm 会先拦截它的构建脚本（prepare），需要在 profile 的 pnpm-workspace.yaml 里放行后才能装上；装之前无法检查包内容，装完后会再验证一次")
		c.checkCatalog(r, "", gitRepo(cand.Spec.Raw))
		return r
	}
	m := cand.Manifest
	c.checkCatalog(r, cand.Name, npmreg.GitHubRepo(m.Repository))

	c.checkBundle(r, m)
	c.checkCompat(r, m)
	c.checkInstalled(r, cand, mode)
	c.checkScripts(r, cand)
	if m.Deprecated != "" {
		r.add("PF07", Warn, "作者已在 npm 上标记弃用：%s", m.Deprecated)
	}
	c.checkNode(r, m)
	c.checkDuplicates(r, cand, mode)
	return r
}

func (c *Checker) checkBundle(r *Report, m *npmreg.Manifest) {
	var d struct {
		Bundle json.RawMessage `json:"bundle"`
	}
	if len(m.DSH) == 0 || json.Unmarshal(m.DSH, &d) != nil || len(d.Bundle) == 0 || string(d.Bundle) == "null" {
		r.add("PF01", Block, "%s 没有声明 dsh.bundle，不是 DSH 插件：装上只会成为普通依赖，不会生效", m.Name)
		return
	}
	paths := patchPaths(m.DSH)
	if len(paths) == 0 {
		r.add("PF01", Block, "dsh.bundle.patch 缺失或为空")
		return
	}
	for _, p := range paths {
		if !safePatchPath(p) {
			r.add("PF01", Block, "dsh.bundle.patch 路径不安全：%q（必须是包内的相对路径）", p)
		}
	}
}

func (c *Checker) checkCompat(r *Report, m *npmreg.Manifest) {
	unmet, err := Compat(m.Engines, m.PeerDependencies, c.DSHVersion)
	switch {
	case err != nil:
		r.add("PF02", Block, "无法判断兼容性（DSH 也会拒绝）：%v", err)
	case len(unmet) > 0:
		r.add("PF02", Block, "%s@%s 不兼容当前 DSH %s：要求 %s", m.Name, m.Version, c.DSHVersion, unmet)
	}
}

func (c *Checker) checkInstalled(r *Report, cand *Candidate, mode Mode) {
	if c.Profile == nil {
		return
	}
	spec, installed := c.Profile.Dependencies[cand.Name]
	switch {
	case mode == ModeAdd && installed:
		have := c.Profile.InstalledVersion(cand.Name)
		if have == "" {
			have = spec
		}
		r.add("PF03", Block, "%s 已经装在 %s 里（%s），请用 update", cand.Name, c.Profile.Name, have)
	case mode == ModeUpdate && !installed:
		r.add("PF03", Block, "%s 没有装在 %s 里，请用 add", cand.Name, c.Profile.Name)
	case mode == ModeUpdate && c.Profile.InstalledVersion(cand.Name) == cand.Version:
		r.add("PF03", Info, "%s 已经是 %s", cand.Name, cand.Version)
	}
}

var lifecycle = []string{"preinstall", "install", "postinstall"}

func (c *Checker) checkScripts(r *Report, cand *Candidate) {
	names := lifecycle
	if cand.Spec.Kind != NPM {
		names = append(names, "prepare") // pnpm runs prepare for local and git sources
	}
	var found []string
	for _, n := range names {
		if s := cand.Manifest.Scripts[n]; s != "" {
			found = append(found, fmt.Sprintf("%s: %s", n, s))
		}
	}
	if len(found) > 0 {
		r.add("PF04", Warn, "安装时会运行脚本，pnpm 默认会拦截，可能导致安装失败：%s", strings.Join(found, "；"))
	}
}

func (c *Checker) checkNode(r *Report, m *npmreg.Manifest) {
	if c.NodeVersion == "" || len(m.Engines) == 0 {
		return
	}
	var e map[string]any
	if json.Unmarshal(m.Engines, &e) != nil {
		return
	}
	rng, ok := e["node"].(string)
	if !ok {
		return
	}
	v, err := semver.NewVersion(c.NodeVersion)
	if err != nil {
		return
	}
	if !satisfies(v, rng) {
		r.add("PF08", Warn, "插件要求 Node %s，当前是 %s", rng, c.NodeVersion)
	}
}

// stale is how long without a push makes PF07 mention it.
const stale = 180 * 24 * time.Hour

// checkCatalog is PF06 (risk flag) and PF07 (archived or inactive), using
// dshfind's data, plus dshfind's score as information.
func (c *Checker) checkCatalog(r *Report, pkg, repo string) {
	if c.Catalog == nil {
		return
	}
	match, others := c.Catalog.Lookup(pkg, repo)
	if match == nil {
		for _, o := range others {
			if o.Risky {
				r.add("PF06", Warn, "dshfind 把使用同一个包名的仓库 %s 标记为有风险：%s。请确认这个包来自你信任的仓库", o.FullName, o.RiskNote)
			}
		}
		return
	}
	if match.Risky {
		note := match.RiskNote
		if note == "" {
			note = "没有附说明"
		}
		r.add("PF06", Warn, "dshfind 把仓库 %s 标记为有风险：%s", match.FullName, note)
	}
	now := c.Now
	if now.IsZero() {
		now = time.Now()
	}
	switch {
	case match.Archived:
		r.add("PF07", Warn, "仓库 %s 已归档，作者不再维护", match.FullName)
	case !match.PushedAt.IsZero() && now.Sub(match.PushedAt) > stale:
		r.add("PF07", Info, "仓库 %s 已经 %d 天没有更新", match.FullName, int(now.Sub(match.PushedAt).Hours()/24))
	}
	if match.Grade != "" && match.Score != nil {
		r.add("dshfind", Info, "评分 %s（%.0f 分）：https://dshfind.com/plugins/%s", match.Grade, *match.Score, match.FullName)
	}
}

// gitRepo extracts owner/repo from a git install spec when it is on GitHub.
func gitRepo(spec string) string {
	if reShortGit.MatchString(spec) && !strings.Contains(spec, ":") {
		spec = "github:" + spec
	}
	return npmreg.GitHubRepoFromURL(spec)
}

// checkDuplicates is PF10. dsh keeps only the last row with a given id, and
// a newly installed bundle is appended to the end of the stack, so a clash
// silently disables whichever installed plugin owned that id first.
func (c *Checker) checkDuplicates(r *Report, cand *Candidate, mode Mode) {
	if cand.PatchErr != "" {
		r.add("PF10", Info, "装之前读不到插件的配置文件（%s），重复 id 会在安装后检查", cand.PatchErr)
		return
	}
	if c.Tree == nil || len(cand.Patches) == 0 {
		return
	}
	owners := map[string]string{}
	for _, row := range c.Tree.Rows {
		if row.ID == "" || row.Source == cand.Name {
			continue // an update replaces its own rows
		}
		owners[row.ID] = row.Source
	}
	keys := make([]string, 0, len(cand.Patches))
	for k := range cand.Patches {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		ids, err := insertedIDs(cand.Patches[k])
		if err != nil {
			r.add("PF01", Block, "%s 不是有效的 patch 文件：%v", k, err)
			continue
		}
		for _, id := range ids {
			if owner, ok := owners[id]; ok {
				r.add("PF10", Warn, "插件会插入 id %q，和已装的 %s 重复：装上后 %s 的这一行会被 DSH 静默丢弃", id, owner, owner)
			}
		}
	}
}

// insertedIDs lists the ids a patch file inserts at the top level.
func insertedIDs(src []byte) ([]string, error) {
	var entries []struct {
		Insert []struct {
			ID string `yaml:"id"`
		} `yaml:"insert"`
	}
	if err := yaml.Unmarshal(src, &entries); err != nil {
		return nil, err
	}
	var ids []string
	for _, e := range entries {
		for _, row := range e.Insert {
			if row.ID != "" {
				ids = append(ids, row.ID)
			}
		}
	}
	return ids, nil
}

// patchPaths reads dsh.bundle.patch, which is a path or a list of paths.
func patchPaths(dsh json.RawMessage) []string {
	var d struct {
		Bundle struct {
			Patch json.RawMessage `json:"patch"`
		} `json:"bundle"`
	}
	if len(dsh) == 0 || json.Unmarshal(dsh, &d) != nil || len(d.Bundle.Patch) == 0 {
		return nil
	}
	var one string
	if json.Unmarshal(d.Bundle.Patch, &one) == nil {
		if one == "" {
			return nil
		}
		return []string{one}
	}
	var many []string
	if json.Unmarshal(d.Bundle.Patch, &many) == nil {
		return many
	}
	return nil
}

// safePatchPath mirrors the desktop market's check: a non-empty relative
// path that stays inside the package.
func safePatchPath(p string) bool {
	if p == "" || strings.ContainsAny(p, "\\\x00") || path.IsAbs(p) || reWinPath.MatchString(p) {
		return false
	}
	clean := path.Clean(strings.TrimPrefix(p, "./"))
	return clean != "." && clean != ".." && !strings.HasPrefix(clean, "../")
}
