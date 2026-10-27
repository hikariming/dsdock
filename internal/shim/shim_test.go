package shim

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestTargetProfile(t *testing.T) {
	cases := map[string]string{
		"web":                               "web",
		"web --port 8080":                   "web",
		"--profile work --no-open":          "work",
		"--profile=work":                    "work",
		"plugin --profile work add x":       "work",
		"plugin add x":                      "",
		"--profile t1 --dump-config":        "t1",
		"-V":                                "",
		"":                                  "",
		"tui --patch ./extra.yml":           "tui",
		"rescue --from-default-profile web": "rescue",
	}
	for in, want := range cases {
		if got := TargetProfile(strings.Fields(in)); got != want {
			t.Errorf("TargetProfile(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestInstallScript(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix shim")
	}
	dir := t.TempDir()
	p, err := Install(dir)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	if !strings.HasPrefix(string(b), "#!/bin/sh\n") || !strings.Contains(string(b), `__shim "$@"`) {
		t.Fatalf("script:\n%s", b)
	}
	info, _ := os.Stat(p)
	if info.Mode()&0o111 == 0 {
		t.Error("shim is not executable")
	}
	if _, err := exec.LookPath("sh"); err == nil {
		if out, err := exec.Command("sh", "-n", p).CombinedOutput(); err != nil {
			t.Errorf("sh -n: %v %s", err, out)
		}
	}
	if filepath.Base(p) != "dsh" {
		t.Errorf("name = %s", p)
	}
}
