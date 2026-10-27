// Package shim is DSDock running as `dsh`: it picks the DSH release for the
// profile being started and hands over to it.
//
// The shim prints nothing of its own on success, so scripts that parse dsh's
// output keep working. On Unix it replaces itself with Node (exec), which M0
// showed is the only way signals reach dsh cleanly; a wrapper that waits for
// a child can leave an orphaned Node process behind.
package shim

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/hikariming/dsdock/internal/appdir"
	"github.com/hikariming/dsdock/internal/dshenv"
	"github.com/hikariming/dsdock/internal/npmreg"
	"github.com/hikariming/dsdock/internal/snapshot"
	"github.com/hikariming/dsdock/internal/versions"
)

// TargetProfile finds the profile a dsh command line works on, including
// management commands (`dsh plugin --profile x`) and dumps, since those must
// use the same release as the profile itself.
func TargetProfile(args []string) string {
	rest := args
	if len(rest) > 0 && rest[0] == "plugin" {
		rest = rest[1:]
	}
	for i, a := range rest {
		switch {
		case a == "--profile" && i+1 < len(rest):
			return rest[i+1]
		case strings.HasPrefix(a, "--profile="):
			return strings.TrimPrefix(a, "--profile=")
		}
	}
	if len(args) > 0 && args[0] != "plugin" && !strings.HasPrefix(args[0], "-") {
		return args[0] // `dsh web` shorthand
	}
	return ""
}

// Main runs the shim with dsh's arguments and returns an exit code when it
// does not exec.
func Main(args []string) int {
	inst, err := resolve(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, "dsh (DSDock):", err)
		return 127
	}
	node, err := dshenv.NodeBinary()
	if err != nil {
		fmt.Fprintln(os.Stderr, "dsh (DSDock):", err)
		return 127
	}
	if problem := nodeProblem(node, inst.NodeRange); problem != "" {
		fmt.Fprintf(os.Stderr, "dsh (DSDock): %s。\n  换一个 Node，或者设置 DSDOCK_NODE 指向满足要求的 node。\n", problem)
		return 1
	}
	return run(node, append([]string{inst.Entry}, args...))
}

func resolve(args []string) (*versions.Installed, error) {
	dir, err := appdir.Dir()
	if err != nil {
		return nil, err
	}
	store, err := snapshot.Open(dir)
	if err != nil {
		return nil, err
	}
	defer store.Close()
	m, err := versions.Open(store.DB(), "", npmreg.New(""))
	if err != nil {
		return nil, err
	}
	home, err := dshenv.Home()
	if err != nil {
		return nil, err
	}
	inst, _, err := m.Resolve(home, TargetProfile(args))
	if errors.Is(err, versions.ErrNoVersion) {
		return nil, errors.New("DSDock 还没有管理任何 DSH 版本：先运行 dsdock dsh install latest，或 dsdock dsh adopt 接管现有安装")
	}
	return inst, err
}

func nodeProblem(node, rng string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, node, "-v")
	cmd.Stderr = io.Discard
	out, err := cmd.Output()
	if err != nil {
		return "" // let dsh report it
	}
	msg, _ := dshenv.NodeProblem(strings.TrimSpace(string(out)), rng)
	return msg
}
