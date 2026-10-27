package shim

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Install writes the dsh shim into dir and returns its path. On Unix it is a
// two-line script that execs this dsdock binary, so upgrading dsdock never
// leaves a stale shim. On Windows it is a copy of dsdock.exe named dsh.exe,
// because a .cmd shim makes Ctrl+C ask "Terminate batch job (Y/N)?".
func Install(dir string) (string, error) {
	self, err := os.Executable()
	if err != nil {
		return "", err
	}
	if real, err := filepath.EvalSymlinks(self); err == nil {
		self = real
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	if runtime.GOOS == "windows" {
		dst := filepath.Join(dir, "dsh.exe")
		return dst, copyFile(self, dst)
	}
	dst := filepath.Join(dir, "dsh")
	script := "#!/bin/sh\n# Written by DSDock: runs the DSH release DSDock picks for this profile.\nexec " + shellQuote(self) + " __shim \"$@\"\n"
	tmp := dst + ".dsdock-tmp"
	if err := os.WriteFile(tmp, []byte(script), 0o755); err != nil {
		return "", err
	}
	return dst, os.Rename(tmp, dst)
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".dsdock-tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}

// PathHint tells the user how to put dir first on PATH, without editing any
// of their files.
func PathHint(dir string) string {
	if runtime.GOOS == "windows" {
		return fmt.Sprintf("在 PowerShell 里运行（只影响当前用户）：\n  [Environment]::SetEnvironmentVariable(\"Path\", \"%s;\" + [Environment]::GetEnvironmentVariable(\"Path\", \"User\"), \"User\")\n然后重新打开终端。", dir)
	}
	rc := "~/.bashrc"
	if strings.HasSuffix(os.Getenv("SHELL"), "zsh") || runtime.GOOS == "darwin" {
		rc = "~/.zprofile"
	}
	return fmt.Sprintf("把下面这行加到 %s，然后重新打开终端：\n  export PATH=%s:\"$PATH\"", rc, shellQuote(dir))
}
