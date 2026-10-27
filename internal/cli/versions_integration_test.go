package cli

// Version management end to end: install releases with pnpm, forward through
// the dsh shim, pin and switch. Needs DSDOCK_IT_NETWORK=1, pnpm and a Node
// that dsh accepts (DSDOCK_IT_NODE, or node on PATH). Unix only, because it
// drives the shell shim.

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type vIT struct {
	t        *testing.T
	bin      string // a real dsdock binary
	shimDir  string
	env      []string
	fixtures string
}

func newVersionsIT(t *testing.T) *vIT {
	t.Helper()
	if os.Getenv("DSDOCK_IT_NETWORK") != "1" {
		t.Skip("set DSDOCK_IT_NETWORK=1 to install DSH releases")
	}
	if runtime.GOOS == "windows" {
		t.Skip("drives the Unix shim script")
	}
	if _, err := exec.LookPath("pnpm"); err != nil {
		t.Skip("pnpm not on PATH")
	}
	root := t.TempDir()
	v := &vIT{t: t, bin: filepath.Join(root, "dsdock"), fixtures: filepath.Join(root, "fixtures")}
	build := exec.Command("go", "build", "-o", v.bin, "../..")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	if err := os.CopyFS(v.fixtures, os.DirFS("testdata/bundles")); err != nil {
		t.Fatal(err)
	}
	dataDir := filepath.Join(root, "dsdock-home")
	v.shimDir = filepath.Join(dataDir, "bin")
	var env []string
	for _, kv := range os.Environ() {
		switch k, _, _ := strings.Cut(kv, "="); k {
		case "DSDOCK_DSH", "DSDOCK_DSH_VERSION", "DSH_HOME", "DSDOCK_HOME", "PATH":
			continue
		}
		env = append(env, kv)
	}
	path := v.shimDir + string(os.PathListSeparator) + os.Getenv("PATH")
	if node := os.Getenv("DSDOCK_IT_NODE"); node != "" {
		env = append(env, "DSDOCK_NODE="+node)
		path = filepath.Dir(node) + string(os.PathListSeparator) + path
	}
	v.env = append(env, "PATH="+path, "DSH_HOME="+filepath.Join(root, "home"), "DSDOCK_HOME="+dataDir)
	return v
}

func (v *vIT) run(name string, args ...string) (int, string) {
	v.t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Env = v.env
	out, err := cmd.CombinedOutput()
	code := 0
	if exit, ok := err.(*exec.ExitError); ok {
		code = exit.ExitCode()
	} else if err != nil {
		v.t.Fatalf("%s %v: %v", name, args, err)
	}
	return code, string(out)
}

func (v *vIT) must(name string, args ...string) string {
	v.t.Helper()
	code, out := v.run(name, args...)
	if code != 0 {
		v.t.Fatalf("%s %v exited %d\n%s", filepath.Base(name), args, code, out)
	}
	return out
}

func (v *vIT) which(profile string) (version, reason string) {
	v.t.Helper()
	args := []string{"dsh", "which", "--json"}
	if profile != "" {
		args = append(args, "-p", profile)
	}
	var w struct{ Version, Reason string }
	if err := json.Unmarshal([]byte(v.must(v.bin, args...)), &w); err != nil {
		v.t.Fatal(err)
	}
	return w.Version, w.Reason
}

func TestIntegrationVersions(t *testing.T) {
	v := newVersionsIT(t)
	dsh := filepath.Join(v.shimDir, "dsh")

	// The first release installed becomes the default.
	v.must(v.bin, "dsh", "install", "latest")
	latest, why := v.which("")
	if why != "default" || latest == "" {
		t.Fatalf("after first install: %s (%s)", latest, why)
	}

	v.must(v.bin, "dsh", "shim")
	if got := strings.TrimSpace(v.must(dsh, "-V")); got != latest {
		t.Fatalf("dsh -V through the shim = %q, want %s", got, latest)
	}
	v.must(dsh, "plugin", "--profile", "web", "add", filepath.Join(v.fixtures, "fx-ok"))
	v.must(v.bin, "status", "-p", "web")

	v.must(v.bin, "dsh", "install", "next")
	var list struct {
		Installed []struct{ Name string }
	}
	json.Unmarshal([]byte(v.must(v.bin, "dsh", "list", "--json")), &list)
	next := ""
	for _, i := range list.Installed {
		if i.Name != latest {
			next = i.Name
		}
	}
	if next == "" {
		t.Skip("latest and next are the same release; nothing to switch between")
	}

	// A pin wins over the default, through the shim as well.
	v.must(v.bin, "dsh", "pin", next, "-p", "web", "--yes")
	if got, why := v.which("web"); got != next || why != "pin" {
		t.Fatalf("web resolves to %s (%s), want %s (pin)", got, why, next)
	}
	cmd := exec.Command(dsh, "--profile", "web", "--dump-config")
	cmd.Env = append(v.env, "DSDOCK_DSH_VERSION=")
	if out, err := cmd.CombinedOutput(); err != nil || !strings.Contains(string(out), "# == fx-ok") {
		t.Fatalf("dump through the shim: %v\n%s", err, out)
	}

	// The default cannot be uninstalled; after switching it can.
	if code, out := v.run(v.bin, "dsh", "uninstall", latest); code == 0 {
		t.Fatalf("uninstalled the default:\n%s", out)
	}
	v.must(v.bin, "dsh", "use", next, "--yes")
	v.must(v.bin, "dsh", "unpin", "-p", "web")
	v.must(v.bin, "dsh", "uninstall", latest)
	if got, why := v.which("web"); got != next || why != "default" {
		t.Fatalf("after switching: %s (%s)", got, why)
	}
}
