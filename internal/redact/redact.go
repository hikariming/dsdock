// Package redact removes secrets from text DSDock stores or shows.
//
// dsh prints the Web access token on startup, and plugin errors can carry
// whatever configuration they were given, so every log line that leaves a
// child process goes through Text before it is kept.
package redact

import "regexp"

var rules = []struct {
	re   *regexp.Regexp
	repl string
}{
	{regexp.MustCompile(`([?&]token=)[A-Za-z0-9_\-]+`), "${1}<redacted>"},
	{regexp.MustCompile(`\bsk-[A-Za-z0-9]{16,}`), "sk-<redacted>"},
	{regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{20,}`), "gh_<redacted>"},
	{regexp.MustCompile(`(?i)(authorization:\s*bearer\s+)\S+`), "${1}<redacted>"},
	{regexp.MustCompile(`(?i)((?:api[_-]?key|secret|password|token)["']?\s*[:=]\s*["']?)[^\s"',}]{6,}`), "${1}<redacted>"},
}

// Text returns s with known secret shapes replaced.
func Text(s string) string {
	for _, r := range rules {
		s = r.re.ReplaceAllString(s, r.repl)
	}
	return s
}
