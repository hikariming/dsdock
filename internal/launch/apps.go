package launch

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// App is an installed official DSH desktop app.
type App struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Path string `json:"-"`
}

// known are the apps anywhere-labs/deepseek-harness-desktop ships, as its
// electron-builder settings name them.
var known = []struct {
	id, name, bundleID, exe string
}{
	{"dsh-desktop", "DSH Desktop", "ai.deepseek.dsh.desktop", "dsh-desktop"},
	{"dsh-next", "DSH NEXT", "ai.deepseek.dsh.desktop.next", "dsh-desktop-next"},
}

// DownloadURL is where the desktop app is offered.
const DownloadURL = "https://dshdesktop.cn"

// FindApps lists the official desktop apps installed on this machine.
func FindApps(ctx context.Context) []App {
	home, _ := os.UserHomeDir()
	return findApps(ctx, runtime.GOOS, home, os.Getenv("LOCALAPPDATA"), os.Getenv("ProgramFiles"), spotlight)
}

func findApps(ctx context.Context, goos, home, localAppData, programFiles string, lookup func(context.Context, string) string) []App {
	var out []App
	for _, k := range known {
		var cands []string
		switch goos {
		case "darwin":
			cands = []string{"/Applications/" + k.name + ".app", filepath.Join(home, "Applications", k.name+".app")}
		case "windows":
			// electron-builder's NSIS installer: per user, or for all users.
			for _, root := range []string{filepath.Join(localAppData, "Programs"), programFiles} {
				if root != "" && root != "Programs" {
					cands = append(cands, filepath.Join(root, k.name, k.exe+".exe"))
				}
			}
		default:
			if p, err := exec.LookPath(k.exe); err == nil {
				cands = []string{p}
			}
		}
		found := ""
		for _, c := range cands {
			if _, err := os.Stat(c); err == nil {
				found = c
				break
			}
		}
		if found == "" && goos == "darwin" && lookup != nil {
			found = lookup(ctx, k.bundleID) // installed somewhere else
		}
		if found != "" {
			out = append(out, App{ID: k.id, Name: k.name, Path: found})
		}
	}
	return out
}

// spotlight finds an app bundle by its identifier.
func spotlight(ctx context.Context, bundleID string) string {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	b, err := exec.CommandContext(ctx, "mdfind", "kMDItemCFBundleIdentifier == '"+bundleID+"'").Output()
	if err != nil {
		return ""
	}
	for _, l := range strings.Split(string(b), "\n") {
		if l = strings.TrimSpace(l); strings.HasSuffix(l, ".app") {
			return l
		}
	}
	return ""
}

// Open starts an app, or brings it forward when it already runs.
func Open(a App) error {
	if a.Path == "" {
		return errors.New("没有找到 " + a.Name)
	}
	var cmd *exec.Cmd
	if runtime.GOOS == "darwin" {
		cmd = exec.Command("open", "-a", a.Path)
	} else {
		cmd = exec.Command(a.Path)
		cmd.Dir = filepath.Dir(a.Path)
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }() // the app outlives this call; reap it
	return nil
}
