// Command dsdock manages DeepSeek Harness from the outside: plugin
// installs, updates, snapshots, rollbacks, rescue and DSH releases.
//
// Run as `dsh` (a copy named dsh.exe on Windows) or as `dsdock __shim` (the
// Unix shim script), it forwards to the DSH release DSDock picks.
package main

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/hikariming/dsdock/internal/cli"
	"github.com/hikariming/dsdock/internal/shim"
)

func main() {
	name := strings.TrimSuffix(strings.ToLower(filepath.Base(os.Args[0])), ".exe")
	switch {
	case name == "dsh":
		os.Exit(shim.Main(os.Args[1:]))
	case len(os.Args) > 1 && os.Args[1] == "__shim":
		os.Exit(shim.Main(os.Args[2:]))
	}
	os.Exit(cli.Main(os.Args[1:]))
}
