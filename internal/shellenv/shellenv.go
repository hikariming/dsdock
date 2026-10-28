// Package shellenv gives an app opened from Finder the PATH the user's
// terminal has.
//
// macOS starts apps from launchd with PATH=/usr/bin:/bin:/usr/sbin:/sbin.
// Everything a user added in ~/.zshrc or ~/.zprofile (Homebrew, nvm, npm's
// global bin, pnpm) is missing, so dsh, node and pnpm are "not installed"
// although the terminal finds them (reported on preview.2).
package shellenv

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	startMark = "__DSDOCK_PATH__"
	endMark   = "__DSDOCK_END__"
)

// Import puts the login shell's PATH in front of this process's PATH, then
// appends common install locations that exist. It returns the directories
// that were not there before.
func Import(ctx context.Context) []string {
	before := filepath.SplitList(os.Getenv("PATH"))
	var dirs []string
	if p, err := LoginPath(ctx, os.Getenv("SHELL")); err == nil {
		dirs = append(dirs, filepath.SplitList(p)...)
	}
	dirs = append(dirs, before...)
	dirs = append(dirs, fallbackDirs()...)
	merged := dedupe(dirs)
	os.Setenv("PATH", strings.Join(merged, string(os.PathListSeparator)))

	had := map[string]bool{}
	for _, d := range before {
		had[d] = true
	}
	var added []string
	for _, d := range merged {
		if !had[d] {
			added = append(added, d)
		}
	}
	return added
}

// LoginPath asks the user's shell for its PATH. An interactive login shell
// reads both ~/.zprofile and ~/.zshrc, where nvm and friends usually live;
// if that fails (a prompt that waits, a broken rc file) a plain login shell
// is tried.
func LoginPath(ctx context.Context, shell string) (string, error) {
	if shell == "" {
		shell = "/bin/zsh"
	}
	script := `printf '\n` + startMark + `%s` + endMark + `\n' "$PATH"`
	if filepath.Base(shell) == "fish" {
		script = `printf '\n` + startMark + `%s` + endMark + `\n' (string join : $PATH)`
	}
	var last error
	for _, flags := range [][]string{{"-l", "-i", "-c"}, {"-l", "-c"}} {
		out, err := run(ctx, shell, append(flags, script)...)
		if p, ok := extract(out); ok {
			return p, nil
		}
		if err == nil {
			err = errors.New("no PATH in the shell's output")
		}
		last = err
	}
	return "", last
}

func run(ctx context.Context, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = nil // an rc file that waits for input gets EOF
	cmd.Env = append(os.Environ(), "DSDOCK_SHELLENV=1")
	out, err := cmd.Output()
	return string(out), err
}

// extract finds the PATH between the markers; rc files may print anything
// around it.
func extract(out string) (string, bool) {
	i := strings.LastIndex(out, startMark)
	if i < 0 {
		return "", false
	}
	rest := out[i+len(startMark):]
	j := strings.Index(rest, endMark)
	if j < 0 {
		return "", false
	}
	p := strings.TrimSpace(rest[:j])
	return p, p != ""
}

// fallbackDirs are where Node, pnpm and npm-installed tools usually are,
// for shells that could not be asked.
func fallbackDirs() []string {
	home, _ := os.UserHomeDir()
	cands := []string{"/opt/homebrew/bin", "/usr/local/bin"}
	if home != "" {
		cands = append(cands,
			filepath.Join(home, ".local", "bin"),
			filepath.Join(home, ".volta", "bin"),
			filepath.Join(home, "Library", "pnpm"),
			filepath.Join(home, ".npm-global", "bin"),
		)
		// nvm: the newest installed Node
		if vs, _ := filepath.Glob(filepath.Join(home, ".nvm", "versions", "node", "*", "bin")); len(vs) > 0 {
			sort.Strings(vs)
			cands = append(cands, vs[len(vs)-1])
		}
	}
	var out []string
	for _, d := range cands {
		if info, err := os.Stat(d); err == nil && info.IsDir() {
			out = append(out, d)
		}
	}
	return out
}

func dedupe(dirs []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, d := range dirs {
		if d == "" || seen[d] {
			continue
		}
		seen[d] = true
		out = append(out, d)
	}
	return out
}
