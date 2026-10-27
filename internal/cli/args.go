package cli

import (
	"fmt"
	"strings"
)

// args holds a parsed command line. Flags may appear anywhere, so both
// `dsdock add x --profile web` and `dsdock --profile web add x` work.
type args struct {
	pos   []string
	vals  map[string]string
	bools map[string]bool
}

var (
	valueFlags = map[string]string{"--profile": "profile", "-p": "profile", "--registry": "registry", "--to": "to", "--limit": "limit"}
	boolFlags  = map[string]string{"--json": "json", "--yes": "yes", "-y": "yes", "--smoke": "smoke", "--pre": "pre", "--dry-run": "dry-run", "--force": "force", "--help": "help", "-h": "help", "--available": "available"}
)

func parseArgs(in []string) (*args, error) {
	a := &args{vals: map[string]string{}, bools: map[string]bool{}}
	for i := 0; i < len(in); i++ {
		s := in[i]
		if s == "--" {
			a.pos = append(a.pos, in[i+1:]...)
			break
		}
		name, inline, hasInline := strings.Cut(s, "=")
		if key, ok := valueFlags[name]; ok {
			if hasInline {
				a.vals[key] = inline
				continue
			}
			if i+1 >= len(in) {
				return nil, fmt.Errorf("%s 需要一个值", name)
			}
			i++
			a.vals[key] = in[i]
			continue
		}
		if key, ok := boolFlags[s]; ok {
			a.bools[key] = true
			continue
		}
		if strings.HasPrefix(s, "-") && len(s) > 1 {
			return nil, fmt.Errorf("未知选项 %s", s)
		}
		a.pos = append(a.pos, s)
	}
	return a, nil
}

func (a *args) str(key string) string { return a.vals[key] }
func (a *args) is(key string) bool    { return a.bools[key] }
