package txn

import "testing"

func TestErrorLine(t *testing.T) {
	crash := "file:///x/dsh-app-boot/lib/index.js:3998\n\t\tthrow new Error(`${binName}: failed`);\n\t\t      ^\n\nError: dsh: failed to parse overlay /h/cordis.patch.yml: YAMLException: bad (6:1)\n    at parsePatchList (file:///x:1:1)\n"
	if got := ErrorLine(crash); got != "Error: dsh: failed to parse overlay /h/cordis.patch.yml: YAMLException: bad (6:1)" {
		t.Errorf("crash: %q", got)
	}
	if got := ErrorLine("dsh: [p] patch: entry \"x\" not found\n"); got != `dsh: [p] patch: entry "x" not found` {
		t.Errorf("warning: %q", got)
	}
	if got := ErrorLine("something odd\nmore"); got != "something odd" {
		t.Errorf("fallback: %q", got)
	}
}
