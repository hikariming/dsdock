// Package versions installs DSH releases side by side and decides which one
// runs for a given profile.
//
// Each release lives in <DSDOCK_HOME>/versions/<version>/ and is installed
// with pnpm, so releases share files through pnpm's store: two releases took
// 548 MB together in M0, against 1.7 GB with one npm install each.
package versions

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Masterminds/semver/v3"

	"github.com/hikariming/dsdock/internal/dshenv"
	"github.com/hikariming/dsdock/internal/npmreg"
)

// Package is the npm package dsh ships as.
const Package = "@deepseek-ai/dsh"

// External names the one dsh installation DSDock did not install itself
// (typically `npm i -g`), once adopted.
const External = "external"

// Sources of an installation.
const (
	Managed    = "managed"
	SourceExt  = "external"
	installing = ".dsdock-installing"
)

// Installed is one dsh release DSDock can run.
type Installed struct {
	Name          string    `json:"name"` // the version, or "external"
	Version       string    `json:"version"`
	Dir           string    `json:"dir"`
	Entry         string    `json:"entry"` // lib/bin.js
	Source        string    `json:"source"`
	NodeRange     string    `json:"nodeRange,omitempty"`
	SessionFormat int       `json:"sessionFormat,omitempty"`
	InstalledAt   time.Time `json:"installedAt"`
}

// Env is the dshenv for running this release against home.
func (i *Installed) Env(home string) (*dshenv.Env, error) {
	return dshenv.ForEntry(home, i.Entry, i.NodeRange)
}

// Manager keeps the installed releases, the global default and per-profile
// pins.
type Manager struct {
	Root     string
	Registry *npmreg.Client
	Progress func(string)

	db *sql.DB
}

const schema = `
CREATE TABLE IF NOT EXISTS versions (
  name TEXT PRIMARY KEY,
  version TEXT NOT NULL,
  dir TEXT NOT NULL,
  entry TEXT NOT NULL,
  source TEXT NOT NULL,
  node_range TEXT NOT NULL DEFAULT '',
  session_format INTEGER NOT NULL DEFAULT 0,
  installed_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS pins (
  dsh_home TEXT NOT NULL,
  profile TEXT NOT NULL,
  name TEXT NOT NULL,
  PRIMARY KEY (dsh_home, profile)
);
CREATE TABLE IF NOT EXISTS settings (
  key TEXT PRIMARY KEY,
  value TEXT NOT NULL
);
`

// Open prepares the manager's tables in db.
func Open(db *sql.DB, root string, reg *npmreg.Client) (*Manager, error) {
	if _, err := db.Exec(schema); err != nil {
		return nil, fmt.Errorf("init versions tables: %w", err)
	}
	return &Manager{Root: root, Registry: reg, db: db}, nil
}

func (m *Manager) say(format string, args ...any) {
	if m.Progress != nil {
		m.Progress(fmt.Sprintf(format, args...))
	}
}

// ErrNoVersion means no release is installed, pinned or set as default.
var ErrNoVersion = errors.New("no DSH version is managed by DSDock")

// ErrNotInstalled means a named release is not installed.
var ErrNotInstalled = errors.New("not installed")

// ErrFilesMissing means a release is recorded but its files are gone. Get
// still returns the record so it can be uninstalled.
var ErrFilesMissing = errors.New("files missing")

