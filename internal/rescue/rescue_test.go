package rescue

import "testing"

func TestRootPackage(t *testing.T) {
	for in, want := range map[string]string{
		"fx-broken":                    "fx-broken",
		"pkg/startup":                  "pkg",
		"@nagi-ovo/dsh-ads":            "@nagi-ovo/dsh-ads",
		"@deepseek-ai/dsh-web-app/sub": "@deepseek-ai/dsh-web-app",
	} {
		if got := rootPackage(in); got != want {
			t.Errorf("rootPackage(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSeverity(t *testing.T) {
	d := &Diagnosis{Healthy: true}
	d.add(Problem{Kind: Unmatched, Severity: Warn})
	if !d.Healthy {
		t.Error("a warning made the profile unhealthy")
	}
	d.add(Problem{Kind: Inactive, Severity: Error})
	if d.Healthy {
		t.Error("an error left the profile healthy")
	}
}
