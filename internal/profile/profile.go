// Package profile reads a DSH profile directory without changing it.
//
// A profile is a pnpm project under $DSH_HOME/profiles/<name>: package.json
// holds its dependencies and the ordered dsh.profile.bundles list,
// cordis.patch.yml is the user's own layer, and pnpm-lock.yaml pins every
// installed version.
package profile

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Files a snapshot captures. Everything else in a profile is either derived
// (node_modules, cordis.yml), a log (.plugin-manager), or not ours to touch.
var ManagedFiles = []string{
	"package.json",
	"pnpm-lock.yaml",
	"pnpm-workspace.yaml",
	"cordis.patch.yml",
}

// WebAppBundle is the in-box bundle that serves the Web UI. Profiles created
// by `dsh plugin --profile <name>` do not have it unless <name> is a shipped
// template such as "web".
const WebAppBundle = "@deepseek-ai/dsh-web-app"

// Profile is a profile directory and its parsed manifest.
type Profile struct {
	Name         string            `json:"name"`
	Dir          string            `json:"dir"`
	Bundles      []string          `json:"bundles"`
	Dependencies map[string]string `json:"dependencies"`
}

type manifest struct {
	Dependencies map[string]string `json:"dependencies"`
	DSH          struct {
		Profile struct {
			Bundles []string `json:"bundles"`
		} `json:"profile"`
	} `json:"dsh"`
}

// ErrNotExist means the profile directory has no package.json yet.
var ErrNotExist = errors.New("profile does not exist")

// Dir is where a named profile lives.
func Dir(home, name string) string { return filepath.Join(home, "profiles", name) }

// Load reads one profile.
func Load(home, name string) (*Profile, error) {
	if err := ValidName(name); err != nil {
		return nil, err
	}
	dir := Dir(home, name)
	b, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("%s: %w", name, ErrNotExist)
	}
	if err != nil {
		return nil, err
	}
	var m manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("%s/package.json: %w", name, err)
	}
	if m.Dependencies == nil {
		m.Dependencies = map[string]string{}
	}
	return &Profile{Name: name, Dir: dir, Bundles: m.DSH.Profile.Bundles, Dependencies: m.Dependencies}, nil
}

// List loads every profile under home, sorted by name. Directories dsh uses
// for staging (names starting with ".") are skipped.
func List(home string) ([]*Profile, error) {
	entries, err := os.ReadDir(filepath.Join(home, "profiles"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []*Profile
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		p, err := Load(home, e.Name())
		if errors.Is(err, ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// ValidName rejects names that would escape the profiles directory.
func ValidName(name string) error {
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\`) || strings.HasPrefix(name, ".") {
		return fmt.Errorf("invalid profile name %q", name)
	}
	return nil
}

// HasBundle reports whether the bundle is in the layer stack.
func (p *Profile) HasBundle(name string) bool {
	for _, b := range p.Bundles {
		if b == name {
			return true
		}
	}
	return false
}

// HasWeb reports whether the profile serves the Web UI.
func (p *Profile) HasWeb() bool { return p.HasBundle(WebAppBundle) }

// External lists bundles installed into the profile, as opposed to in-box
// bundles that resolve from the dsh installation.
func (p *Profile) External() []string {
	var out []string
	for _, b := range p.Bundles {
		if _, ok := p.Dependencies[b]; ok {
			out = append(out, b)
		}
	}
	return out
}

// InstalledVersion reads the version actually present in node_modules.
func (p *Profile) InstalledVersion(pkg string) string {
	b, err := os.ReadFile(filepath.Join(p.Dir, "node_modules", filepath.FromSlash(pkg), "package.json"))
	if err != nil {
		return ""
	}
	var m struct {
		Version string `json:"version"`
	}
	if json.Unmarshal(b, &m) != nil {
		return ""
	}
	return m.Version
}

// UserPatchPath is the user's own patch layer.
func (p *Profile) UserPatchPath() string { return filepath.Join(p.Dir, "cordis.patch.yml") }
