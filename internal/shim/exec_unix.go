//go:build !windows

package shim

import (
	"fmt"
	"os"
	"syscall"
)

// run replaces this process with node. It only returns on failure.
func run(node string, args []string) int {
	err := syscall.Exec(node, append([]string{node}, args...), os.Environ())
	fmt.Fprintln(os.Stderr, "dsh (DSDock): exec node:", err)
	return 126
}
