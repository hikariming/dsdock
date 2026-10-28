package launch

import (
	"context"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/hikariming/dsdock/internal/dshenv"
)

func TestParseReady(t *testing.T) {
	good := map[string]string{
		"dsh web: http://127.0.0.1:33071/?token=abc": "http://127.0.0.1:33071/?token=abc",
		"  dsh web: https://dsh.example/?token=x  ":  "https://dsh.example/?token=x",
	}
	for in, want := range good {
		if got, ok := ParseReady(in); !ok || got != want {
			t.Errorf("ParseReady(%q) = %q, %v", in, got, ok)
		}
	}
	for _, bad := range []string{
		"dsh web: opening the default browser; pass --no-open to disable",
		"dsh web: listening on 0.0.0.0 over plain HTTP; restrict this port",
		"[fx-ok] loaded",
		"",
	} {
		if _, ok := ParseReady(bad); ok {
			t.Errorf("ParseReady(%q) accepted", bad)
		}
	}
}

// fake stands in for dsh: it prints script and then waits to be killed.
func fake(t *testing.T, script string) func() *exec.Cmd {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell only")
	}
	return func() *exec.Cmd { return exec.Command("/bin/sh", "-c", script) }
}

func TestWebStartsOnceAndStops(t *testing.T) {
	dir := t.TempDir()
	count := filepath.Join(dir, "count")
	w := &Web{LogPath: filepath.Join(dir, "web.log")}
	cmd := fake(t, `echo x >> `+count+`; echo "[fx] loaded"; echo "dsh web: http://127.0.0.1:4242/?token=SECRETtoken1234"; exec sleep 60`)
	ctx := context.Background()
	u, err := w.start(ctx, cmd)
	if err != nil || u != "http://127.0.0.1:4242/?token=SECRETtoken1234" {
		t.Fatalf("start = %q, %v", u, err)
	}
	if again, err := w.start(ctx, cmd); err != nil || again != u {
		t.Fatalf("second start = %q, %v", again, err)
	}
	if b, _ := os.ReadFile(count); strings.Count(string(b), "x") != 1 {
		t.Errorf("started %d times", strings.Count(string(b), "x"))
	}
	w.Stop()
	if w.URL() != "" {
		t.Error("URL after Stop")
	}
	log, _ := os.ReadFile(w.LogPath)
	if strings.Contains(string(log), "SECRET") || !strings.Contains(string(log), "[fx] loaded") {
		t.Errorf("log = %q", log)
	}
}

func TestWebReportsEarlyExit(t *testing.T) {
	w := &Web{}
	_, err := w.start(context.Background(), fake(t, `echo "dsh: no DeepSeek Harness here" >&2; exit 3`))
	if err == nil || !strings.Contains(err.Error(), "no DeepSeek Harness here") {
		t.Fatalf("err = %v", err)
	}
	if w.URL() != "" {
		t.Error("URL after a failed start")
	}
}

func TestWebRestartsAfterItEnds(t *testing.T) {
	w := &Web{}
	cmd := fake(t, `echo "dsh web: http://127.0.0.1:4343/?token=t"; sleep 0.2`)
	if _, err := w.start(context.Background(), cmd); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for w.URL() != "" && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if w.URL() != "" {
		t.Fatal("still reported running after exit")
	}
	if u, err := w.start(context.Background(), cmd); err != nil || u == "" {
		t.Fatalf("restart = %q, %v", u, err)
	}
	w.Stop()
}

func TestFindApps(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "Applications", "DSH NEXT.app"), 0o755); err != nil {
		t.Fatal(err)
	}
	asked := []string{}
	lookup := func(_ context.Context, id string) string {
		asked = append(asked, id)
		if id == "ai.deepseek.dsh.desktop" {
			return "/Volumes/Apps/DSH Desktop.app"
		}
		return ""
	}
	got := findApps(context.Background(), "darwin", home, "", "", lookup)
	if len(got) != 2 || got[0].Path != "/Volumes/Apps/DSH Desktop.app" || got[1].ID != "dsh-next" {
		t.Fatalf("apps = %+v", got)
	}
	if len(asked) != 1 {
		t.Errorf("Spotlight asked for %v; DSH NEXT was found on disk", asked)
	}

	local := t.TempDir()
	exe := filepath.Join(local, "Programs", "DSH Desktop", "dsh-desktop.exe")
	_ = os.MkdirAll(filepath.Dir(exe), 0o755)
	_ = os.WriteFile(exe, nil, 0o755)
	got = findApps(context.Background(), "windows", home, local, "", nil)
	if len(got) != 1 || got[0].Path != exe {
		t.Fatalf("windows apps = %+v", got)
	}
}

// TestIntegrationWeb starts a real `dsh --profile web` and signs in with the
// address it printed, as the browser will.
func TestIntegrationWeb(t *testing.T) {
	bin := os.Getenv("DSDOCK_IT_DSH")
	if bin == "" {
		t.Skip("set DSDOCK_IT_DSH to run against a real dsh")
	}
	env, err := dshenv.ForEntry(t.TempDir(), bin, "")
	if err != nil {
		t.Fatal(err)
	}
	if n := os.Getenv("DSDOCK_IT_NODE"); n != "" {
		env.Node = n
	}
	w := &Web{LogPath: filepath.Join(t.TempDir(), "web.log")}
	defer w.Stop()
	u, err := w.Start(context.Background(), env)
	if err != nil {
		log, _ := os.ReadFile(w.LogPath)
		t.Fatalf("start: %v\n%s", err, log)
	}
	if strings.Contains(u, ":3080/") {
		t.Errorf("took the default port: %s", u)
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Get(u)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode/100 != 3 || len(resp.Cookies()) == 0 {
		t.Fatalf("signing in with the printed address: %s, cookies %v", resp.Status, resp.Cookies())
	}
	if again, _ := w.Start(context.Background(), env); again != u {
		t.Errorf("second Start started another instance: %s", again)
	}
	w.Stop()
	if _, err := http.Get(u); err == nil {
		t.Error("still serving after Stop")
	}
}