// List returns every installed release, newest first, external last.
func (m *Manager) List() ([]*Installed, error) {
	rows, err := m.db.Query(`SELECT name, version, dir, entry, source, node_range, session_format, installed_at FROM versions`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Installed
	for rows.Next() {
		var i Installed
		var at int64
		if err := rows.Scan(&i.Name, &i.Version, &i.Dir, &i.Entry, &i.Source, &i.NodeRange, &i.SessionFormat, &at); err != nil {
			return nil, err
		}
		i.InstalledAt = time.UnixMilli(at).UTC()
		out = append(out, &i)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.SliceStable(out, func(a, b int) bool {
		if (out[a].Name == External) != (out[b].Name == External) {
			return out[b].Name == External
		}
		va, ea := semver.NewVersion(out[a].Version)
		vb, eb := semver.NewVersion(out[b].Version)
		if ea != nil || eb != nil {
			return out[a].Version > out[b].Version
		}
		return va.GreaterThan(vb)
	})
	return out, nil
}

// Get loads one release by name.
func (m *Manager) Get(name string) (*Installed, error) {
	var i Installed
	var at int64
	err := m.db.QueryRow(`SELECT name, version, dir, entry, source, node_range, session_format, installed_at FROM versions WHERE name = ?`, name).
		Scan(&i.Name, &i.Version, &i.Dir, &i.Entry, &i.Source, &i.NodeRange, &i.SessionFormat, &at)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("DSH %s: %w", name, ErrNotInstalled)
	}
	if err != nil {
		return nil, err
	}
	i.InstalledAt = time.UnixMilli(at).UTC()
	if _, err := os.Stat(i.Entry); err != nil {
		return &i, fmt.Errorf("DSH %s 的文件不见了（%s），请重新安装或卸载它: %w", name, i.Entry, ErrFilesMissing)
	}
	return &i, nil
}

func (m *Manager) put(i *Installed) error {
	_, err := m.db.Exec(`INSERT INTO versions (name, version, dir, entry, source, node_range, session_format, installed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(name) DO UPDATE SET version=excluded.version, dir=excluded.dir, entry=excluded.entry,
		  source=excluded.source, node_range=excluded.node_range, session_format=excluded.session_format, installed_at=excluded.installed_at`,
		i.Name, i.Version, i.Dir, i.Entry, i.Source, i.NodeRange, i.SessionFormat, i.InstalledAt.UnixMilli())
	return err
}

// ---------- settings, default and pins ----------

func (m *Manager) setting(key string) (string, error) {
	var v string
	err := m.db.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v, err
}

func (m *Manager) setSetting(key, value string) error {
	if value == "" {
		_, err := m.db.Exec(`DELETE FROM settings WHERE key = ?`, key)
		return err
	}
	_, err := m.db.Exec(`INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}

// Default is the release used when a profile has no pin; empty if unset.
func (m *Manager) Default() (string, error) { return m.setting("default") }

// Previous is the default before the last switch, for undoing it.
func (m *Manager) Previous() (string, error) { return m.setting("previous") }

// SetDefault switches the global default and remembers the old one.
func (m *Manager) SetDefault(name string) error {
	if _, err := m.Get(name); err != nil {
		return err
	}
	cur, err := m.Default()
	if err != nil {
		return err
	}
	if cur == name {
		return nil
	}
	if err := m.setSetting("previous", cur); err != nil {
		return err
	}
	return m.setSetting("default", name)
}

// Pin makes a profile always run one release.
func (m *Manager) Pin(home, profile, name string) error {
	if _, err := m.Get(name); err != nil {
		return err
	}
	_, err := m.db.Exec(`INSERT INTO pins (dsh_home, profile, name) VALUES (?, ?, ?) ON CONFLICT(dsh_home, profile) DO UPDATE SET name = excluded.name`, home, profile, name)
	return err
}

// Unpin lets a profile follow the default again.
func (m *Manager) Unpin(home, profile string) error {
	_, err := m.db.Exec(`DELETE FROM pins WHERE dsh_home = ? AND profile = ?`, home, profile)
	return err
}

// PinOf is the release a profile is pinned to; empty if none.
func (m *Manager) PinOf(home, profile string) (string, error) {
	var name string
	err := m.db.QueryRow(`SELECT name FROM pins WHERE dsh_home = ? AND profile = ?`, home, profile).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return name, err
}

// Pin is one profile pinned to a release.
type Pin struct {
	Home    string `json:"home"`
	Profile string `json:"profile"`
	Name    string `json:"name"`
}

// Pins lists pins, optionally only those to one release.
func (m *Manager) Pins(name string) ([]Pin, error) {
	q, args := `SELECT dsh_home, profile, name FROM pins`, []any{}
	if name != "" {
		q, args = q+` WHERE name = ?`, append(args, name)
	}
	rows, err := m.db.Query(q+` ORDER BY dsh_home, profile`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Pin
	for rows.Next() {
		var p Pin
		if err := rows.Scan(&p.Home, &p.Profile, &p.Name); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ---------- resolution ----------

// Why a release was chosen.
const (
	ByEnv      = "DSDOCK_DSH_VERSION"
	ByPin      = "pin"
	ByDefault  = "default"
	ByExternal = "external"
)

// Resolve picks the release for a profile (which may be empty):
// DSDOCK_DSH_VERSION, then the profile's pin, then the default, then an
// adopted external installation.
func (m *Manager) Resolve(home, profile string) (*Installed, string, error) {
	if name := os.Getenv("DSDOCK_DSH_VERSION"); name != "" {
		i, err := m.Get(name)
		return i, ByEnv, err
	}
	if profile != "" {
		name, err := m.PinOf(home, profile)
		if err != nil {
			return nil, "", err
		}
		if name != "" {
			i, err := m.Get(name)
			return i, ByPin, err
		}
	}
	name, err := m.Default()
	if err != nil {
		return nil, "", err
	}
	if name != "" {
		i, err := m.Get(name)
		return i, ByDefault, err
	}
	if i, err := m.Get(External); err == nil {
		return i, ByExternal, nil
	}
	return nil, "", ErrNoVersion
}

// ---------- uninstall ----------

// Uninstall removes a release. It refuses while the release is the default
// or pinned, since that would leave profiles with nothing to run.
func (m *Manager) Uninstall(name string) error {
	i, err := m.Get(name)
	if err != nil && !errors.Is(err, ErrFilesMissing) {
		return err
	}
	if def, _ := m.Default(); def == name {
		return fmt.Errorf("DSH %s 是默认版本，先用 dsdock dsh use 切换到别的版本", name)
	}
	if pins, err := m.Pins(name); err != nil {
		return err
	} else if len(pins) > 0 {
		var names []string
		for _, p := range pins {
			names = append(names, p.Profile)
		}
		return fmt.Errorf("profile %s 锁定在 DSH %s 上，先 unpin", strings.Join(names, "、"), name)
	}
	if i != nil && i.Source == Managed {
		if err := os.RemoveAll(i.Dir); err != nil {
			return err
		}
	}
	_, err = m.db.Exec(`DELETE FROM versions WHERE name = ?`, name)
	if prev, _ := m.Previous(); prev == name {
		_ = m.setSetting("previous", "")
	}
	return err
}

// dirFor is where a managed release lives.
func (m *Manager) dirFor(version string) string { return filepath.Join(m.Root, version) }
