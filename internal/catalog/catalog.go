// Package catalog reads dshfind's plugin catalog: risk flags, archive state,
// last activity and dshfind's own score for each indexed repository.
//
// The catalog is a convenience. Every failure here degrades to "no data" so
// it never blocks an install.
package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// DefaultBase is dshfind's public API.
const DefaultBase = "https://api.dshfind.com"

// TTL is how long a fetched catalog is used before asking again.
const TTL = 6 * time.Hour

// Entry is one indexed repository.
type Entry struct {
	FullName string    `json:"full_name"`
	Risky    bool      `json:"is_risky"`
	RiskNote string    `json:"risk_note"`
	Archived bool      `json:"archived"`
	PushedAt time.Time `json:"pushed_at"`
	Score    *float64  `json:"score"`
	Grade    string    `json:"grade"`
	Install  struct {
		PkgName string `json:"pkg_name"`
	} `json:"install"`
}

// Index answers lookups by repository and by npm package name.
type Index struct {
	AsOf   string
	byRepo map[string]*Entry
	byPkg  map[string][]*Entry
}

// Lookup finds the entry for a package. repo ("owner/repo", from the
// package's repository field or the install spec) decides when known: several
// repositories can publish the same npm name, and dshfind flags impostors
// that reuse an official package name. Without a repo, a package name that
// maps to exactly one repository is used. Others are the remaining
// repositories using that npm name.
func (ix *Index) Lookup(pkg, repo string) (match *Entry, others []*Entry) {
	if ix == nil {
		return nil, nil
	}
	if repo != "" {
		match = ix.byRepo[strings.ToLower(repo)]
	}
	cands := ix.byPkg[pkg]
	if match == nil && repo == "" && len(cands) == 1 {
		match = cands[0]
	}
	for _, e := range cands {
		if e != match {
			others = append(others, e)
		}
	}
	return match, others
}

func build(entries []*Entry, asOf string) *Index {
	ix := &Index{AsOf: asOf, byRepo: map[string]*Entry{}, byPkg: map[string][]*Entry{}}
	for _, e := range entries {
		ix.byRepo[strings.ToLower(e.FullName)] = e
		if e.Install.PkgName != "" {
			ix.byPkg[e.Install.PkgName] = append(ix.byPkg[e.Install.PkgName], e)
		}
	}
	return ix
}

type document struct {
	Data []*Entry `json:"data"`
	AsOf string   `json:"as_of"`
}

type meta struct {
	ETag      string    `json:"etag"`
	FetchedAt time.Time `json:"fetchedAt"`
}

// Load returns the catalog, from cache when it is fresh, otherwise from
// dshfind with a conditional request. A stale cache is used when dshfind is
// unreachable. DSDOCK_NO_CATALOG=1 turns it off; DSDOCK_CATALOG_URL points it
// at another server.
func Load(ctx context.Context, cacheDir string) (*Index, error) {
	if os.Getenv("DSDOCK_NO_CATALOG") == "1" {
		return nil, errors.New("disabled by DSDOCK_NO_CATALOG")
	}
	base := os.Getenv("DSDOCK_CATALOG_URL")
	if base == "" {
		base = DefaultBase
	}
	body := filepath.Join(cacheDir, "dshfind-catalog.json")
	metaPath := body + ".meta"
	var m meta
	if b, err := os.ReadFile(metaPath); err == nil {
		_ = json.Unmarshal(b, &m)
	}
	cached := func() (*Index, error) { return parseFile(body) }
	if time.Since(m.FetchedAt) < TTL {
		if ix, err := cached(); err == nil {
			return ix, nil
		}
	}

	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(base, "/")+"/v1/catalog", nil)
	if err != nil {
		return nil, err
	}
	if m.ETag != "" {
		if _, err := os.Stat(body); err == nil {
			req.Header.Set("If-None-Match", m.ETag)
		}
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		if ix, cerr := cached(); cerr == nil {
			return ix, nil
		}
		return nil, err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusNotModified:
		m.FetchedAt = time.Now().UTC()
		writeMeta(metaPath, m)
		return cached()
	case http.StatusOK:
	default:
		if ix, cerr := cached(); cerr == nil {
			return ix, nil
		}
		return nil, fmt.Errorf("dshfind catalog: %s", resp.Status)
	}
	var d document
	if err := json.NewDecoder(io.LimitReader(resp.Body, 128<<20)).Decode(&d); err != nil {
		return nil, fmt.Errorf("dshfind catalog: %w", err)
	}
	// Cache only the fields DSDock reads: about 23 MB of catalog becomes a
	// small file that loads fast on every check.
	if slim, err := json.Marshal(d); err == nil && os.MkdirAll(cacheDir, 0o755) == nil {
		tmp := body + ".tmp"
		if os.WriteFile(tmp, slim, 0o644) == nil && os.Rename(tmp, body) == nil {
			writeMeta(metaPath, meta{ETag: resp.Header.Get("ETag"), FetchedAt: time.Now().UTC()})
		}
	}
	return build(d.Data, d.AsOf), nil
}

func parseFile(path string) (*Index, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return parse(b)
}

func parse(b []byte) (*Index, error) {
	var d document
	if err := json.Unmarshal(b, &d); err != nil {
		return nil, fmt.Errorf("dshfind catalog: %w", err)
	}
	return build(d.Data, d.AsOf), nil
}

func writeMeta(path string, m meta) {
	if b, err := json.Marshal(m); err == nil {
		_ = os.WriteFile(path, b, 0o644)
	}
}
