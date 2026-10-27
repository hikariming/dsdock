package dshenv

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNodeProblem(t *testing.T) {
	for v, bad := range map[string]bool{"v18.20.4": true, "v22.23.3": true, "v24.9.0": false, "v24.22.1": false, "v25.0.0": false} {
		msg, err := nodeProblem(v)
		if err != nil {
			t.Fatal(err)
		}
		if (msg != "") != bad {
			t.Errorf("nodeProblem(%s) = %q", v, msg)
		}
	}
}

func TestChildEnvSetsHomeAndNode(t *testing.T) {
	t.Setenv("DSH_HOME", "/elsewhere")
	t.Setenv("PATH", "/usr/bin")
	e := &Env{Home: "/h", Entry: "/x/bin.js", Node: filepath.Join("/opt", "node", "bin", "node")}
	var home, path string
	n := 0
	for _, kv := range e.childEnv() {
		switch {
		case strings.HasPrefix(kv, "DSH_HOME="):
			home = kv
			n++
		case strings.HasPrefix(strings.ToUpper(kv), "PATH="):
			path = "PATH=" + kv[len("PATH="):] // Windows spells it Path
		}
	}
	if home != "DSH_HOME=/h" || n != 1 {
		t.Errorf("DSH_HOME = %q (seen %d times)", home, n)
	}
	want := "PATH=" + filepath.Join("/opt", "node", "bin") + string(os.PathListSeparator) + "/usr/bin"
	if path != want {
		t.Errorf("PATH = %q, want %q", path, want)
	}
}

func TestDiscoverWithOverride(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin.js")
	if err := os.WriteFile(bin, []byte("// stub"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DSDOCK_DSH", bin)
	t.Setenv("DSDOCK_NODE", "/some/node")
	t.Setenv("DSH_HOME", dir)
	e, err := Discover()
	if err != nil {
		t.Fatal(err)
	}
	if e.Entry != bin || e.Node != "/some/node" || e.Home != dir || !e.isScript() {
		t.Errorf("env = %+v", e)
	}
}
