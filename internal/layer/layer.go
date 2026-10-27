// Package layer parses what `dsh --dump-config` prints and the warnings dsh
// writes to stderr, so the rest of DSDock never has to look at raw text.
//
// The dump is one YAML sequence. Before each run of rows, dsh prints a comment
// naming where those rows came from:
//
//	# == fx-ok
//	# == fx-ok, patched by /home/u/.dsh/profiles/t1/cordis.patch.yml
//
// Only top-level rows are modelled; groups and includes are left opaque.
package layer

import (
	"bufio"
	"fmt"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// Disabled states a row can be in. dsh accepts booleans, null, or a `!!js`
// expression whose value is only known at runtime.
const (
	Enabled      = ""
	Disabled     = "true"
	DisabledExpr = "expr"
)

// Row is one top-level entry of the composed tree.
type Row struct {
	ID        string   `json:"id,omitempty"`
	Name      string   `json:"name,omitempty"`
	Disabled  string   `json:"disabled,omitempty"`
	Source    string   `json:"source"`
	PatchedBy []string `json:"patchedBy,omitempty"`
}

// Tree is a parsed dump.
type Tree struct {
	Rows []Row `json:"rows"`
}

const headerPrefix = "# == "

// Parse reads the stdout of `dsh --dump-config`.
func Parse(dump string) (*Tree, error) {
	t := &Tree{}
	var source string
	var patchedBy []string
	var body strings.Builder

	flush := func() error {
		if strings.TrimSpace(body.String()) == "" {
			body.Reset()
			return nil
		}
		rows, err := parseRows(body.String())
		if err != nil {
			return fmt.Errorf("layer %q: %w", source, err)
		}
		for _, r := range rows {
			r.Source = source
			r.PatchedBy = patchedBy
			t.Rows = append(t.Rows, r)
		}
		body.Reset()
		return nil
	}

	sc := bufio.NewScanner(strings.NewReader(dump))
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		if strings.HasPrefix(line, headerPrefix) {
			if err := flush(); err != nil {
				return nil, err
			}
			source, patchedBy = parseHeader(strings.TrimPrefix(line, headerPrefix))
			continue
		}
		body.WriteString(line)
		body.WriteByte('\n')
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if err := flush(); err != nil {
		return nil, err
	}
	return t, nil
}

func parseHeader(h string) (string, []string) {
	name, rest, ok := strings.Cut(h, ", patched by ")
	if !ok {
		return strings.TrimSpace(h), nil
	}
	var files []string
	for _, f := range strings.Split(rest, ", ") {
		if f = strings.TrimSpace(f); f != "" {
			files = append(files, f)
		}
	}
	return strings.TrimSpace(name), files
}

func parseRows(src string) ([]Row, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(src), &doc); err != nil {
		return nil, err
	}
	if len(doc.Content) == 0 {
		return nil, nil
	}
	seq := doc.Content[0]
	if seq.Kind != yaml.SequenceNode {
		return nil, fmt.Errorf("expected a sequence, got %v", seq.Kind)
	}
	rows := make([]Row, 0, len(seq.Content))
	for _, item := range seq.Content {
		if item.Kind != yaml.MappingNode {
			continue
		}
		var r Row
		for i := 0; i+1 < len(item.Content); i += 2 {
			k, v := item.Content[i], item.Content[i+1]
			switch k.Value {
			case "id":
				r.ID = v.Value
			case "name":
				r.Name = v.Value
			case "disabled":
				r.Disabled = disabledState(v)
			}
		}
		rows = append(rows, r)
	}
	return rows, nil
}

func disabledState(v *yaml.Node) string {
	if v.Tag == "!!js" {
		return DisabledExpr
	}
	if v.Tag == "!!bool" && v.Value == "true" {
		return Disabled
	}
	return Enabled
}

// Sources returns each layer name once, in the order it first appears.
func (t *Tree) Sources() []string {
	seen := map[string]bool{}
	var out []string
	for _, r := range t.Rows {
		if !seen[r.Source] {
			seen[r.Source] = true
			out = append(out, r.Source)
		}
	}
	return out
}

// HasSource reports whether any row came from the named layer.
func (t *Tree) HasSource(name string) bool {
	for _, r := range t.Rows {
		if r.Source == name {
			return true
		}
	}
	return false
}

// Duplicate is an id inserted by more than one row. dsh prints both rows
// without a warning and, when booting, keeps only the last one.
type Duplicate struct {
	ID      string   `json:"id"`
	Sources []string `json:"sources"`
}

