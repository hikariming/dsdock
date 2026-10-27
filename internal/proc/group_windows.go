//go:build windows

package proc

import (
	"os/exec"
	"strconv"
	"syscall"
)

const createNewProcessGroup = 0x00000200

func newGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNewProcessGroup}
}

// killGroup ends the process and its children. A Job Object would be more
// precise; this is enough until the Windows M0 results are in.
func killGroup(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	_ = exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid)).Run()
}
