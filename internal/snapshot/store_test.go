package snapshot

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/hikariming/dsdock/internal/profile"
)

func setup(t *testing.T) (*Store, string) {
	t.Helper()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	home := t.TempDir()
	dir := profile.Dir(home, "web")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, dir, "package.json", `{"dependencies":{"a":"1.0.0"}}`)
	write(t, dir, "pnpm-lock.yaml", "lockfileVersion: '9.0'\n")
	write(t, dir, "cordis.patch.yml", "[]\n")
	return s, home
}

func write(t *testing.T, dir, rel, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, rel), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, dir, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, rel))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestTakeAndWriteBack(t *testing.T) {
	s, home := setup(t)
	dir := profile.Dir(home, "web")
	sn, err := s.Take(home, "web", "before add x", "0.2.1-rc.4", false)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"package.json", "pnpm-lock.yaml", "cordis.patch.yml"}; !reflect.DeepEqual(sn.Files, want) {
		t.Errorf("files = %v", sn.Files)
	}

	// Change the profile the way an install would, and add a file that did
	// not exist before.
	write(t, dir, "package.json", `{"dependencies":{"a":"1.0.0","b":"2.0.0"}}`)
	write(t, dir, "pnpm-workspace.yaml", "allowBuilds: {}\n")
	write(t, dir, "cordis.yml", "[]\n") // not managed: must survive

	if err := s.WriteBack(sn.ID); err != nil {
		t.Fatal(err)
	}
	if got := read(t, dir, "package.json"); got != `{"dependencies":{"a":"1.0.0"}}` {
		t.Errorf("package.json = %s", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "pnpm-workspace.yaml")); !os.IsNotExist(err) {
		t.Errorf("pnpm-workspace.yaml should be removed, stat err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "cordis.yml")); err != nil {
		t.Errorf("unmanaged file touched: %v", err)
	}
	if matches, _ := filepath.Glob(filepath.Join(dir, "*.dsdock-tmp")); len(matches) != 0 {
		t.Errorf("temp files left: %v", matches)
	}
}

func TestBlobsAreShared(t *testing.T) {
	s, home := setup(t)
	if _, err := s.Take(home, "web", "one", "", false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Take(home, "web", "two", "", false); err != nil {
		t.Fatal(err)
	}
	var n int
	filepath.WalkDir(filepath.Join(s.dir, "blobs"), func(_ string, d os.DirEntry, _ error) error {
		if !d.IsDir() {
			n++
		}
		return nil
	})
	if n != 3 {
		t.Errorf("blob files = %d, want 3", n)
	}
}

func TestListAndLastVerified(t *testing.T) {
	s, home := setup(t)
	a, _ := s.Take(home, "web", "a", "", false)
	b, _ := s.Take(home, "web", "b", "", true)
	if err := s.MarkVerified(a.ID, "h1"); err != nil {
		t.Fatal(err)
	}
	list, err := s.List(home, "web", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].ID != b.ID || !list[0].Manual {
		t.Fatalf("list = %+v", list)
	}
	lv, err := s.LastVerified(home, "web")
	if err != nil || lv == nil || lv.ID != a.ID || lv.DumpHash != "h1" {
		t.Fatalf("last verified = %+v, %v", lv, err)
	}
	if other, _ := s.List(home, "other", 0); len(other) != 0 {
		t.Errorf("other profile sees %d snapshots", len(other))
	}
}

func TestReadFile(t *testing.T) {
	s, home := setup(t)
	sn, _ := s.Take(home, "web", "a", "", false)
	b, err := s.ReadFile(sn.ID, "cordis.patch.yml")
	if err != nil || string(b) != "[]\n" {
		t.Fatalf("ReadFile = %q, %v", b, err)
	}
	if _, err := s.ReadFile(sn.ID, "pnpm-workspace.yaml"); !os.IsNotExist(err) && err == nil {
		t.Error("missing file read without error")
	}
}

func TestOps(t *testing.T) {
	s, home := setup(t)
	op, err := s.BeginOp("add", home, "web", "fx-ok")
	if err != nil {
		t.Fatal(err)
	}
	op.Status, op.Log = OpApplied, "ok"
	if err := s.EndOp(op); err != nil {
		t.Fatal(err)
	}
	ops, err := s.Ops(home, "web", 10)
	if err != nil || len(ops) != 1 || ops[0].Status != OpApplied || ops[0].EndedAt.IsZero() {
		t.Fatalf("ops = %+v, %v", ops, err)
	}
}
