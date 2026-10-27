package profile

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func writeProfile(t *testing.T, home, name, pkg string) {
	t.Helper()
	dir := Dir(home, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(pkg), 0o644); err != nil {
		t.Fatal(err)
	}
}

const webPkg = `{
  "name": "dsh-profile-web",
  "private": true,
  "dependencies": { "fx-ok": "link:/x/fx-ok", "@nagi-ovo/dsh-ads": "0.1.2" },
  "dsh": { "profile": { "bundles": ["@deepseek-ai/dsh-base", "@deepseek-ai/dsh-web-app", "fx-ok", "@nagi-ovo/dsh-ads"] } }
}`

func TestLoad(t *testing.T) {
	home := t.TempDir()
	writeProfile(t, home, "web", webPkg)
	p, err := Load(home, "web")
	if err != nil {
		t.Fatal(err)
	}
	if !p.HasWeb() {
		t.Error("HasWeb = false")
	}
	if got, want := p.External(), []string{"fx-ok", "@nagi-ovo/dsh-ads"}; !reflect.DeepEqual(got, want) {
		t.Errorf("External = %v, want %v", got, want)
	}
}

func TestLoadMissing(t *testing.T) {
	_, err := Load(t.TempDir(), "nope")
	if !errors.Is(err, ErrNotExist) {
		t.Fatalf("err = %v", err)
	}
}

func TestListSkipsStaging(t *testing.T) {
	home := t.TempDir()
	writeProfile(t, home, "web", webPkg)
	writeProfile(t, home, "a", `{"dsh":{"profile":{"bundles":["@deepseek-ai/dsh-base"]}}}`)
	if err := os.MkdirAll(filepath.Join(home, "profiles", ".web.staging-1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, "profiles", "empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	ps, err := List(home)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, p := range ps {
		names = append(names, p.Name)
	}
	if want := []string{"a", "web"}; !reflect.DeepEqual(names, want) {
		t.Errorf("names = %v, want %v", names, want)
	}
}

func TestValidName(t *testing.T) {
	for _, bad := range []string{"", ".", "..", "a/b", `a\b`, ".hidden"} {
		if ValidName(bad) == nil {
			t.Errorf("ValidName(%q) = nil", bad)
		}
	}
	if err := ValidName("web"); err != nil {
		t.Error(err)
	}
}

func TestInstalledVersion(t *testing.T) {
	home := t.TempDir()
	writeProfile(t, home, "web", webPkg)
	p, _ := Load(home, "web")
	nm := filepath.Join(p.Dir, "node_modules", "@nagi-ovo", "dsh-ads")
	if err := os.MkdirAll(nm, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nm, "package.json"), []byte(`{"version":"0.1.2"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := p.InstalledVersion("@nagi-ovo/dsh-ads"); got != "0.1.2" {
		t.Errorf("version = %q", got)
	}
	if got := p.InstalledVersion("missing"); got != "" {
		t.Errorf("missing version = %q", got)
	}
}
