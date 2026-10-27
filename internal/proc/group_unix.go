//go:build !windows

package proc

import (
	"os/exec"
	"syscall"
	"time"
)

// newGroup starts the command as the leader of its own process group, so the
// whole tree can be signalled at once.
func newGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killGroup asks the group to stop, then forces it after a grace period.
func killGroup(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	pgid := -cmd.Process.Pid
	_ = syscall.Kill(pgid, syscall.SIGINT)
	for i := 0; i < 30; i++ {
		if syscall.Kill(pgid, 0) != nil {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	_ = syscall.Kill(pgid, syscall.SIGKILL)
}
