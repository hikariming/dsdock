package cli

// End-to-end tests against a real dsh. They run only when DSDOCK_IT_DSH
// points at dsh's lib/bin.js; DSDOCK_IT_NODE optionally names the node to use
// (dsh needs >= 24.9), and DSDOCK_IT_NETWORK=1 enables the npm cases. pnpm
// must be on PATH. Every test uses its own DSH_HOME and DSDOCK_HOME.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/hikariming/dsdock/internal/dshenv"
	"github.com/hikariming/dsdock/internal/snapshot"
)

type itEnv struct {
	t        *testing.T
	home     string
	fixtures string
}

func newIT(t *testing.T) *itEnv {
	t.Helper()
	bin := os.Getenv("DSDOCK_IT_DSH")
	if bin == "" {
		t.Skip("set DSDOCK_IT_DSH to dsh's lib/bin.js to run integration tests")
	}
	root := t.TempDir()
	e := &itEnv{t: t, home: filepath.Join(root, "home"), fixtures: filepath.Join(root, "fixtures")}
	t.Setenv("DSH_HOME", e.home)
	t.Setenv("DSDOCK_HOME", filepath.Join(root, "dsdock"))
	t.Setenv("DSDOCK_DSH", bin)
	if node := os.Getenv("DSDOCK_IT_NODE"); node != "" {
		t.Setenv("DSDOCK_NODE", node)
	}
	t.Setenv("DSDOCK_DSH_VERSION", "")
	if os.Getenv("DSDOCK_IT_NETWORK") != "1" {
		t.Setenv("DSDOCK_NO_CATALOG", "1") // stay offline unless asked
	}
	if err := os.CopyFS(e.fixtures, os.DirFS("testdata/bundles")); err != nil {
		t.Fatal(err)
	}
	return e
}

func (e *itEnv) fixture(name string) string { return filepath.Join(e.fixtures, name) }

// dsh runs dsh directly, for setup that DSDock deliberately does not do.
func (e *itEnv) dsh(args ...string) {
	e.t.Helper()
	env, err := dshenv.Discover("")
	if err != nil {
		e.t.Fatal(err)
	}
	r, err := env.Run(context.Background(), args...)
	if err != nil || !r.OK() {
		e.t.Fatalf("dsh %v: %v\n%s%s", args, err, r.Stdout, r.Stderr)
	}
}

func (e *itEnv) dsdock(args ...string) (int, string) {
	e.t.Helper()
	var out, errb strings.Builder
	code := run(context.Background(), args, &out, &errb, strings.NewReader(""))
	return code, out.String() + errb.String()
}

func (e *itEnv) bundles(profile string) []string {
	e.t.Helper()
	b, err := os.ReadFile(filepath.Join(e.home, "profiles", profile, "package.json"))
	if err != nil {
		e.t.Fatal(err)
	}
	var m struct {
		DSH struct {
			Profile struct {
				Bundles []string `json:"bundles"`
			} `json:"profile"`
		} `json:"dsh"`
	}
	if err := json.Unmarshal(b, &m); err != nil {
		e.t.Fatal(err)
	}
	return m.DSH.Profile.Bundles
}

func (e *itEnv) expect(code int, out string, want int, contains ...string) {
	e.t.Helper()
	if code != want {
		e.t.Fatalf("exit %d, want %d\n%s", code, want, out)
	}
	for _, c := range contains {
		if !strings.Contains(out, c) {
			e.t.Fatalf("output lacks %q\n%s", c, out)
		}
	}
}

var webBase = []string{"@deepseek-ai/dsh-base", "@deepseek-ai/dsh-web-app"}

