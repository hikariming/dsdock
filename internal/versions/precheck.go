package versions

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/hikariming/dsdock/internal/layer"
	"github.com/hikariming/dsdock/internal/preflight"
	"github.com/hikariming/dsdock/internal/profile"
)

// ProfileCheck is how one profile fares on the target release.
type ProfileCheck struct {
	Profile string `json:"profile"`
	// Skipped maps a bundle to the reason the target release leaves it out.
	// This comes from the target composing the profile itself, so it honours
	// exemptions granted with `dsh plugin allow-version`.
	Skipped map[string]string `json:"skipped,omitempty"`
	// ComposeError is set when the target release cannot compose the profile
	// at all. Incompatible and Unreadable are then filled from the manifests
	// instead, as a best guess.
	ComposeError string                     `json:"composeError,omitempty"`
	Incompatible map[string]preflight.Unmet `json:"incompatible,omitempty"`
	Unreadable   []string                   `json:"unreadable,omitempty"`
}

// OK reports a profile with nothing to worry about.
func (c ProfileCheck) OK() bool {
	return len(c.Skipped) == 0 && len(c.Incompatible) == 0 && len(c.Unreadable) == 0 && c.ComposeError == ""
}

// SwitchReport is what moving some profiles from one release to another
// would do.
type SwitchReport struct {
	From     string         `json:"from,omitempty"`
	To       string         `json:"to"`
	Profiles []ProfileCheck `json:"profiles"`
	// SessionDowngrade is set when the target writes an older Session format:
	// sessions created or continued on the newer release will not open.
	SessionDowngrade bool `json:"sessionDowngrade,omitempty"`
	FromFormat       int  `json:"fromFormat,omitempty"`
	ToFormat         int  `json:"toFormat,omitempty"`
}

// Problems reports whether anything needs the user's attention.
func (r *SwitchReport) Problems() bool {
	if r.SessionDowngrade {
		return true
	}
	for _, p := range r.Profiles {
		if !p.OK() {
			return true
		}
	}
	return false
}

// Precheck evaluates moving profiles (in home) from one release to another.
// from may be nil when nothing ran them before.
func Precheck(ctx context.Context, home string, profiles []*profile.Profile, from, to *Installed) (*SwitchReport, error) {
	r := &SwitchReport{To: to.Name, ToFormat: to.SessionFormat}
	if from != nil {
		r.From, r.FromFormat = from.Name, from.SessionFormat
		r.SessionDowngrade = from.SessionFormat > 0 && to.SessionFormat > 0 && to.SessionFormat < from.SessionFormat
	}
	env, err := to.Env(home)
	if err != nil {
		return nil, err
	}
	for _, p := range profiles {
		pc := ProfileCheck{Profile: p.Name}
		res, err := env.Dump(ctx, p.Name)
		switch {
		case err != nil:
			pc.ComposeError = err.Error()
		case !res.OK():
			pc.ComposeError = firstLine(res.Stderr)
		default:
			for _, sk := range layer.ParseWarnings(res.Stderr).Skipped {
				if pc.Skipped == nil {
					pc.Skipped = map[string]string{}
				}
				pc.Skipped[sk.Package] = sk.Short()
			}
		}
		if pc.ComposeError != "" {
			staticCheck(&pc, p, to.Version)
		}
		r.Profiles = append(r.Profiles, pc)
	}
	return r, nil
}

// staticCheck judges installed bundles from their manifests alone.
func staticCheck(pc *ProfileCheck, p *profile.Profile, version string) {
	for _, b := range p.External() {
		engines, peers, ok := installedManifest(p, b)
		if !ok {
			continue
		}
		unmet, err := preflight.Compat(engines, peers, version)
		switch {
		case err != nil:
			pc.Unreadable = append(pc.Unreadable, b)
		case len(unmet) > 0:
			if pc.Incompatible == nil {
				pc.Incompatible = map[string]preflight.Unmet{}
			}
			pc.Incompatible[b] = unmet
		}
	}
}

func installedManifest(p *profile.Profile, pkg string) (engines, peers json.RawMessage, ok bool) {
	b, err := os.ReadFile(filepath.Join(p.Dir, "node_modules", filepath.FromSlash(pkg), "package.json"))
	if err != nil {
		return nil, nil, false
	}
	var m struct {
		Engines          json.RawMessage `json:"engines"`
		PeerDependencies json.RawMessage `json:"peerDependencies"`
	}
	if json.Unmarshal(b, &m) != nil {
		return nil, nil, false
	}
	return m.Engines, m.PeerDependencies, true
}

func firstLine(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			return s[:i]
		}
	}
	return s
}
