// Package snapshot keeps copies of the few files that define a profile, so any
// change DSDock makes can be undone.
//
// A snapshot is the profile's ManagedFiles (about 32 KB in practice), stored
// content-addressed under blobs/. node_modules is never copied: restoring the
// lockfile and running a frozen, offline install rebuilds it exactly.
package snapshot

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/hikariming/dsdock/internal/profile"

	_ "modernc.org/sqlite"
)

// Snapshot is one saved state of a profile.
type Snapshot struct {
	ID         string    `json:"id"`
	Home       string    `json:"home"`
	Profile    string    `json:"profile"`
	Reason     string    `json:"reason"`
	CreatedAt  time.Time `json:"createdAt"`
	DSHVersion string    `json:"dshVersion,omitempty"`
	Manual     bool      `json:"manual"`
	// Verified means the profile composed cleanly when this snapshot was taken
	// or after it was restored; DumpHash is that composition's sha256.
	Verified bool     `json:"verified"`
	DumpHash string   `json:"dumpHash,omitempty"`
	Files    []string `json:"files"`
}

// Store is DSDock's snapshot database.
type Store struct {
	dir string
	db  *sql.DB
}

const schema = `
CREATE TABLE IF NOT EXISTS snapshots (
  id TEXT PRIMARY KEY,
  dsh_home TEXT NOT NULL,
  profile TEXT NOT NULL,
  reason TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  dsh_version TEXT NOT NULL DEFAULT '',
  manual INTEGER NOT NULL DEFAULT 0,
  verified INTEGER NOT NULL DEFAULT 0,
  dump_hash TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS snapshots_by_profile ON snapshots (dsh_home, profile, created_at);
CREATE TABLE IF NOT EXISTS snapshot_files (
  snapshot_id TEXT NOT NULL REFERENCES snapshots(id) ON DELETE CASCADE,
  relpath TEXT NOT NULL,
  blob TEXT NOT NULL,
  mode INTEGER NOT NULL,
  PRIMARY KEY (snapshot_id, relpath)
);
CREATE TABLE IF NOT EXISTS ops (
  id TEXT PRIMARY KEY,
  kind TEXT NOT NULL,
  dsh_home TEXT NOT NULL,
  profile TEXT NOT NULL,
  spec TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL,
  snapshot_id TEXT NOT NULL DEFAULT '',
  log TEXT NOT NULL DEFAULT '',
  started_at INTEGER NOT NULL,
  ended_at INTEGER NOT NULL DEFAULT 0
);
`

// Open opens (and if needed creates) the store in dir.
func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(filepath.Join(dir, "blobs"), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", "file:"+filepath.Join(dir, "dsdock.db")+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("init database: %w", err)
	}
	return &Store{dir: dir, db: db}, nil
}

// DB exposes the database to other DSDock subsystems that keep their own
// tables in it (versions, pins, settings).
func (s *Store) DB() *sql.DB { return s.db }

// Close releases the database.
func (s *Store) Close() error { return s.db.Close() }

// NewID returns a sortable, unique id with the given prefix.
func NewID(prefix string) string {
	var b [3]byte
	_, _ = rand.Read(b[:])
	return prefix + time.Now().UTC().Format("20060102-150405") + "-" + hex.EncodeToString(b[:])
}

// Take copies the profile's managed files into the store.
func (s *Store) Take(home, name, reason, dshVersion string, manual bool) (*Snapshot, error) {
	dir := profile.Dir(home, name)
	snap := &Snapshot{
		ID: NewID("s"), Home: home, Profile: name, Reason: reason,
		CreatedAt: time.Now().UTC(), DSHVersion: dshVersion, Manual: manual,
	}
	type file struct {
		rel, blob string
		mode      fs.FileMode
	}
	var files []file
	for _, rel := range profile.ManagedFiles {
		p := filepath.Join(dir, rel)
		info, err := os.Stat(p)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		blob, err := s.putBlob(b)
		if err != nil {
			return nil, err
		}
		files = append(files, file{rel, blob, info.Mode().Perm()})
		snap.Files = append(snap.Files, rel)
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("profile %s has none of the files a snapshot needs", name)
	}

	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT INTO snapshots (id, dsh_home, profile, reason, created_at, dsh_version, manual) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		snap.ID, home, name, reason, snap.CreatedAt.UnixMilli(), dshVersion, boolInt(manual)); err != nil {
		return nil, err
	}
	for _, f := range files {
		if _, err := tx.Exec(`INSERT INTO snapshot_files (snapshot_id, relpath, blob, mode) VALUES (?, ?, ?, ?)`,
			snap.ID, f.rel, f.blob, int(f.mode)); err != nil {
			return nil, err
		}
	}
	return snap, tx.Commit()
}

// MarkVerified records that the profile composed cleanly in this state.
func (s *Store) MarkVerified(id, dumpHash string) error {
	_, err := s.db.Exec(`UPDATE snapshots SET verified = 1, dump_hash = ? WHERE id = ?`, dumpHash, id)
	return err
}

