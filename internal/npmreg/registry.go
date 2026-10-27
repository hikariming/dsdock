// Package npmreg reads package metadata and files from an npm registry
// without installing anything.
package npmreg

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"regexp"
	"strings"
	"time"
)

// DefaultRegistry is the public npm registry.
const DefaultRegistry = "https://registry.npmjs.org"

// Client talks to one registry.
type Client struct {
	Registry string
	HTTP     *http.Client
}

// New returns a client for registry (DefaultRegistry when empty).
func New(registry string) *Client {
	if registry == "" {
		registry = DefaultRegistry
	}
	return &Client{Registry: strings.TrimRight(registry, "/"), HTTP: &http.Client{Timeout: 30 * time.Second}}
}

// Manifest is one published version's package.json, as the registry serves it.
type Manifest struct {
	Name             string            `json:"name"`
	Version          string            `json:"version"`
	Description      string            `json:"description"`
	Scripts          map[string]string `json:"scripts"`
	Engines          json.RawMessage   `json:"engines"`
	PeerDependencies json.RawMessage   `json:"peerDependencies"`
	DSH              json.RawMessage   `json:"dsh"`
	Deprecated       string            `json:"deprecated"`
	HasInstallScript bool              `json:"hasInstallScript"`
	Repository       json.RawMessage   `json:"repository"`
	Dist             struct {
		Tarball      string `json:"tarball"`
		UnpackedSize int64  `json:"unpackedSize"`
	} `json:"dist"`
}

// Packument is the abbreviated document listing every version.
type Packument struct {
	Name     string               `json:"name"`
	DistTags map[string]string    `json:"dist-tags"`
	Versions map[string]*Manifest `json:"versions"`
}

// ErrNotFound means the registry has no such package or version.
var ErrNotFound = errors.New("not found in registry")

func escape(name string) string {
	// Scoped names keep the @ but encode the slash.
	return strings.Replace(url.PathEscape(name), "%40", "@", 1)
}

func (c *Client) get(ctx context.Context, u, accept string, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", accept)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return ErrNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: %s", u, resp.Status)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 64<<20)).Decode(v)
}

// Packument fetches the version list. The abbreviated form still carries
// each version's engines and peerDependencies, which is all version
// selection needs.
func (c *Client) Packument(ctx context.Context, name string) (*Packument, error) {
	var p Packument
	err := c.get(ctx, c.Registry+"/"+escape(name), "application/vnd.npm.install-v1+json; q=1.0, application/json; q=0.8", &p)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	return &p, nil
}

// Manifest fetches one version's full package.json. version may be an exact
// version or a dist-tag.
func (c *Client) Manifest(ctx context.Context, name, version string) (*Manifest, error) {
	if version == "" {
		version = "latest"
	}
	var m Manifest
	if err := c.get(ctx, c.Registry+"/"+escape(name)+"/"+url.PathEscape(version), "application/json", &m); err != nil {
		return nil, fmt.Errorf("%s@%s: %w", name, version, err)
	}
	return &m, nil
}

// FileCDN serves single files out of published npm packages. Reading one
// small file there avoids downloading a whole tarball: a 24 MB plugin kept
// its cordis.patch.yml as the last tarball entry and took over two minutes
// to reach. DSDOCK_FILE_CDN overrides it; "off" disables it.
var FileCDN = "https://cdn.jsdelivr.net/npm"

// maxTarball is the largest unpacked package ReadFiles will download whole
// when the CDN cannot serve the files.
const maxTarball = 5 << 20

// ErrTooLarge means the files could only be read by downloading a package
// larger than maxTarball.
var ErrTooLarge = errors.New("package too large to inspect before installing")

// ReadFiles returns the named files (paths relative to the package root) of
// one published version: from the file CDN when possible, otherwise from the
// tarball if it is small. Files the package lacks are absent from the result.
func (c *Client) ReadFiles(ctx context.Context, m *Manifest, names ...string) (map[string][]byte, error) {
	if out, ok := c.readFromCDN(ctx, m, names); ok {
		return out, nil
	}
	if m.Dist.Tarball == "" {
		return nil, fmt.Errorf("%s@%s has no tarball", m.Name, m.Version)
	}
	if m.Dist.UnpackedSize > maxTarball {
		return nil, ErrTooLarge
	}
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.Dist.Tarball, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req) // the context bounds it; c.HTTP's timeout is for metadata
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", m.Dist.Tarball, resp.Status)
	}
	return readTarball(io.LimitReader(resp.Body, 256<<20), names)
}

