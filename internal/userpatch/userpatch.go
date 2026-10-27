// Package userpatch edits a profile's own cordis.patch.yml.
//
// The file is a top-level YAML sequence of patch entries and starts out as
// comments plus `[]`. Appending list items after that `[]` produces invalid
// YAML, which a running dsh silently ignores and the next boot refuses, so
// every edit here goes through the YAML tree and keeps the user's comments.
package userpatch

import (
	"bytes"
	"errors"
	"fmt"
	"io"

	"gopkg.in/yaml.v3"
)

// SetDisabled turns one entry on or off, with the same rules as dsh's own
// plugin manager:
//
//   - only `disabled` in the last override of that id changes;
//   - with no such override, one is appended;
//   - an override left holding only `id` and `disabled` is dropped when the
//     layers below already give the entry the requested state.
//
// base is the entry's disabled state without the user layer. It reports
// whether the file changed.
func SetDisabled(src []byte, id string, disabled, base bool) ([]byte, bool, error) {
	if id == "" {
		return nil, false, errors.New("empty id")
	}
	doc, seq, err := parse(src)
	if err != nil {
		return nil, false, err
	}

	idx := -1
	for i, item := range seq.Content {
		if isOverrideOf(item, id) {
			idx = i
		}
	}

	if idx < 0 {
		if disabled == base {
			return src, false, nil
		}
		seq.Style = 0 // a flow `[]` cannot take block items
		seq.Content = append(seq.Content, &yaml.Node{Kind: yaml.MappingNode, Content: []*yaml.Node{
			str("id"), str(id), str("disabled"), boolNode(disabled),
		}})
		return encode(doc)
	}

	item := seq.Content[idx]
	if v := value(item, "disabled"); v != nil && v.Tag == "!!bool" && (v.Value == "true") == disabled {
		return src, false, nil
	}
	setValue(item, "disabled", boolNode(disabled))
	if disabled == base && onlyKeys(item, "id", "disabled") {
		remove(seq, idx)
	}
	return encode(doc)
}

// remove drops one entry. Comments written above it (including the file's
// header, which YAML attaches to the first entry) move to whatever now takes
// its place, so deleting an override never deletes the user's notes.
func remove(seq *yaml.Node, idx int) {
	comment := seq.Content[idx].HeadComment
	seq.Content = append(seq.Content[:idx], seq.Content[idx+1:]...)
	if len(seq.Content) == 0 {
		seq.Style = yaml.FlowStyle
		seq.HeadComment = joinComments(seq.HeadComment, comment)
		return
	}
	if idx < len(seq.Content) {
		next := seq.Content[idx]
		next.HeadComment = joinComments(comment, next.HeadComment)
		return
	}
	prev := seq.Content[idx-1]
	prev.FootComment = joinComments(prev.FootComment, comment)
}

func joinComments(a, b string) string {
	switch {
	case a == "":
		return b
	case b == "":
		return a
	}
	return a + "\n" + b
}

// Validate reports whether src is a patch file dsh can load: a YAML sequence,
// possibly empty.
func Validate(src []byte) error {
	_, _, err := parse(src)
	return err
}

func parse(src []byte) (*yaml.Node, *yaml.Node, error) {
	// yaml.Unmarshal stops after the first value and drops the rest without an
	// error, so `[]` followed by list items would read as an empty list and a
	// rewrite would erase the user's entries. dsh (js-yaml) rejects such a
	// file; decode twice to do the same.
	dec := yaml.NewDecoder(bytes.NewReader(src))
	var doc yaml.Node
	if err := dec.Decode(&doc); err != nil && !errors.Is(err, io.EOF) {
		return nil, nil, fmt.Errorf("cordis.patch.yml: %w", err)
	}
	var extra yaml.Node
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("more than one YAML document")
		}
		return nil, nil, fmt.Errorf("cordis.patch.yml: %w", err)
	}
	if doc.Kind == 0 { // empty file
		doc = yaml.Node{Kind: yaml.DocumentNode}
	}
	if len(doc.Content) == 0 {
		doc.Content = []*yaml.Node{{Kind: yaml.SequenceNode, Tag: "!!seq", Style: yaml.FlowStyle}}
	}
	seq := doc.Content[0]
	if seq.Kind == yaml.ScalarNode && seq.Tag == "!!null" {
		*seq = yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Style: yaml.FlowStyle, HeadComment: seq.HeadComment}
	}
	if seq.Kind != yaml.SequenceNode {
		return nil, nil, errors.New("cordis.patch.yml: top level must be a list of patch entries")
	}
	return &doc, seq, nil
}

func encode(doc *yaml.Node) ([]byte, bool, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(doc); err != nil {
		return nil, false, err
	}
	if err := enc.Close(); err != nil {
		return nil, false, err
	}
	return buf.Bytes(), true, nil
}

// isOverrideOf matches a patch entry that targets id. Insert entries add rows
// and never count as overrides.
func isOverrideOf(item *yaml.Node, id string) bool {
	if item.Kind != yaml.MappingNode || value(item, "insert") != nil {
		return false
	}
	v := value(item, "id")
	return v != nil && v.Value == id
}

func value(m *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

func setValue(m *yaml.Node, key string, v *yaml.Node) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			v.LineComment = m.Content[i+1].LineComment
			m.Content[i+1] = v
			return
		}
	}
	m.Content = append(m.Content, str(key), v)
}

func onlyKeys(m *yaml.Node, keys ...string) bool {
	allowed := map[string]bool{}
	for _, k := range keys {
		allowed[k] = true
	}
	for i := 0; i < len(m.Content); i += 2 {
		if !allowed[m.Content[i].Value] {
			return false
		}
	}
	return true
}

func str(s string) *yaml.Node { return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: s} }

func boolNode(b bool) *yaml.Node {
	v := "false"
	if b {
		v = "true"
	}
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: v}
}
