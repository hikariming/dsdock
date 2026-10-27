package core

import (
	"context"
	"errors"

	"github.com/hikariming/dsdock/internal/proc"
	"github.com/hikariming/dsdock/internal/profile"
	"github.com/hikariming/dsdock/internal/versions"
)

// SwitchPlan is a checked change of which dsh release runs some profiles.
type SwitchPlan struct {
	Kind    string                 `json:"kind"`              // use, pin
	Profile string                 `json:"profile,omitempty"` // for pin
	Report  *versions.SwitchReport `json:"report"`
	// Same means the release already runs these profiles.
	Same bool `json:"same,omitempty"`

	affected []string
	apply    func() error
}

// NeedsConfirmation reports skipped plugins, compose failures or a Session
// format downgrade.
func (p *SwitchPlan) NeedsConfirmation() bool { return p.Report.Problems() }

// Apply records the switch.
func (p *SwitchPlan) Apply() error {
	if p.Same {
		return nil
	}
	return p.apply()
}

// Affected lists the profiles whose release changes.
func (p *SwitchPlan) Affected() []string { return p.affected }

// Ensure returns an installed release, installing a version or channel
// first when needed. installed reports whether it had to.
func (c *Core) Ensure(ctx context.Context, name string) (inst *versions.Installed, installed bool, err error) {
	inst, err = c.Vers.Get(name)
	if err == nil {
		return inst, false, nil
	}
	if !errors.Is(err, versions.ErrNotInstalled) || name == versions.External {
		return nil, false, err
	}
	inst, _, err = c.Vers.Install(ctx, name)
	return inst, err == nil, err
}

// PlanUse prepares switching the default release. Profiles with a pin keep
// theirs; every other profile is checked against the target.
func (c *Core) PlanUse(ctx context.Context, name string) (*SwitchPlan, error) {
	to, err := c.Vers.Get(name)
	if err != nil {
		return nil, err
	}
	from, _, err := c.Vers.Resolve(c.Home, "")
	if err != nil && !errors.Is(err, versions.ErrNoVersion) {
		return nil, err
	}
	all, err := profile.List(c.Home)
	if err != nil {
		return nil, err
	}
	var affected []*profile.Profile
	for _, p := range all {
		if pin, _ := c.Vers.PinOf(c.Home, p.Name); pin == "" {
			affected = append(affected, p)
		}
	}
	plan := &SwitchPlan{Kind: "use", affected: names(affected), apply: func() error { return c.Vers.SetDefault(to.Name) }}
	if from != nil && from.Name == to.Name {
		plan.Same, plan.Report = true, &versions.SwitchReport{From: from.Name, To: to.Name}
		return plan, nil
	}
	plan.Report, err = versions.Precheck(ctx, c.Home, affected, from, to)
	return plan, err
}

// PlanPin prepares pinning one profile to a release.
func (c *Core) PlanPin(ctx context.Context, profileName, name string) (*SwitchPlan, error) {
	p, err := c.LoadProfile(profileName)
	if err != nil {
		return nil, err
	}
	to, err := c.Vers.Get(name)
	if err != nil {
		return nil, err
	}
	from, _, err := c.Vers.Resolve(c.Home, p.Name)
	if err != nil && !errors.Is(err, versions.ErrNoVersion) {
		return nil, err
	}
	plan := &SwitchPlan{Kind: "pin", Profile: p.Name, affected: []string{p.Name},
		apply: func() error { return c.Vers.Pin(c.Home, p.Name, to.Name) }}
	if pin, _ := c.Vers.PinOf(c.Home, p.Name); pin == to.Name {
		plan.Same, plan.Report = true, &versions.SwitchReport{From: pin, To: to.Name}
		return plan, nil
	}
	plan.Report, err = versions.Precheck(ctx, c.Home, []*profile.Profile{p}, from, to)
	return plan, err
}

// Running lists running instances of the named profiles.
func (c *Core) Running(ctx context.Context, profiles []string) []proc.Running {
	all, _ := proc.FindRunning(ctx)
	want := map[string]bool{}
	for _, p := range profiles {
		want[p] = true
	}
	var out []proc.Running
	for _, r := range all {
		if want[r.Profile] {
			out = append(out, r)
		}
	}
	return out
}

func names(ps []*profile.Profile) []string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = p.Name
	}
	return out
}
