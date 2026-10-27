package preflight

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hikariming/dsdock/internal/layer"
	"github.com/hikariming/dsdock/internal/npmreg"
	"github.com/hikariming/dsdock/internal/profile"
)

func TestParseSpec(t *testing.T) {
	cwd := t.TempDir()
	if err := os.Mkdir(filepath.Join(cwd, "plug"), 0o755); err != nil {
		t.Fatal(err)
	}
	cases := map[string]Spec{
		"dsh-meme":                      {Kind: NPM, Name: "dsh-meme"},
		"dsh-meme@0.1.62":               {Kind: NPM, Name: "dsh-meme", Version: "0.1.62"},
		"@nagi-ovo/dsh-ads":             {Kind: NPM, Name: "@nagi-ovo/dsh-ads"},
		"@nagi-ovo/dsh-ads@^0.1":        {Kind: NPM, Name: "@nagi-ovo/dsh-ads", Version: "^0.1"},
		"dsh-meme@next":                 {Kind: NPM, Name: "dsh-meme", Version: "next"},
		"./plug":                        {Kind: Local, Path: filepath.Join(cwd, "plug")},
		"file:./plug":                   {Kind: Local, Path: filepath.Join(cwd, "plug")},
		"github:owner/repo":             {Kind: Git},
		"owner/repo":                    {Kind: Git},
		"git+https://x.test/a/b.git":    {Kind: Git},
		"https://github.com/owner/repo": {Kind: Git},
	}
	for raw, want := range cases {
		got, err := ParseSpec(raw, cwd)
		if err != nil {
			t.Errorf("%s: %v", raw, err)
			continue
		}
		want.Raw = raw
		if got != want {
			t.Errorf("%s: got %+v, want %+v", raw, got, want)
		}
	}
	for _, bad := range []string{"", "Not A Name", "./missing", "https://x.test/a.tgz"} {
		if _, err := ParseSpec(bad, cwd); err == nil {
			t.Errorf("ParseSpec(%q) succeeded", bad)
		}
	}
}

func TestSafePatchPath(t *testing.T) {
	for p, ok := range map[string]bool{
		"./cordis.patch.yml": true, "patches/web.yml": true,
		"": false, "/etc/x": false, "../x": false, "a/../../x": false, `a\b`: false, "C:/x": false, ".": false,
	} {
		if safePatchPath(p) != ok {
			t.Errorf("safePatchPath(%q) = %v", p, !ok)
		}
	}
}

// writePlugin creates a local bundle like the M0 fixtures.
func writePlugin(t *testing.T, dir, name, extra, ids string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
	pkg := `{"name":"` + name + `","version":"1.0.0",` + extra + `"dsh":{"bundle":{"patch":"./cordis.patch.yml"}}}`
	os.WriteFile(filepath.Join(p, "package.json"), []byte(pkg), 0o644)
	var b strings.Builder
	b.WriteString("- insert:\n")
	for _, id := range strings.Split(ids, ",") {
		b.WriteString("    - id: " + id + "\n      name: " + name + "\n")
	}
	os.WriteFile(filepath.Join(p, "cordis.patch.yml"), []byte(b.String()), 0o644)
	return p
}

func rules(r *Report) map[string]Level {
	out := map[string]Level{}
	for _, f := range r.Findings {
		out[f.Rule] = f.Level
	}
	return out
}

