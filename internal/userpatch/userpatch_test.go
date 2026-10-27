package userpatch

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// The file dsh writes for a new profile (Linux M0, V3).
const fresh = `# Your patch layer for this dsh profile, applied after every bundle layer:
# a top-level YAML array of loader patch entries (id-targeted config
# overrides, disables, and insert lists; ` + "`!!js`" + ` expressions allowed).
[]
`

type entry struct {
	ID       string `yaml:"id"`
	Disabled *bool  `yaml:"disabled"`
	Config   any    `yaml:"config"`
	Insert   any    `yaml:"insert"`
}

func entries(t *testing.T, b []byte) []entry {
	t.Helper()
	var out []entry
	if err := yaml.Unmarshal(b, &out); err != nil {
		t.Fatalf("result is not a valid patch list: %v\n%s", err, b)
	}
	return out
}

func TestDisableOnFreshFile(t *testing.T) {
	out, changed, err := SetDisabled([]byte(fresh), "fx-ok", true, false)
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	es := entries(t, out)
	if len(es) != 1 || es[0].ID != "fx-ok" || es[0].Disabled == nil || !*es[0].Disabled {
		t.Fatalf("entries = %+v\n%s", es, out)
	}
	if !strings.HasPrefix(string(out), "# Your patch layer") {
		t.Errorf("header comment lost:\n%s", out)
	}
}

func TestEnableRemovesRedundantOverride(t *testing.T) {
	out, _, err := SetDisabled([]byte(fresh), "fx-ok", true, false)
	if err != nil {
		t.Fatal(err)
	}
	back, changed, err := SetDisabled(out, "fx-ok", false, false)
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	if es := entries(t, back); len(es) != 0 {
		t.Fatalf("entries = %+v\n%s", es, back)
	}
	if !strings.Contains(string(back), "[]") || !strings.HasPrefix(string(back), "# Your patch layer") {
		t.Errorf("round trip should give back an empty list with the header:\n%s", back)
	}
}

func TestOnlyLastOverrideChanges(t *testing.T) {
	src := `- id: fx-ok
  config: {a: 1}
- id: other
  disabled: true
- id: fx-ok
  config: {b: 2} # keep me
`
	out, changed, err := SetDisabled([]byte(src), "fx-ok", true, false)
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	es := entries(t, out)
	if len(es) != 3 || es[0].Disabled != nil || es[2].Disabled == nil || !*es[2].Disabled {
		t.Fatalf("entries = %+v\n%s", es, out)
	}
	if !strings.Contains(string(out), "# keep me") {
		t.Errorf("comment lost:\n%s", out)
	}
}

func TestOverrideWithConfigIsKept(t *testing.T) {
	src := "- id: fx-ok\n  disabled: true\n  config: {a: 1}\n"
	out, _, err := SetDisabled([]byte(src), "fx-ok", false, false)
	if err != nil {
		t.Fatal(err)
	}
	es := entries(t, out)
	if len(es) != 1 || es[0].Disabled == nil || *es[0].Disabled || es[0].Config == nil {
		t.Fatalf("entries = %+v\n%s", es, out)
	}
}

func TestInsertIsNotAnOverride(t *testing.T) {
	src := "- insert:\n    - id: fx-ok\n      name: fx-ok\n"
	out, changed, err := SetDisabled([]byte(src), "fx-ok", true, false)
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	if es := entries(t, out); len(es) != 2 || es[1].ID != "fx-ok" {
		t.Fatalf("entries = %+v\n%s", es, out)
	}
}

func TestNoChangeWhenAlreadyInState(t *testing.T) {
	if _, changed, _ := SetDisabled([]byte(fresh), "fx-ok", false, false); changed {
		t.Error("enabling an enabled entry changed the file")
	}
	if _, changed, _ := SetDisabled([]byte(fresh), "base-off", true, true); changed {
		t.Error("disabling an entry the bundles already disable changed the file")
	}
	src := "- id: fx-ok\n  disabled: true\n"
	if _, changed, _ := SetDisabled([]byte(src), "fx-ok", true, false); changed {
		t.Error("re-disabling changed the file")
	}
}

func TestEmptyAndNullFiles(t *testing.T) {
	for _, src := range []string{"", "# only a comment\n", "null\n"} {
		out, changed, err := SetDisabled([]byte(src), "x", true, false)
		if err != nil || !changed {
			t.Fatalf("%q: changed=%v err=%v", src, changed, err)
		}
		if es := entries(t, out); len(es) != 1 {
			t.Fatalf("%q: entries = %+v", src, es)
		}
	}
}

func TestValidate(t *testing.T) {
	// The mistake from M0 V7: list items appended after the default `[]`.
	broken := fresh + "- id: x\n  config: {}\n"
	if Validate([]byte(broken)) == nil {
		t.Error("broken file validated")
	}
	if Validate([]byte("a: 1\n")) == nil {
		t.Error("mapping validated")
	}
	if err := Validate([]byte(fresh)); err != nil {
		t.Error(err)
	}
}

func TestRemovingEntryKeepsComments(t *testing.T) {
	src := `# header
- id: a
  disabled: true
# about b
- id: b
  config: {x: 1}
`
	out, changed, err := SetDisabled([]byte(src), "a", false, false)
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	if es := entries(t, out); len(es) != 1 || es[0].ID != "b" {
		t.Fatalf("entries = %+v\n%s", es, out)
	}
	for _, c := range []string{"# header", "# about b"} {
		if !strings.Contains(string(out), c) {
			t.Errorf("%q lost:\n%s", c, out)
		}
	}
}

func TestRefusesToRewriteUnreadableFile(t *testing.T) {
	for _, src := range []string{
		fresh + "- id: x\n  config: {}\n", // yaml.Unmarshal alone would read this as []
		"[]\n---\n- id: y\n",
	} {
		if _, _, err := SetDisabled([]byte(src), "x", true, false); err == nil {
			t.Errorf("SetDisabled accepted %q", src)
		}
	}
}