// readFromCDN fetches every file from the CDN. ok is false when the CDN is
// off or any request fails for a reason other than the file being absent.
func (c *Client) readFromCDN(ctx context.Context, m *Manifest, names []string) (map[string][]byte, bool) {
	base := FileCDN
	if v := os.Getenv("DSDOCK_FILE_CDN"); v != "" {
		base = v
	}
	if base == "off" || m.Name == "" || m.Version == "" {
		return nil, false
	}
	out := map[string][]byte{}
	for _, n := range names {
		rel := path.Clean(strings.TrimPrefix(n, "./"))
		u := strings.TrimRight(base, "/") + "/" + m.Name + "@" + m.Version + "/" + rel
		rctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		req, err := http.NewRequestWithContext(rctx, http.MethodGet, u, nil)
		if err != nil {
			cancel()
			return nil, false
		}
		resp, err := c.HTTP.Do(req)
		if err != nil {
			cancel()
			return nil, false
		}
		b, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		resp.Body.Close()
		cancel()
		switch {
		case err != nil:
			return nil, false
		case resp.StatusCode == http.StatusNotFound:
			continue
		case resp.StatusCode != http.StatusOK:
			return nil, false
		}
		out[rel] = b
	}
	return out, true
}

func readTarball(r io.Reader, names []string) (map[string][]byte, error) {
	want := map[string]bool{}
	for _, n := range names {
		want[path.Clean(strings.TrimPrefix(n, "./"))] = true
	}
	gz, err := gzip.NewReader(r)
	if err != nil {
		return nil, err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	out := map[string][]byte{}
	for len(out) < len(want) {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		if h.Typeflag != tar.TypeReg {
			continue
		}
		// npm tarballs put everything under one top directory, usually "package/".
		_, rel, ok := strings.Cut(h.Name, "/")
		if !ok || !want[path.Clean(rel)] {
			continue
		}
		b, err := io.ReadAll(io.LimitReader(tr, 4<<20))
		if err != nil {
			return nil, err
		}
		out[path.Clean(rel)] = b
	}
	return out, nil
}

// Releases lists published versions with their publish times, from the full
// packument (the abbreviated one carries no times).
type Releases struct {
	DistTags map[string]string `json:"dist-tags"`
	Time     map[string]string `json:"time"`
	Versions map[string]struct {
		Deprecated string `json:"deprecated"`
	} `json:"versions"`
}

// Releases fetches the version list with publish times.
func (c *Client) Releases(ctx context.Context, name string) (*Releases, error) {
	var r Releases
	if err := c.get(ctx, c.Registry+"/"+escape(name), "application/json", &r); err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	return &r, nil
}

var reGitHub = regexp.MustCompile(`(?i)github\.com[/:]([A-Za-z0-9_.-]+)/([A-Za-z0-9_.-]+?)(?:\.git)?(?:[/#?].*)?$`)

// GitHubRepo returns "owner/repo" for a package.json repository field (a
// string or {"url": ...}), or "" when it does not point at GitHub.
func GitHubRepo(repository json.RawMessage) string {
	var url string
	if json.Unmarshal(repository, &url) != nil {
		var obj struct {
			URL string `json:"url"`
		}
		if json.Unmarshal(repository, &obj) != nil {
			return ""
		}
		url = obj.URL
	}
	return GitHubRepoFromURL(url)
}

// GitHubRepoFromURL accepts github URLs, git+https/ssh forms and the
// "github:owner/repo" shorthand.
func GitHubRepoFromURL(url string) string {
	if rest, ok := strings.CutPrefix(url, "github:"); ok {
		url = "github.com/" + rest
	}
	m := reGitHub.FindStringSubmatch(url)
	if m == nil {
		return ""
	}
	return m[1] + "/" + m[2]
}
