package proc

import (
	"strconv"
	"strings"
)

// Running is a dsh process that has a profile booted.
type Running struct {
	PID     int    `json:"pid"`
	Profile string `json:"profile"`
	Command string `json:"command"`
}

// parsePS reads `ps -axo pid=,args=` output.
func parsePS(out string) []Running {
	var found []Running
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}
		if prof, ok := profileFromArgs(fields[1:]); ok {
			found = append(found, Running{PID: pid, Profile: prof, Command: strings.Join(fields[1:], " ")})
		}
	}
	return found
}

// profileFromArgs finds the profile a dsh command line boots. `dsh plugin`
// and other management invocations return false.
func profileFromArgs(args []string) (string, bool) {
	i := -1
	for j, a := range args {
		a = strings.ReplaceAll(a, `\`, "/")
		if strings.HasSuffix(a, "@deepseek-ai/dsh/lib/bin.js") || strings.HasSuffix(a, "/bin/dsh") || a == "dsh" {
			i = j
			break
		}
	}
	if i < 0 {
		return "", false
	}
	rest := args[i+1:]
	if len(rest) > 0 && rest[0] == "plugin" {
		return "", false
	}
	for _, a := range rest {
		switch a {
		case "--dump-config", "--dump-default-config", "--dump-config-schema", "-V", "--version", "--help", "-h":
			return "", false // these exit without booting
		}
	}
	for j, a := range rest {
		switch {
		case a == "--profile" && j+1 < len(rest):
			return rest[j+1], true
		case strings.HasPrefix(a, "--profile="):
			return strings.TrimPrefix(a, "--profile="), true
		case j == 0 && !strings.HasPrefix(a, "-"):
			return a, true // `dsh web` shorthand
		}
	}
	return "", false
}
