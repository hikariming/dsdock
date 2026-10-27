package layer

import (
	"os"
	"reflect"
	"testing"
)

func read(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestParseRealDump(t *testing.T) {
	tree, err := Parse(read(t, "dump-v4c.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if got := len(tree.Rows); got != 99 {
		t.Errorf("rows = %d, want 99", got)
	}
	want := []string{"@deepseek-ai/dsh-base", "fx-ok", "fx-conflict-a", "fx-conflict-b"}
	if got := tree.Sources(); !reflect.DeepEqual(got, want) {
		t.Errorf("sources = %v, want %v", got, want)
	}
	first := tree.Rows[0]
	if first.ID != "tool-plugin-manager" || first.Disabled != Disabled {
		t.Errorf("first row = %+v", first)
	}
	if r := tree.Rows[1]; r.ID != "plugin-manager" || r.Disabled != DisabledExpr {
		t.Errorf("!!js disabled not recognised: %+v", r)
	}
}

func TestDuplicates(t *testing.T) {
	tree, err := Parse(read(t, "dump-v4c.txt"))
	if err != nil {
		t.Fatal(err)
	}
	dups := tree.Duplicates()
	if len(dups) != 1 {
		t.Fatalf("duplicates = %+v, want one", dups)
	}
	d := dups[0]
	if d.ID != "fx-same" || d.Winner() != "fx-conflict-b" || !reflect.DeepEqual(d.Losers(), []string{"fx-conflict-a"}) {
		t.Errorf("duplicate = %+v", d)
	}
}

func TestPatchedHeader(t *testing.T) {
	tree, err := Parse(read(t, "dump-v4u.txt"))
	if err != nil {
		t.Fatal(err)
	}
	var row *Row
	for i := range tree.Rows {
		if tree.Rows[i].ID == "fx-ok" {
			row = &tree.Rows[i]
		}
	}
	if row == nil {
		t.Fatal("fx-ok row missing")
	}
	if row.Source != "fx-ok" || row.Disabled != Disabled {
		t.Errorf("row = %+v", row)
	}
	if want := []string{"/DSH_HOME/profiles/t1/cordis.patch.yml"}; !reflect.DeepEqual(row.PatchedBy, want) {
		t.Errorf("patchedBy = %v, want %v", row.PatchedBy, want)
	}
}

func TestParseEmpty(t *testing.T) {
	tree, err := Parse("")
	if err != nil || len(tree.Rows) != 0 {
		t.Fatalf("tree = %+v, err = %v", tree, err)
	}
}

func TestParseWarningsUnmatched(t *testing.T) {
	w := ParseWarnings(read(t, "stderr-notfound.txt"))
	want := []Unmatched{{File: "/DSH_HOME/profiles/t1/cordis.patch.yml", ID: "no-such-row"}}
	if !reflect.DeepEqual(w.Unmatched, want) {
		t.Errorf("unmatched = %+v", w.Unmatched)
	}
}

func TestParseWarningsInactiveError(t *testing.T) {
	w := ParseWarnings(read(t, "stderr-inactive-error.txt"))
	if len(w.Inactive) != 1 {
		t.Fatalf("inactive = %+v", w.Inactive)
	}
	got := w.Inactive[0]
	if got.ID != "fx-broken" || got.Module != "fx-broken" || got.Reason != "Error: fx-broken: boom on apply" || got.Pending() {
		t.Errorf("inactive = %+v", got)
	}
}

func TestParseWarningsInactivePending(t *testing.T) {
	w := ParseWarnings(read(t, "stderr-inactive-pending.txt"))
	if len(w.Inactive) != 1 || !w.Inactive[0].Pending() || w.Inactive[0].Module != "@nagi-ovo/dsh-ads" {
		t.Fatalf("inactive = %+v", w.Inactive)
	}
}

func TestParseWarningsStopsAfterBlock(t *testing.T) {
	in := "dsh: warning: 1 entry did not activate\n" +
		"a (pkg-a): Error: x\n" +
		"    at frame\n" +
		"some unrelated log line that is not an entry\n" +
		"b (pkg-b): looks like an entry but is outside the block\n"
	w := ParseWarnings(in)
	if len(w.Inactive) != 1 || w.Inactive[0].ID != "a" {
		t.Fatalf("inactive = %+v", w.Inactive)
	}
}

func TestParseCRLF(t *testing.T) {
	tree, err := Parse("# == a\r\n- id: x\r\n  name: a\r\n# == b\r\n- id: y\r\n")
	if err != nil {
		t.Fatal(err)
	}
	if got := tree.Sources(); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Errorf("sources = %q", got)
	}
}
