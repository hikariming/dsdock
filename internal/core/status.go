package core

import (
	"context"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"sync"

	"github.com/hikariming/dsdock/internal/dshenv"
	"github.com/hikariming/dsdock/internal/layer"
	"github.com/hikariming/dsdock/internal/preflight"
	"github.com/hikariming/dsdock/internal/proc"
	"github.com/hikariming/dsdock/internal/profile"
	"github.com/hikariming/dsdock/internal/txn"
)

// Head describes the dsh that runs when no profile is named.
type Head struct {
	Home        string `json:"home"`
	Version     string `json:"version,omitempty"`
	Source      string `json:"source,omitempty"`
	Entry       string `json:"entry,omitempty"`
	NodeProblem string `json:"nodeProblem,omitempty"`
	Error       string `json:"error,omitempty"`
}

// Plugin is one installed bundle.
type Plugin struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Spec    string `json:"spec"`   // as recorded in package.json
	Source  string `json:"source"` // npm, local, git
	// Rows are the ids this bundle contributes, with their state.
	Rows []PluginRow `json:"rows,omitempty"`
}

// PluginRow is one row a bundle inserts.
type PluginRow struct {
	ID       string `json:"id"`
	Disabled string `json:"disabled,omitempty"`
	Patched  bool   `json:"patched,omitempty"` // the user layer changes it
}

// ProfileStatus is one profile's health.
type ProfileStatus struct {
	Name       string            `json:"name"`
	Web        bool              `json:"web"`
	Template   bool              `json:"template"` // one dsh ships, not one the user made
	Plugins    []Plugin          `json:"plugins"`
	DSH        string            `json:"dsh,omitempty"`
	DSHSource  string            `json:"dshSource,omitempty"`
	Healthy    bool              `json:"healthy"`
	Error      string            `json:"error,omitempty"`
	Duplicates []layer.Duplicate `json:"duplicates,omitempty"`
	Unmatched  []layer.Unmatched `json:"unmatched,omitempty"`
	Skipped    []layer.Skipped   `json:"skipped,omitempty"`
	Running    []proc.Running    `json:"running,omitempty"`
	LastSnap   string            `json:"lastSnapshot,omitempty"`
}

// Warnings counts problems that leave the profile usable.
func (s *ProfileStatus) Warnings() int { return len(s.Duplicates) + len(s.Unmatched) + len(s.Skipped) }

// Status is the whole picture.
type Status struct {
	DSH      Head            `json:"dsh"`
	Profiles []ProfileStatus `json:"profiles"`
}

// Status checks every profile (or one) with the dsh that runs it.
func (c *Core) Status(ctx context.Context, only string) (*Status, error) {
	var profiles []*profile.Profile
	if only != "" {
		p, err := c.LoadProfile(only)
		if err != nil {
			return nil, err
		}
		profiles = []*profile.Profile{p}
	} else {
		var err error
		if profiles, err = profile.List(c.Home); err != nil {
			return nil, err
		}
	}
	running, _ := proc.FindRunning(ctx)

	st := &Status{DSH: Head{Home: c.Home}}
	if env, why, err := c.EnvFor(""); err == nil {
		st.DSH.Version, _ = env.Version(ctx)
		st.DSH.NodeProblem, _ = env.CheckNode(ctx)
		st.DSH.Source, st.DSH.Entry = why, env.Entry
	} else {
		st.DSH.Error = err.Error()
	}

	st.Profiles = make([]ProfileStatus, len(profiles))
	var wg sync.WaitGroup
	for i, p := range profiles {
		wg.Add(1)
		go func(i int, p *profile.Profile) { // each dump takes ~150 ms
			defer wg.Done()
			st.Profiles[i] = c.profileStatus(ctx, p, running)
		}(i, p)
	}
	wg.Wait()
	return st, nil
}

