package preflight

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Kind is where a plugin comes from.
type Kind string

const (
	NPM   Kind = "npm"
	Local Kind = "local"
	Git   Kind = "git"
)

// Spec is what the user asked to install.
type Spec struct {
	Raw     string `json:"raw"`
	Kind    Kind   `json:"kind"`
	Name    string `json:"name,omitempty"`    // npm only
	Version string `json:"version,omitempty"` // npm only: exact version, range or dist-tag
	Path    string `json:"path,omitempty"`    // local only, absolute
}

var (
	reNpmName  = regexp.MustCompile(`^(?:@[a-z0-9][a-z0-9._~-]*/)?[a-z0-9][a-z0-9._~-]*$`)
	reShortGit = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+(?:#.*)?$`)
	reWinPath  = regexp.MustCompile(`^[A-Za-z]:[\\/]`)
)

// ParseSpec classifies an install argument the way pnpm would. Relative
// paths are resolved against cwd so dsh never has to guess.
func ParseSpec(raw, cwd string) (Spec, error) {
	s := Spec{Raw: raw}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return s, errors.New("empty spec")
	}
	switch {
	case strings.HasPrefix(raw, "file:"), strings.HasPrefix(raw, "link:"):
		return local(s, raw[5:], cwd)
	case strings.HasPrefix(raw, "."), strings.HasPrefix(raw, "/"), strings.HasPrefix(raw, "~/"), reWinPath.MatchString(raw):
		return local(s, raw, cwd)
	case strings.HasPrefix(raw, "git+"), strings.HasPrefix(raw, "git://"), strings.HasPrefix(raw, "github:"),
		strings.HasPrefix(raw, "gitlab:"), strings.HasPrefix(raw, "bitbucket:"):
		s.Kind = Git
		return s, nil
	case strings.HasPrefix(raw, "http://"), strings.HasPrefix(raw, "https://"):
		if strings.HasSuffix(strings.SplitN(raw, "#", 2)[0], ".git") || strings.Contains(raw, "github.com/") {
			s.Kind = Git
			return s, nil
		}
		return s, fmt.Errorf("unsupported spec %q: tarball URLs are not supported yet", raw)
	case !strings.HasPrefix(raw, "@") && reShortGit.MatchString(raw):
		s.Kind = Git // owner/repo means GitHub to pnpm
		return s, nil
	}

	name, version := raw, ""
	if i := strings.LastIndex(raw, "@"); i > 0 {
		name, version = raw[:i], raw[i+1:]
	}
	if !reNpmName.MatchString(name) {
		return s, fmt.Errorf("not a package name, path or git address: %q", raw)
	}
	s.Kind, s.Name, s.Version = NPM, name, version
	return s, nil
}

func local(s Spec, p, cwd string) (Spec, error) {
	if strings.HasPrefix(p, "~/") {
		h, err := os.UserHomeDir()
		if err != nil {
			return s, err
		}
		p = filepath.Join(h, p[2:])
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(cwd, p)
	}
	info, err := os.Stat(p)
	if err != nil {
		return s, err
	}
	if !info.IsDir() {
		return s, fmt.Errorf("%s is not a directory (tarballs are not supported yet)", p)
	}
	s.Kind, s.Path = Local, filepath.Clean(p)
	return s, nil
}

// Arg is what DSDock passes to `dsh plugin add`.
func (s Spec) Arg(resolvedVersion string) string {
	switch s.Kind {
	case NPM:
		if resolvedVersion != "" {
			return s.Name + "@" + resolvedVersion
		}
		if s.Version != "" {
			return s.Name + "@" + s.Version
		}
		return s.Name
	case Local:
		return s.Path
	}
	return s.Raw
}
