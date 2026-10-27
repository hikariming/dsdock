//go:build !windows

package proc

import (
	"context"
	"os/exec"
)

// FindRunning lists dsh processes with a booted profile.
func FindRunning(ctx context.Context) ([]Running, error) {
	out, err := exec.CommandContext(ctx, "ps", "-axo", "pid=,args=").Output()
	if err != nil {
		return nil, err
	}
	return parsePS(string(out)), nil
}