func (c *Core) profileStatus(ctx context.Context, p *profile.Profile, running []proc.Running) ProfileStatus {
	ps := ProfileStatus{Name: p.Name, Web: p.HasWeb(), Template: profile.Templates[p.Name]}
	for _, b := range p.External() {
		spec := p.Dependencies[b]
		ps.Plugins = append(ps.Plugins, Plugin{Name: b, Version: p.InstalledVersion(b), Spec: spec, Source: sourceOf(spec)})
	}
	for _, r := range running {
		if r.Profile == p.Name {
			ps.Running = append(ps.Running, r)
		}
	}
	if snaps, _ := c.Store.List(c.Home, p.Name, 1); len(snaps) > 0 {
		ps.LastSnap = snaps[0].CreatedAt.Local().Format("2006-01-02 15:04")
	}
	env, why, err := c.EnvFor(p.Name)
	if err != nil {
		ps.Error = err.Error()
		return ps
	}
	ps.DSH, _ = env.Version(ctx)
	ps.DSHSource = why
	comp, err := c.Engine(env).Compose(ctx, p.Name)
	switch {
	case err != nil:
		ps.Error = err.Error()
	case !comp.OK:
		ps.Error = txn.ErrorLine(comp.Result.Stderr)
		if ps.Error == "" {
			ps.Error = fmt.Sprintf("dump-config 退出码 %d", comp.Result.ExitCode)
		}
	default:
		ps.Healthy = true
		ps.Duplicates = comp.Tree.Duplicates()
		ps.Unmatched = comp.Warnings.Unmatched
		ps.Skipped = comp.Warnings.Skipped
		for i := range ps.Plugins {
			for _, row := range comp.Tree.Rows {
				if row.Source == ps.Plugins[i].Name && row.ID != "" {
					ps.Plugins[i].Rows = append(ps.Plugins[i].Rows, PluginRow{ID: row.ID, Disabled: row.Disabled, Patched: len(row.PatchedBy) > 0})
				}
			}
		}
	}
	return ps
}

func sourceOf(spec string) string {
	switch {
	case strings.HasPrefix(spec, "link:"), strings.HasPrefix(spec, "file:"):
		return "local"
	case strings.Contains(spec, "git"), strings.Contains(spec, "://"):
		return "git"
	}
	return "npm"
}

// Update is a newer compatible version of an installed plugin.
type Update struct {
	Name    string `json:"name"`
	Current string `json:"current"`
	Latest  string `json:"latest"`
	Error   string `json:"error,omitempty"`
}

// Outdated lists npm plugins in a profile with a newer version that the
// profile's dsh accepts.
func (c *Core) Outdated(ctx context.Context, profileName string, allowPre bool) ([]Update, error) {
	p, err := c.LoadProfile(profileName)
	if err != nil {
		return nil, err
	}
	env, _, err := c.EnvFor(profileName)
	if err != nil {
		return nil, err
	}
	version, err := env.Version(ctx)
	if err != nil {
		return nil, err
	}
	ch := &preflight.Checker{Registry: c.Reg, DSHVersion: version, AllowPre: allowPre}
	var (
		mu  sync.Mutex
		out []Update
		wg  sync.WaitGroup
	)
	for _, b := range p.External() {
		if !isNpmSpec(p.Dependencies[b]) {
			continue
		}
		wg.Add(1)
		go func(b string) {
			defer wg.Done()
			u := Update{Name: b, Current: p.InstalledVersion(b)}
			latest, err := ch.LatestCompatible(ctx, b)
			if err != nil {
				u.Error = err.Error()
			} else if latest == u.Current {
				return
			}
			u.Latest = latest
			mu.Lock()
			out = append(out, u)
			mu.Unlock()
		}(b)
	}
	wg.Wait()
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func nodeVersion(ctx context.Context, env *dshenv.Env) (string, error) {
	node := env.Node
	if node == "" {
		node = "node"
	}
	out, err := exec.CommandContext(ctx, node, "-v").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimPrefix(strings.TrimSpace(string(out)), "v"), nil
}
