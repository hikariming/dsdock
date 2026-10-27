//go:build windows

package proc

import "context"

// FindRunning is not implemented on Windows yet; it waits for the Windows M0
// results (V7, V12).
func FindRunning(ctx context.Context) ([]Running, error) { return nil, nil }