func TestIntegrationLifecycle(t *testing.T) {
	e := newIT(t)
	e.dsh("plugin", "--profile", "web", "add", e.fixture("fx-ok"))
	start := append(append([]string{}, webBase...), "fx-ok")
	if got := e.bundles("web"); !reflect.DeepEqual(got, start) {
		t.Fatalf("setup bundles = %v", got)
	}

	t.Run("incompatible plugin is refused before install", func(t *testing.T) {
		code, out := e.dsdock("add", e.fixture("fx-incompat"), "-p", "web")
		e.expect(code, out, 5, "PF02")
		if got := e.bundles("web"); !reflect.DeepEqual(got, start) {
			t.Fatalf("bundles changed: %v", got)
		}
	})

	t.Run("duplicate id needs confirmation and is reported", func(t *testing.T) {
		code, out := e.dsdock("add", e.fixture("fx-conflict-a"), "-p", "web")
		e.expect(code, out, 0)
		code, out = e.dsdock("add", e.fixture("fx-conflict-b"), "-p", "web")
		e.expect(code, out, 5, "PF10", "需要确认")
		code, out = e.dsdock("add", e.fixture("fx-conflict-b"), "-p", "web", "--yes")
		e.expect(code, out, 0, "只有 fx-conflict-b 会生效")
	})

	t.Run("plugin that throws on load is rolled back", func(t *testing.T) {
		before := e.bundles("web")
		code, out := e.dsdock("add", e.fixture("fx-broken"), "-p", "web", "--smoke")
		e.expect(code, out, 3, "已回滚", "fx-broken")
		if got := e.bundles("web"); !reflect.DeepEqual(got, before) {
			t.Fatalf("bundles after rollback = %v, want %v", got, before)
		}
	})

	t.Run("disable then enable restores the file byte for byte", func(t *testing.T) {
		path := filepath.Join(e.home, "profiles", "web", "cordis.patch.yml")
		orig, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		code, out := e.dsdock("disable", "fx-ok", "-p", "web")
		e.expect(code, out, 0)
		if b, _ := os.ReadFile(path); !strings.Contains(string(b), "disabled: true") {
			t.Fatalf("patch after disable:\n%s", b)
		}
		code, out = e.dsdock("enable", "fx-ok", "-p", "web")
		e.expect(code, out, 0)
		if b, _ := os.ReadFile(path); string(b) != string(orig) {
			t.Fatalf("patch after enable:\n%s\nwant:\n%s", b, orig)
		}
	})

	t.Run("restore the first snapshot", func(t *testing.T) {
		code, out := e.dsdock("snap", "list", "-p", "web", "--json", "--limit", "1000")
		e.expect(code, out, 0)
		var snaps []snapshot.Snapshot
		if err := json.Unmarshal([]byte(out), &snaps); err != nil || len(snaps) == 0 {
			t.Fatalf("snap list: %v\n%s", err, out)
		}
		first := snaps[len(snaps)-1].ID
		code, out = e.dsdock("snap", "restore", first, "--yes")
		e.expect(code, out, 0, "完成")
		if got := e.bundles("web"); !reflect.DeepEqual(got, start) {
			t.Fatalf("bundles after restore = %v, want %v", got, start)
		}
	})

	t.Run("rescue a plugin installed behind DSDock's back", func(t *testing.T) {
		before := e.bundles("web")
		e.dsh("plugin", "--profile", "web", "add", e.fixture("fx-broken"))
		code, out := e.dsdock("rescue", "-p", "web")
		e.expect(code, out, 7, "fx-broken", "没有激活", "需要确认")
		code, out = e.dsdock("rescue", "-p", "web", "--yes")
		e.expect(code, out, 0, "完成")
		if got := e.bundles("web"); !reflect.DeepEqual(got, before) {
			t.Fatalf("bundles after rescue = %v, want %v", got, before)
		}
	})

	t.Run("rescue a hand-broken patch file", func(t *testing.T) {
		path := filepath.Join(e.home, "profiles", "web", "cordis.patch.yml")
		orig, _ := os.ReadFile(path)
		broken := append(append([]byte{}, orig...), []byte("- id: fx-ok\n  disabled: true\n")...)
		if err := os.WriteFile(path, broken, 0o644); err != nil {
			t.Fatal(err)
		}
		code, out := e.dsdock("rescue", "-p", "web", "--no-boot", "--yes")
		e.expect(code, out, 0, "无法读取", "line")
		if b, _ := os.ReadFile(path); string(b) != string(orig) {
			t.Fatalf("patch after rescue:\n%s", b)
		}
		code, out = e.dsdock("rescue", "-p", "web")
		e.expect(code, out, 0, "没有发现问题")
	})

	t.Run("smoke", func(t *testing.T) {
		code, out := e.dsdock("smoke", "-p", "web")
		e.expect(code, out, 0, "所有插件都已激活")
	})
}

func TestIntegrationNpmUpdate(t *testing.T) {
	if os.Getenv("DSDOCK_IT_NETWORK") != "1" {
		t.Skip("set DSDOCK_IT_NETWORK=1 to install from the npm registry")
	}
	e := newIT(t)
	e.dsh("plugin", "--profile", "web", "add", e.fixture("fx-ok"))
	code, out := e.dsdock("add", "@nagi-ovo/dsh-ads@0.1.2", "-p", "web")
	e.expect(code, out, 0)
	order := e.bundles("web")
	code, out = e.dsdock("update", "@nagi-ovo/dsh-ads", "-p", "web", "--to", "0.1.3")
	e.expect(code, out, 0, "0.1.2 → 0.1.3")
	if got := e.bundles("web"); !reflect.DeepEqual(got, order) {
		t.Fatalf("update changed the stack: %v -> %v", order, got)
	}
}
