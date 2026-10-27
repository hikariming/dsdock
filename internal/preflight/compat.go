package preflight

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/Masterminds/semver/v3"
)

// Unmet maps a requirement ("engines.dsh" or a peer package name) to the
// range dsh would refuse.
type Unmet map[string]string

// Compat ports dsh-app-boot's evaluatePluginCompatibility (0.2.1-rc.4):
//
//   - an own engines.dsh decides alone; peerDependencies are then ignored;
//   - otherwise every @deepseek-ai/dsh and @deepseek-ai/dsh-* peer must match;
//   - prereleases take part in ranges (includePrerelease);
//   - for peers only, workspace:^, workspace:~ and workspace:* mean "this dsh";
//   - empty or invalid ranges are unmet;
//   - a non-object engines or peerDependencies, or a non-string range, makes
//     the manifest unreadable, which dsh treats as a refusal.
func Compat(engines, peers json.RawMessage, dshVersion string) (Unmet, error) {
	runtime, err := semver.StrictNewVersion(dshVersion)
	if err != nil {
		return nil, fmt.Errorf("invalid dsh version %q", dshVersion)
	}
	if len(engines) > 0 && string(engines) != "null" {
		var e map[string]json.RawMessage
		if err := json.Unmarshal(engines, &e); err != nil {
			return nil, errors.New("engines must be an object")
		}
		if raw, ok := e["dsh"]; ok {
			var r string
			if err := json.Unmarshal(raw, &r); err != nil {
				return nil, errors.New("engines.dsh must be a string")
			}
			if !satisfies(runtime, r) {
				return Unmet{"engines.dsh": r}, nil
			}
			return nil, nil
		}
	}
	if len(peers) == 0 || string(peers) == "null" {
		return nil, nil
	}
	var p map[string]json.RawMessage
	if err := json.Unmarshal(peers, &p); err != nil {
		return nil, errors.New("peerDependencies must be an object")
	}
	unmet := Unmet{}
	for name, raw := range p {
		var r string
		if err := json.Unmarshal(raw, &r); err != nil {
			return nil, fmt.Errorf("peerDependencies[%q] must be a string", name)
		}
		if name != "@deepseek-ai/dsh" && !strings.HasPrefix(name, "@deepseek-ai/dsh-") {
			continue
		}
		switch r {
		case "workspace:^", "workspace:~", "workspace:*":
			continue
		}
		if !satisfies(runtime, r) {
			unmet[name] = r
		}
	}
	if len(unmet) == 0 {
		return nil, nil
	}
	return unmet, nil
}

func satisfies(v *semver.Version, r string) bool {
	if strings.TrimSpace(r) == "" {
		return false
	}
	c, err := semver.NewConstraint(r)
	if err != nil {
		return false
	}
	c.IncludePrerelease = true
	return c.Check(v)
}

// String renders unmet requirements in a stable order.
func (u Unmet) String() string {
	keys := make([]string, 0, len(u))
	for k := range u {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = fmt.Sprintf("%s %s", k, u[k])
	}
	return strings.Join(parts, ", ")
}
