// Package core is what DSDock does, independent of how it is asked: the CLI
// and the GUI are thin shells over it.
//
// Changes follow plan → confirm → apply. A Plan carries the preflight report
// and every reason the user should confirm; the shell decides how to ask
// (a terminal prompt, a dialog) and then calls Apply, which goes through
// internal/txn like every change must.
package core

import (
	"errors"
	"fmt"
	"path/filepath"

	"github.com/hikariming/dsdock/internal/appdir"
	"github.com/hikariming/dsdock/internal/dshenv"
	"github.com/hikariming/dsdock/internal/npmreg"
	"github.com/hikariming/dsdock/internal/profile"
	"github.com/hikariming/dsdock/internal/snapshot"
	"github.com/hikariming/dsdock/internal/txn"
	"github.com/hikariming/dsdock/internal/versions"
)

// Core holds DSDock's state for one DSH_HOME.
type Core struct {
	Home    string // DSH_HOME
	DataDir string // DSDOCK_HOME
	Store   *snapshot.Store
	Vers    *versions.Manager
	Reg     *npmreg.Client
	// Progress receives short status lines while work runs; it may be nil.
	Progress func(string)
}

// Open loads DSDock's data. registry may be empty for the public registry.
func Open(registry string) (*Core, error) {
	dir, err := appdir.Dir()
	if err != nil {
		return nil, err
	}
	home, err := dshenv.Home()
	if err != nil {
		return nil, err
	}
	store, err := snapshot.Open(dir)
	if err != nil {
		return nil, err
	}
	reg := npmreg.New(registry)
	vers, err := versions.Open(store.DB(), filepath.Join(dir, "versions"), reg)
	if err != nil {
		store.Close()
		return nil, err
	}
	c := &Core{Home: home, DataDir: dir, Store: store, Vers: vers, Reg: reg}
	vers.Progress = func(msg string) { c.say(msg) }
	return c, nil
}

// Close releases the database.
func (c *Core) Close() error { return c.Store.Close() }

func (c *Core) say(msg string) {
	if c.Progress != nil {
		c.Progress(msg)
	}
}

// ShimDir is where `dsdock dsh shim` writes the dsh shim.
func (c *Core) ShimDir() string { return filepath.Join(c.DataDir, "bin") }

// Where a dsh came from, besides the versions.By* reasons.
const (
	ByEnvEntry = "DSDOCK_DSH"
	ByPath     = "PATH"
)

// EnvFor picks the dsh that works on a profile (which may be empty):
// DSDOCK_DSH when set, else the release DSDock resolves (pin, default,
// adopted), else the dsh on PATH that is not DSDock's own shim.
func (c *Core) EnvFor(profileName string) (*dshenv.Env, string, error) {
	if envSet("DSDOCK_DSH") {
		env, err := dshenv.Discover(c.ShimDir())
		return env, ByEnvEntry, err
	}
	inst, why, err := c.Vers.Resolve(c.Home, profileName)
	switch {
	case err == nil:
		env, err := inst.Env(c.Home)
		return env, why, err
	case errors.Is(err, versions.ErrNoVersion):
		env, err := dshenv.Discover(c.ShimDir())
		return env, ByPath, err
	default:
		return nil, "", err
	}
}

// Engine runs changes with env.
func (c *Core) Engine(env *dshenv.Env) *txn.Engine {
	return &txn.Engine{Env: env, Store: c.Store, LockDir: filepath.Join(c.DataDir, "locks"), Progress: c.Progress}
}

// EngineFor resolves a profile's dsh and returns an engine for it.
func (c *Core) EngineFor(profileName string) (*txn.Engine, error) {
	env, _, err := c.EnvFor(profileName)
	if err != nil {
		return nil, err
	}
	return c.Engine(env), nil
}

// ErrNoProfile wraps profile.ErrNotExist with how to create one.
type ErrNoProfile struct{ Name, Dir string }

func (e *ErrNoProfile) Error() string {
	return fmt.Sprintf("profile %s 不存在（%s）。\n  新建 profile：dsh plugin --profile %s add <包>\n  （只有 web 等官方模板名会自带 Web 界面；其它名字想要 Web，用 dsh --profile %s --from-default-profile web）", e.Name, e.Dir, e.Name, e.Name)
}

func (e *ErrNoProfile) Unwrap() error { return profile.ErrNotExist }

// LoadProfile reads a profile in this home.
func (c *Core) LoadProfile(name string) (*profile.Profile, error) {
	if err := profile.ValidName(name); err != nil {
		return nil, err
	}
	p, err := profile.Load(c.Home, name)
	if errors.Is(err, profile.ErrNotExist) {
		return nil, &ErrNoProfile{Name: name, Dir: profile.Dir(c.Home, name)}
	}
	return p, err
}
