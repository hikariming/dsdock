//go:build windows

package shim

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
)

// run starts node in the same console and waits. Ctrl+C reaches both
// processes; the shim ignores it and lets dsh decide how to shut down.
// TODO(windows M0, V12): confirm no orphaned node after closing the console,
// and move to a Job Object if there is.
func run(node string, args []string) int {
	signal.Ignore(os.Interrupt)
	cmd := exec.Command(node, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	err := cmd.Run()
	var exit *exec.ExitError
	switch {
	case err == nil:
		return 0
	case errors.As(err, &exit):
		return exit.ExitCode()
	}
	fmt.Fprintln(os.Stderr, "dsh (DSDock): start node:", err)
	return 126
}
