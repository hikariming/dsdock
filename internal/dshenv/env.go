// Package dshenv finds the dsh installation DSDock drives and runs it.
//
// DSDock never edits a profile's dependencies itself: every install, removal
// and update goes through `dsh plugin`, and every check goes through
// `dsh --dump-config`. This package is the only place that starts dsh.
package dshenv

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/Masterminds/semver/v3"
)

// MinNode is the Node version dsh's source declares (apps/cli/package.json).
// The published package does not carry `engines`, so DSDock checks it itself.
const MinNode = "24.9.0"

// Env is one dsh installation plus the DSH_HOME it works on.
type Env struct {
	Home  string `json:"home"`
	Entry string `json:"entry"` // bin.js, or an executable such as an npm shim
	Node  string `json:"node,omitempty"`

	version string
}

// Discover reads the environment:
//
//	DSH_HOME      the home dsh uses (default ~/.dsh)
//	DSDOCK_DSH    path to dsh's lib/bin.js or to a dsh executable
//	DSDOCK_NODE   node to run bin.js with (default: node on PATH)
//
// Without DSDOCK_DSH, the dsh on PATH is used.
func Discover() (*Env, error) {
	home := os.Getenv("DSH_HOME")
	if home == "" {
		h, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		home = filepath.Join(h, ".dsh")
	}
	e := &Env{Home: home}

	if p := os.Getenv("DSDOCK_DSH"); p != "" {
		abs, err := filepath.Abs(p)
		if err != nil {
			return nil, err
		}
		if _, err := os.Stat(abs); err != nil {
			return nil, fmt.Errorf("DSDOCK_DSH: %w", err)
		}
		e.Entry = abs
	} else {
		p, err := exec.LookPath("dsh")
		if err != nil {
			return nil, errors.New("dsh not found: install DeepSeek Harness or set DSDOCK_DSH to its lib/bin.js")
		}
		e.Entry = p
	}

	if e.isScript() {
		node := os.Getenv("DSDOCK_NODE")
		if node == "" {
			p, err := exec.LookPath("node")
			if err != nil {
				return nil, errors.New("node not found: set DSDOCK_NODE")
			}
			node = p
		}
		e.Node = node
	}
	return e, nil
}

func (e *Env) isScript() bool {
	ext := strings.ToLower(filepath.Ext(e.Entry))
	return ext == ".js" || ext == ".mjs" || ext == ".cjs"
}

// Command builds a dsh invocation. The child sees DSH_HOME and, when DSDock
// was told which node to use, has that node first on PATH so pnpm and any
// script dsh starts use the same runtime.
func (e *Env) Command(ctx context.Context, args ...string) *exec.Cmd {
	var cmd *exec.Cmd
	if e.isScript() {
		cmd = exec.CommandContext(ctx, e.Node, append([]string{e.Entry}, args...)...)
	} else {
		cmd = exec.CommandContext(ctx, e.Entry, args...)
	}
	cmd.Env = e.childEnv()
	return cmd
}

func (e *Env) childEnv() []string {
	env := os.Environ()
	out := make([]string, 0, len(env)+1)
	for _, kv := range env {
		if strings.HasPrefix(kv, "DSH_HOME=") {
			continue
		}
		if e.Node != "" && strings.HasPrefix(strings.ToUpper(kv), "PATH=") {
			k, v, _ := strings.Cut(kv, "=")
			kv = k + "=" + filepath.Dir(e.Node) + string(os.PathListSeparator) + v
		}
		out = append(out, kv)
	}
	return append(out, "DSH_HOME="+e.Home)
}

// Result is a finished dsh run.
type Result struct {
	Args     []string      `json:"args"`
	Stdout   string        `json:"stdout"`
	Stderr   string        `json:"stderr"`
	ExitCode int           `json:"exitCode"`
	Duration time.Duration `json:"duration"`
}

// OK reports a zero exit code.
func (r *Result) OK() bool { return r.ExitCode == 0 }

// Run executes dsh and captures its output. A non-zero exit is not an error;
// only failing to start dsh at all is.
func (e *Env) Run(ctx context.Context, args ...string) (*Result, error) {
	cmd := e.Command(ctx, args...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	start := time.Now()
	err := cmd.Run()
	r := &Result{Args: args, Stdout: out.String(), Stderr: errb.String(), Duration: time.Since(start)}
	var exit *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &exit):
		r.ExitCode = exit.ExitCode()
	default:
		return r, err
	}
	return r, nil
}

// Plugin runs `dsh plugin --profile <profile> <args...>`, which forwards to
// pnpm in the profile directory.
func (e *Env) Plugin(ctx context.Context, profile string, args ...string) (*Result, error) {
	return e.Run(ctx, append([]string{"plugin", "--profile", profile}, args...)...)
}

// Dump runs `dsh --profile <profile> --dump-config`.
func (e *Env) Dump(ctx context.Context, profile string, patches ...string) (*Result, error) {
	args := []string{"--profile", profile}
	for _, p := range patches {
		args = append(args, "--patch", p)
	}
	return e.Run(ctx, append(args, "--dump-config")...)
}

// DumpDefault runs `dsh --profile <profile> --dump-default-config`: the
// bundle layers only, without the user's patches.
func (e *Env) DumpDefault(ctx context.Context, profile string) (*Result, error) {
	return e.Run(ctx, "--profile", profile, "--dump-default-config")
}

// Version is the dsh version, read once.
func (e *Env) Version(ctx context.Context) (string, error) {
	if e.version != "" {
		return e.version, nil
	}
	r, err := e.Run(ctx, "-V")
	if err != nil {
		return "", err
	}
	if !r.OK() {
		return "", fmt.Errorf("dsh -V exited %d: %s", r.ExitCode, strings.TrimSpace(r.Stderr))
	}
	e.version = strings.TrimSpace(r.Stdout)
	return e.version, nil
}

// CheckNode returns a readable problem when the node dsh runs on is too old.
// Old Node fails inside dsh with an import error nobody can act on.
func (e *Env) CheckNode(ctx context.Context) (string, error) {
	node := e.Node
	if node == "" {
		p, err := exec.LookPath("node")
		if err != nil {
			return "", nil // dsh is a native executable; nothing to check
		}
		node = p
	}
	out, err := exec.CommandContext(ctx, node, "-v").Output()
	if err != nil {
		return "", fmt.Errorf("%s -v: %w", node, err)
	}
	return nodeProblem(strings.TrimSpace(string(out)))
}

func nodeProblem(v string) (string, error) {
	have, err := semver.NewVersion(strings.TrimPrefix(v, "v"))
	if err != nil {
		return "", fmt.Errorf("node version %q: %w", v, err)
	}
	if have.LessThan(semver.MustParse(MinNode)) {
		return fmt.Sprintf("dsh needs Node >= %s, found %s", MinNode, v), nil
	}
	return "", nil
}
