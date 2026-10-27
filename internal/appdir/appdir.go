// Package appdir decides where DSDock keeps its own data.
package appdir

import (
	"os"
	"path/filepath"
	"runtime"
)

// Dir returns DSDock's data directory. DSDOCK_HOME overrides it, which is how
// tests keep away from the user's real data.
func Dir() (string, error) {
	if d := os.Getenv("DSDOCK_HOME"); d != "" {
		return d, nil
	}
	switch runtime.GOOS {
	case "darwin", "windows":
		base, err := os.UserConfigDir() // ~/Library/Application Support, %APPDATA%
		if err != nil {
			return "", err
		}
		return filepath.Join(base, "DSDock"), nil
	default:
		if x := os.Getenv("XDG_DATA_HOME"); x != "" {
			return filepath.Join(x, "dsdock"), nil
		}
		h, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(h, ".local", "share", "dsdock"), nil
	}
}
