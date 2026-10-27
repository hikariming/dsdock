package cli

import (
	"context"
	"os/exec"
)

func execOutput(ctx context.Context, name string, args ...string) (string, error) {
	out, err := exec.CommandContext(ctx, name, args...).Output()
	return string(out), err
}
