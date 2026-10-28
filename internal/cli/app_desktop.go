//go:build desktop

package cli

import (
	"context"

	"github.com/hikariming/dsdock/internal/desktop"
	"github.com/hikariming/dsdock/internal/shellenv"
)

// HasDesktop reports whether this build includes the native app.
const HasDesktop = true

func (ap *app) cmdApp(ctx context.Context, _ []string) error {
	// Opened from Finder the app has launchd's bare PATH: without the
	// terminal's PATH there is no dsh, node or pnpm to find.
	shellenv.Import(ctx)
	ap.core.SetProgress(nil) // jobs report progress in the page instead
	return desktop.Run(ap.core, desktop.Options{Version: Version, Show: ap.a.str("show")})
}
