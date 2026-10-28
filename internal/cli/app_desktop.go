//go:build desktop

package cli

import (
	"context"

	"github.com/hikariming/dsdock/internal/desktop"
)

// HasDesktop reports whether this build includes the native app.
const HasDesktop = true

func (ap *app) cmdApp(ctx context.Context, _ []string) error {
	ap.core.SetProgress(nil) // jobs report progress in the page instead
	return desktop.Run(ap.core, desktop.Options{Version: Version, Show: ap.a.str("show")})
}