// Winner is the source whose row dsh actually loads.
func (d Duplicate) Winner() string { return d.Sources[len(d.Sources)-1] }

// Losers are the sources whose rows are silently dropped.
func (d Duplicate) Losers() []string { return d.Sources[:len(d.Sources)-1] }

// Duplicates lists every id that appears on more than one row, in dump order.
func (t *Tree) Duplicates() []Duplicate {
	idx := map[string]int{}
	var out []Duplicate
	for _, r := range t.Rows {
		if r.ID == "" {
			continue
		}
		if i, ok := idx[r.ID]; ok {
			out[i].Sources = append(out[i].Sources, r.Source)
			continue
		}
		idx[r.ID] = len(out)
		out = append(out, Duplicate{ID: r.ID, Sources: []string{r.Source}})
	}
	dups := out[:0]
	for _, d := range out {
		if len(d.Sources) > 1 {
			dups = append(dups, d)
		}
	}
	return dups
}

// IDs returns the ids of rows from one source.
func (t *Tree) IDs(source string) []string {
	var ids []string
	for _, r := range t.Rows {
		if r.Source == source && r.ID != "" {
			ids = append(ids, r.ID)
		}
	}
	return ids
}

// Unmatched is a patch row whose target id does not exist. dsh still exits 0.
type Unmatched struct {
	File string `json:"file"`
	ID   string `json:"id"`
}

// Inactive is a plugin that did not activate during boot. dsh keeps running
// when this happens, so it is the main signal a smoke test has to read.
type Inactive struct {
	ID     string `json:"id"`
	Module string `json:"module"`
	Reason string `json:"reason"`
}

// Pending reports whether the plugin is waiting for a service rather than
// having thrown.
func (i Inactive) Pending() bool { return strings.HasPrefix(i.Reason, "pending") }

// Skipped is a bundle dsh left out of the composition, typically because it
// is incompatible with the running release. dsh still exits 0.
type Skipped struct {
	Package string `json:"package"`
	Reason  string `json:"reason"`
}

// Short trims dsh's explanation to its first sentence.
func (s Skipped) Short() string {
	r := strings.TrimPrefix(s.Reason, "Error: ")
	if i := strings.Index(r, ". "); i > 0 {
		r = r[:i]
	}
	return r
}

// Warnings are the parts of dsh's stderr DSDock understands.
type Warnings struct {
	Unmatched []Unmatched `json:"unmatched,omitempty"`
	Inactive  []Inactive  `json:"inactive,omitempty"`
	Skipped   []Skipped   `json:"skipped,omitempty"`
}

// SkippedPackage reports why a bundle was skipped, if it was.
func (w Warnings) SkippedPackage(pkg string) (string, bool) {
	for _, s := range w.Skipped {
		if s.Package == pkg {
			return s.Short(), true
		}
	}
	return "", false
}

var (
	reUnmatched     = regexp.MustCompile(`^dsh: \[(.+)\] patch: entry "(.*)" not found$`)
	reInactiveHead  = regexp.MustCompile(`^dsh: warning: \d+ entr(?:y|ies) did not activate$`)
	reInactiveEntry = regexp.MustCompile(`^(\S+) \((.+?)\): (.*)$`)
	reSkipped       = regexp.MustCompile(`^dsh: skipping profile bundle "(.+?)": (.*)$`)
)

// ParseWarnings reads dsh's stderr. Lines it does not recognise are ignored.
func ParseWarnings(stderr string) Warnings {
	var w Warnings
	inInactive := false
	for _, line := range strings.Split(stderr, "\n") {
		line = strings.TrimRight(line, "\r")
		if m := reSkipped.FindStringSubmatch(line); m != nil {
			w.Skipped = append(w.Skipped, Skipped{Package: m[1], Reason: m[2]})
			inInactive = false
			continue
		}
		if m := reUnmatched.FindStringSubmatch(line); m != nil {
			w.Unmatched = append(w.Unmatched, Unmatched{File: m[1], ID: m[2]})
			inInactive = false
			continue
		}
		if reInactiveHead.MatchString(line) {
			inInactive = true
			continue
		}
		if !inInactive {
			continue
		}
		if line == "" || line[0] == ' ' || line[0] == '\t' {
			continue // stack frames under an entry
		}
		if m := reInactiveEntry.FindStringSubmatch(line); m != nil {
			w.Inactive = append(w.Inactive, Inactive{ID: m[1], Module: m[2], Reason: m[3]})
			continue
		}
		inInactive = false
	}
	return w
}
