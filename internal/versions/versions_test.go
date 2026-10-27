package versions

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hikariming/dsdock/internal/npmreg"
	"github.com/hikariming/dsdock/internal/profile"

	_ "modernc.org/sqlite"
)

func newManager(t *testing.T) *Manager {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	m, err := Open(db, t.TempDir(), npmreg.New("http://127.0.0.1:0"))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// fake registers a release whose entry file exists, without pnpm.
func fake(t *testing.T, m *Manager, name, version string, format int) *Installed {
	t.Helper()
	dir := filepath.Join(m.Root, name)
	entry := filepath.Join(dir, "node_modules", "@deepseek-ai", "dsh", "lib", "bin.js")
	if err := os.MkdirAll(filepath.Dir(entry), 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(entry, []byte("// stub"), 0o644)
	src := Managed
	if name == External {
		src = SourceExt
	}
	i := &Installed{Name: name, Version: version, Dir: dir, Entry: entry, Source: src, SessionFormat: format, InstalledAt: time.Now()}
	if err := m.put(i); err != nil {
		t.Fatal(err)
	}
	return i
}

func TestResolveOrder(t *testing.T) {
	m := newManager(t)
	t.Setenv("DSDOCK_DSH_VERSION", "")
	if _, _, err := m.Resolve("/h", "web"); !errors.Is(err, ErrNoVersion) {
		t.Fatalf("empty manager: %v", err)
	}
	fake(t, m, External, "0.2.1-rc.1", 4)
	fake(t, m, "0.2.1-rc.2", "0.2.1-rc.2", 4)
	fake(t, m, "0.2.1-rc.4", "0.2.1-rc.4", 4)

	check := func(profile, wantName, wantWhy string) {
		t.Helper()
		i, why, err := m.Resolve("/h", profile)
		if err != nil || i.Name != wantName || why != wantWhy {
			t.Fatalf("Resolve(%q) = %v %q %v; want %s %s", profile, i, why, err, wantName, wantWhy)
		}
	}
	check("web", External, ByExternal)
	if err := m.SetDefault("0.2.1-rc.4"); err != nil {
		t.Fatal(err)
	}
	check("web", "0.2.1-rc.4", ByDefault)
	if err := m.Pin("/h", "work", "0.2.1-rc.2"); err != nil {
		t.Fatal(err)
	}
	check("work", "0.2.1-rc.2", ByPin)
	check("web", "0.2.1-rc.4", ByDefault)
	if err := m.Pin("/other-home", "web", "0.2.1-rc.2"); err != nil {
		t.Fatal(err)
	}
	check("web", "0.2.1-rc.4", ByDefault) // pins are per DSH_HOME
	t.Setenv("DSDOCK_DSH_VERSION", "0.2.1-rc.2")
	check("web", "0.2.1-rc.2", ByEnv)
}

func TestSetDefaultRemembersPrevious(t *testing.T) {
	m := newManager(t)
	fake(t, m, "a", "0.2.1-rc.2", 4)
	fake(t, m, "b", "0.2.1-rc.4", 4)
	m.SetDefault("a")
	m.SetDefault("b")
	if prev, _ := m.Previous(); prev != "a" {
		t.Errorf("previous = %q", prev)
	}
	if err := m.SetDefault("missing"); !errors.Is(err, ErrNotInstalled) {
		t.Errorf("SetDefault(missing) = %v", err)
	}
}

func TestUninstallGuards(t *testing.T) {
	m := newManager(t)
	a := fake(t, m, "0.2.1-rc.2", "0.2.1-rc.2", 4)
	fake(t, m, "0.2.1-rc.4", "0.2.1-rc.4", 4)
	m.SetDefault("0.2.1-rc.2")
	if err := m.Uninstall("0.2.1-rc.2"); err == nil || !strings.Contains(err.Error(), "默认") {
		t.Fatalf("uninstalling the default: %v", err)
	}
	m.SetDefault("0.2.1-rc.4")
	m.Pin("/h", "work", "0.2.1-rc.2")
	if err := m.Uninstall("0.2.1-rc.2"); err == nil || !strings.Contains(err.Error(), "work") {
		t.Fatalf("uninstalling a pinned release: %v", err)
	}
	m.Unpin("/h", "work")
	if err := m.Uninstall("0.2.1-rc.2"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(a.Dir); !os.IsNotExist(err) {
		t.Errorf("managed dir not removed: %v", err)
	}
	if _, err := m.Get("0.2.1-rc.2"); !errors.Is(err, ErrNotInstalled) {
		t.Errorf("still recorded: %v", err)
	}
}

func TestMissingFilesCanBeUninstalled(t *testing.T) {
	m := newManager(t)
	i := fake(t, m, "0.2.1-rc.2", "0.2.1-rc.2", 4)
	os.RemoveAll(i.Dir)
	if _, err := m.Get(i.Name); !errors.Is(err, ErrFilesMissing) {
		t.Fatalf("Get = %v", err)
	}
	if err := m.Uninstall(i.Name); err != nil {
		t.Fatal(err)
	}
}

func TestListOrder(t *testing.T) {
	m := newManager(t)
	fake(t, m, External, "0.2.1-rc.4", 4)
	fake(t, m, "0.2.1-rc.2", "0.2.1-rc.2", 4)
	fake(t, m, "0.2.1-rc.10", "0.2.1-rc.10", 4)
	fake(t, m, "0.2.0", "0.2.0", 3)
	list, err := m.List()
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, i := range list {
		names = append(names, i.Name)
	}
	want := "0.2.1-rc.10 0.2.1-rc.2 0.2.0 external"
	if got := strings.Join(names, " "); got != want {
		t.Errorf("order = %s, want %s", got, want)
	}
}

func TestPrecheck(t *testing.T) {
	m := newManager(t)
	from := fake(t, m, "new", "0.2.1-rc.4", 4)
	to := fake(t, m, "old", "0.2.0", 3)
	// The stub composes like dsh 0.2.1-rc.4 did in M0: it skips the
	// incompatible bundle with a warning and exits 0.
	os.WriteFile(to.Entry, []byte(`console.error('dsh: skipping profile bundle "needs-new": Error: Plugin needs-new@1.0.0 is incompatible with dsh 0.2.0: requirements {"engines.dsh":">=0.2.1-rc.1"}. Running it may cause crashes.'); process.exit(0)`+"\n"), 0o644)

	home := t.TempDir()
	dir := profile.Dir(home, "web")
	os.MkdirAll(filepath.Join(dir, "node_modules", "needs-new"), 0o755)
	os.MkdirAll(filepath.Join(dir, "node_modules", "fine"), 0o755)
	os.WriteFile(filepath.Join(dir, "node_modules", "needs-new", "package.json"), []byte(`{"engines":{"dsh":">=0.2.1-rc.1"}}`), 0o644)
	os.WriteFile(filepath.Join(dir, "node_modules", "fine", "package.json"), []byte(`{}`), 0o644)
	p := &profile.Profile{Name: "web", Dir: dir, Bundles: []string{"@deepseek-ai/dsh-base", "needs-new", "fine"},
		Dependencies: map[string]string{"needs-new": "1.0.0", "fine": "1.0.0"}}

	if _, err := lookNode(); err != nil {
		t.Skip("node not on PATH")
	}
	r, err := Precheck(context.Background(), home, []*profile.Profile{p}, from, to)
	if err != nil {
		t.Fatal(err)
	}
	if !r.SessionDowngrade || r.FromFormat != 4 || r.ToFormat != 3 {
		t.Errorf("session downgrade not reported: %+v", r)
	}
	pc := r.Profiles[0]
	if reason := pc.Skipped["needs-new"]; len(pc.Skipped) != 1 || !strings.Contains(reason, "incompatible") || strings.Contains(reason, "crashes") {
		t.Errorf("skipped = %v", pc.Skipped)
	}
	if pc.ComposeError != "" || pc.Incompatible != nil {
		t.Errorf("compose error = %q, static = %v", pc.ComposeError, pc.Incompatible)
	}
	if !r.Problems() {
		t.Error("Problems() = false")
	}

	// When the target cannot compose at all, fall back to the manifests.
	os.WriteFile(to.Entry, []byte("console.error('boom'); process.exit(1)\n"), 0o644)
	r, err = Precheck(context.Background(), home, []*profile.Profile{p}, from, to)
	if err != nil {
		t.Fatal(err)
	}
	pc = r.Profiles[0]
	if pc.ComposeError != "boom" {
		t.Errorf("compose error = %q", pc.ComposeError)
	}
	if _, bad := pc.Incompatible["needs-new"]; !bad || len(pc.Incompatible) != 1 {
		t.Errorf("static fallback = %v", pc.Incompatible)
	}
}

func lookNode() (string, error) {
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		for _, n := range []string{"node", "node.exe"} {
			if _, err := os.Stat(filepath.Join(dir, n)); err == nil {
				return filepath.Join(dir, n), nil
			}
		}
	}
	return "", os.ErrNotExist
}

func TestWorkspaceYAML(t *testing.T) {
	y := workspaceYAML()
	for _, want := range []string{"nodeLinker: hoisted", "strictDepBuilds: false", `"@deepseek-ai/dsh-subprocess-local": true`, `"node-pty": false`} {
		if !strings.Contains(y, want) {
			t.Errorf("workspace yaml lacks %q:\n%s", want, y)
		}
	}
}

func TestFetchNodeRange(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/dsh-v1.0.0/package.json":
			w.Write([]byte(`{"engines":{"node":"^22.19.0 || >=24.0.0"}}`))
		case "/dsh-v1.0.0/apps/cli/package.json":
			w.Write([]byte(`{"name":"@deepseek-ai/dsh"}`))
		case "/dsh-v2.0.0/apps/cli/package.json":
			w.Write([]byte(`{"engines":{"node":">=24.9.0"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	old := RawBase
	RawBase = srv.URL
	defer func() { RawBase = old }()
	ctx := context.Background()
	for v, want := range map[string]string{"1.0.0": "^22.19.0 || >=24.0.0", "2.0.0": ">=24.9.0", "3.0.0": ""} {
		if got := fetchNodeRange(ctx, v); got != want {
			t.Errorf("fetchNodeRange(%s) = %q, want %q", v, got, want)
		}
	}
}

func TestFindBinJS(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "node_modules", "@deepseek-ai", "dsh", "lib", "bin.js")
	os.MkdirAll(filepath.Dir(bin), 0o755)
	os.WriteFile(bin, []byte(""), 0o644)
	// Windows npm prefix: dsh.cmd next to node_modules.
	if got, err := findBinJS(filepath.Join(root, "dsh.cmd")); err != nil || got != bin {
		t.Errorf("from .cmd shim: %q, %v", got, err)
	}
	if got, err := findBinJS(bin); err != nil || got != bin {
		t.Errorf("direct: %q, %v", got, err)
	}
	if _, err := findBinJS(filepath.Join(t.TempDir(), "dsh")); err == nil {
		t.Error("found bin.js where there is none")
	}
}