func checkLocal(t *testing.T, c *Checker, dir string, mode Mode) *Report {
	t.Helper()
	s, err := ParseSpec(dir, "/")
	if err != nil {
		t.Fatal(err)
	}
	cand, err := c.Resolve(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	return c.Check(cand, mode)
}

func TestLocalRules(t *testing.T) {
	dir := t.TempDir()
	tree := &layer.Tree{Rows: []layer.Row{{ID: "fx-same", Source: "fx-conflict-a"}, {ID: "web", Source: "@deepseek-ai/dsh-web-app"}}}
	prof := &profile.Profile{Name: "web", Dependencies: map[string]string{"fx-conflict-a": "link:/x"}}
	c := &Checker{DSHVersion: "0.2.1-rc.4", NodeVersion: "24.22.1", Profile: prof, Tree: tree}

	ok := checkLocal(t, c, writePlugin(t, dir, "fx-ok", "", "fx-ok"), ModeAdd)
	if len(ok.Findings) != 0 {
		t.Errorf("fx-ok findings = %+v", ok.Findings)
	}

	incompat := checkLocal(t, c, writePlugin(t, dir, "fx-incompat", `"engines":{"dsh":">=99.0.0"},`, "fx-incompat"), ModeAdd)
	if rules(incompat)["PF02"] != Block || !incompat.Blocked() {
		t.Errorf("fx-incompat = %+v", incompat.Findings)
	}

	dup := checkLocal(t, c, writePlugin(t, dir, "fx-conflict-b", "", "fx-same"), ModeAdd)
	if rules(dup)["PF10"] != Warn || dup.Blocked() {
		t.Errorf("fx-conflict-b = %+v", dup.Findings)
	}
	if !strings.Contains(dup.Findings[0].Message, "fx-conflict-a") {
		t.Errorf("PF10 should name the plugin that loses: %s", dup.Findings[0].Message)
	}

	again := checkLocal(t, c, writePlugin(t, dir, "fx-conflict-a", "", "fx-same"), ModeAdd)
	if rules(again)["PF03"] != Block {
		t.Errorf("reinstall = %+v", again.Findings)
	}
	if _, clash := rules(again)["PF10"]; clash {
		t.Error("a plugin clashing with its own rows reported PF10")
	}

	scripts := checkLocal(t, c, writePlugin(t, dir, "fx-script", `"scripts":{"prepare":"node build.mjs","test":"x"},`, "fx-script"), ModeAdd)
	if rules(scripts)["PF04"] != Warn {
		t.Errorf("fx-script = %+v", scripts.Findings)
	}

	node := checkLocal(t, c, writePlugin(t, dir, "fx-node", `"engines":{"node":">=30"},`, "fx-node"), ModeAdd)
	if rules(node)["PF08"] != Warn {
		t.Errorf("fx-node = %+v", node.Findings)
	}
}

func TestNotABundle(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "lib")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"name":"lib","version":"1.0.0"}`), 0o644)
	r := checkLocal(t, &Checker{DSHVersion: "0.2.1-rc.4"}, dir, ModeAdd)
	if rules(r)["PF01"] != Block {
		t.Errorf("findings = %+v", r.Findings)
	}
}

func TestGitIsWarnedOnly(t *testing.T) {
	c := &Checker{DSHVersion: "0.2.1-rc.4"}
	s, _ := ParseSpec("github:owner/repo", "/")
	cand, _ := c.Resolve(context.Background(), s)
	r := c.Check(cand, ModeAdd)
	if rules(r)["PF05"] != Warn || r.Blocked() {
		t.Errorf("findings = %+v", r.Findings)
	}
	if cand.Arg() != "github:owner/repo" {
		t.Errorf("arg = %s", cand.Arg())
	}
}

func TestPickVersion(t *testing.T) {
	// latest (2.0.0) needs a future dsh, 1.5.0 is deprecated, 1.6.0-beta is a
	// prerelease: the pick should be 1.4.0.
	packument := `{"name":"p","dist-tags":{"latest":"2.0.0"},"versions":{
		"1.4.0":{"version":"1.4.0","engines":{"dsh":">=0.2.0"}},
		"1.5.0":{"version":"1.5.0","deprecated":"broken"},
		"1.6.0-beta.1":{"version":"1.6.0-beta.1"},
		"2.0.0":{"version":"2.0.0","engines":{"dsh":">=0.3.0"}}}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(packument))
	}))
	defer srv.Close()
	c := &Checker{Registry: npmreg.New(srv.URL), DSHVersion: "0.2.1-rc.4"}
	ctx := context.Background()
	for spec, want := range map[string]string{"p": "1.4.0", "p@^1.0.0": "1.4.0", "p@2.0.0": "2.0.0", "p@latest": "2.0.0"} {
		s, _ := ParseSpec(spec, "/")
		got, err := c.pickVersion(ctx, s)
		if err != nil || got != want {
			t.Errorf("%s: got %q, %v; want %s", spec, got, err, want)
		}
	}
	c.AllowPre = true
	s, _ := ParseSpec("p", "/")
	if got, _ := c.pickVersion(ctx, s); got != "1.6.0-beta.1" {
		t.Errorf("with prereleases: %s", got)
	}
	c.DSHVersion, c.AllowPre = "0.1.0", false
	if _, err := c.pickVersion(ctx, s); err == nil {
		t.Error("expected no compatible version for dsh 0.1.0")
	}
}
