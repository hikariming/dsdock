package preflight

import (
	"testing"

	"github.com/Masterminds/semver/v3"
)

// Reference answers from npm semver (the copy dsh 0.2.1-rc.4 ships), called
// the way dsh calls it: satisfies("0.2.1-rc.4", range, {includePrerelease: true}).
var npmSemverRC4 = map[string]bool{
	">=0.2.0":          true,
	"^0.2.0":           true,
	"^0.2.1-rc.1":      true,
	"~0.2.0":           true,
	"0.2.x":            true,
	">=0.2.0 <0.3.0":   true,
	"^0.1.0 || ^0.2.0": true,
	"0.2.1":            false,
	"<0.2.1":           true,
	">0.2.1-rc.3":      true,
	"*":                true,
	">=99.0.0":         false,
	"^0.3.0":           false,
	"0.2.0 - 0.2.5":    true,
	"x":                true,
	"":                 false,
}

func TestSatisfiesMatchesNpmSemver(t *testing.T) {
	v := semver.MustParse("0.2.1-rc.4")
	for r, want := range npmSemverRC4 {
		if got := satisfies(v, r); got != want {
			t.Errorf("satisfies(0.2.1-rc.4, %q) = %v, npm semver says %v", r, got, want)
		}
	}
}
