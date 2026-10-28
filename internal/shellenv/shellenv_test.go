package shellenv

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestExtract(t *testing.T) {
	noisy := "Last login: Tue\nwelcome to oh-my-zsh\n\n" + startMark + "/a/bin:/b/bin" + endMark + "\nbye\n"
	if p, ok := extract(noisy); !ok || p != "/a/bin:/b/bin" {
		t.Fatalf("extract = %q, %v", p, ok)
	}
	for _, bad := range []string{"", "no markers", startMark + "unterminated", startMark + endMark} {
		if _, ok := extract(bad); ok {
			t.Errorf("extract(%q) succeeded", bad)
		}
	}
}

// fakeShell writes a script standing in for the user's shell: it prints rc
// noise, then runs the command it was given with PATH set as an rc file
// would.
func fakeShell(t *testing.T, rcPath string, interactiveWorks bool) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shells only")
	}
	dir := t.TempDir()
	sh := filepath.Join(dir, "fakesh")
	guard := ""
	if !interactiveWorks {
		guard = `case "$*" in *-i*) echo "broken rc" >&2; exit 1;; esac`
	}
	script := "#!/bin/sh\n" + guard + "\necho 'rc noise'\nPATH=" + rcPath + "\nexport PATH\nfor last; do :; done\nexec /bin/sh -c \"$last\"\n"
	if err := os.WriteFile(sh, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return sh
}

func TestLoginPath(t *testing.T) {
	sh := fakeShell(t, "/from/zshrc/bin:/usr/bin:/bin", true)
	p, err := LoginPath(context.Background(), sh)
	if err != nil || p != "/from/zshrc/bin:/usr/bin:/bin" {
		t.Fatalf("LoginPath = %q, %v", p, err)
	}
}

func TestLoginPathFallsBackWithoutInteractive(t *testing.T) {
	sh := fakeShell(t, "/from/zprofile/bin:/usr/bin:/bin", false)
	p, err := LoginPath(context.Background(), sh)
	if err != nil || p != "/from/zprofile/bin:/usr/bin:/bin" {
		t.Fatalf("LoginPath = %q, %v", p, err)
	}
}

func TestImportPutsShellPathFirst(t *testing.T) {
	sh := fakeShell(t, "/shell/first:/usr/bin", true)
	t.Setenv("SHELL", sh)
	t.Setenv("PATH", "/usr/bin:/bin:/usr/sbin:/sbin") // what launchd gives an app
	added := Import(context.Background())
	got := filepath.SplitList(os.Getenv("PATH"))
	if got[0] != "/shell/first" {
		t.Fatalf("PATH = %v", got)
	}
	if !strings.Contains(strings.Join(added, ":"), "/shell/first") {
		t.Errorf("added = %v", added)
	}
	seen := map[string]int{}
	for _, d := range got {
		seen[d]++
		if seen[d] > 1 {
			t.Errorf("%s appears twice in %v", d, got)
		}
	}
	if seen["/bin"] != 1 || seen["/usr/sbin"] != 1 {
		t.Errorf("the original PATH was not kept: %v", got)
	}
}