// Get loads one snapshot.
func (s *Store) Get(id string) (*Snapshot, error) {
	snaps, err := s.query(`WHERE id = ?`, id)
	if err != nil {
		return nil, err
	}
	if len(snaps) == 0 {
		return nil, fmt.Errorf("snapshot %s not found", id)
	}
	return snaps[0], nil
}

// List returns a profile's snapshots, newest first.
func (s *Store) List(home, name string, limit int) ([]*Snapshot, error) {
	if limit <= 0 {
		limit = 1 << 30
	}
	return s.query(`WHERE dsh_home = ? AND profile = ? ORDER BY created_at DESC, rowid DESC LIMIT ?`, home, name, limit)
}

// LastVerified is the newest snapshot known to compose cleanly.
func (s *Store) LastVerified(home, name string) (*Snapshot, error) {
	snaps, err := s.query(`WHERE dsh_home = ? AND profile = ? AND verified = 1 ORDER BY created_at DESC, rowid DESC LIMIT 1`, home, name)
	if err != nil || len(snaps) == 0 {
		return nil, err
	}
	return snaps[0], nil
}

func (s *Store) query(where string, args ...any) ([]*Snapshot, error) {
	rows, err := s.db.Query(`SELECT id, dsh_home, profile, reason, created_at, dsh_version, manual, verified, dump_hash FROM snapshots `+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Snapshot
	for rows.Next() {
		var sn Snapshot
		var created int64
		var manual, verified int
		if err := rows.Scan(&sn.ID, &sn.Home, &sn.Profile, &sn.Reason, &created, &sn.DSHVersion, &manual, &verified, &sn.DumpHash); err != nil {
			return nil, err
		}
		sn.CreatedAt = time.UnixMilli(created).UTC()
		sn.Manual, sn.Verified = manual != 0, verified != 0
		out = append(out, &sn)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, sn := range out {
		files, err := s.files(sn.ID)
		if err != nil {
			return nil, err
		}
		for _, f := range files {
			sn.Files = append(sn.Files, f.rel)
		}
	}
	return out, nil
}

type storedFile struct {
	rel, blob string
	mode      fs.FileMode
}

func (s *Store) files(id string) ([]storedFile, error) {
	rows, err := s.db.Query(`SELECT relpath, blob, mode FROM snapshot_files WHERE snapshot_id = ? ORDER BY relpath`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []storedFile
	for rows.Next() {
		var f storedFile
		var mode int
		if err := rows.Scan(&f.rel, &f.blob, &mode); err != nil {
			return nil, err
		}
		f.mode = fs.FileMode(mode)
		out = append(out, f)
	}
	return out, rows.Err()
}

// ReadFile returns one file as it was in the snapshot.
func (s *Store) ReadFile(id, rel string) ([]byte, error) {
	files, err := s.files(id)
	if err != nil {
		return nil, err
	}
	for _, f := range files {
		if f.rel == rel {
			return os.ReadFile(s.blobPath(f.blob))
		}
	}
	return nil, fmt.Errorf("snapshot %s has no %s: %w", id, rel, os.ErrNotExist)
}

// WriteBack puts the snapshot's files back into the profile directory. Managed
// files that did not exist when the snapshot was taken are removed. It does
// not touch node_modules; the caller reinstalls from the restored lockfile.
func (s *Store) WriteBack(id string) error {
	sn, err := s.Get(id)
	if err != nil {
		return err
	}
	files, err := s.files(id)
	if err != nil {
		return err
	}
	dir := profile.Dir(sn.Home, sn.Profile)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	kept := map[string]bool{}
	for _, f := range files {
		b, err := os.ReadFile(s.blobPath(f.blob))
		if err != nil {
			return fmt.Errorf("blob for %s: %w", f.rel, err)
		}
		if err := writeAtomic(filepath.Join(dir, f.rel), b, f.mode); err != nil {
			return err
		}
		kept[f.rel] = true
	}
	for _, rel := range profile.ManagedFiles {
		if kept[rel] {
			continue
		}
		if err := os.Remove(filepath.Join(dir, rel)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func (s *Store) blobPath(sum string) string {
	return filepath.Join(s.dir, "blobs", sum[:2], sum)
}

func (s *Store) putBlob(b []byte) (string, error) {
	h := sha256.Sum256(b)
	sum := hex.EncodeToString(h[:])
	p := s.blobPath(sum)
	if _, err := os.Stat(p); err == nil {
		return sum, nil
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return "", err
	}
	return sum, writeAtomic(p, b, 0o644)
}

func writeAtomic(path string, b []byte, mode fs.FileMode) error {
	tmp := path + ".dsdock-tmp"
	if err := os.WriteFile(tmp, b, mode); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// Hash is the sha256 DSDock uses to compare compositions.
func Hash(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
