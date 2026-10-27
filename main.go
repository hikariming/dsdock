// Command dsdock manages DeepSeek Harness from the outside: plugin
// installs, updates, snapshots, rollbacks and rescue.
package main

import (
	"os"

	"github.com/hikariming/dsdock/internal/cli"
)

func main() {
	os.Exit(cli.Main(os.Args[1:]))
}
