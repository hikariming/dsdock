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
	"path"
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
	Dist             struct {
		Tarball string `json:"tarball"`
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

// ReadFiles streams a version's tarball and returns the named files (paths
// relative to the package root). Files missing from the tarball are absent
// from the result.
func (c *Client) ReadFiles(ctx context.Context, m *Manifest, names ...string) (map[string][]byte, error) {
	if m.Dist.Tarball == "" {
		return nil, fmt.Errorf("%s@%s has no tarball", m.Name, m.Version)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.Dist.Tarball, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", m.Dist.Tarball, resp.Status)
	}
	return readTarball(io.LimitReader(resp.Body, 256<<20), names)
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
