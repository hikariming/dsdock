package npmreg

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func tarball(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		tw.Write([]byte(body))
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

func TestClient(t *testing.T) {
	tgz := tarball(t, map[string]string{
		"package/package.json":     `{}`,
		"package/cordis.patch.yml": "- insert:\n    - id: x\n",
		"package/index.js":         "",
	})
	mux := http.NewServeMux()
	var srv *httptest.Server
	mux.HandleFunc("/@scope%2Fpkg", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"name":"@scope/pkg","dist-tags":{"latest":"1.1.0"},"versions":{"1.0.0":{"version":"1.0.0"},"1.1.0":{"version":"1.1.0","engines":{"dsh":">=0.2.0"}}}}`))
	})
	mux.HandleFunc("/@scope%2Fpkg/latest", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"name":"@scope/pkg","version":"1.1.0","scripts":{"postinstall":"node x"},"dsh":{"bundle":{"patch":"./cordis.patch.yml"}},"dist":{"tarball":"` + srv.URL + `/t.tgz"}}`))
	})
	mux.HandleFunc("/t.tgz", func(w http.ResponseWriter, r *http.Request) { w.Write(tgz) })
	srv = httptest.NewServer(mux)
	defer srv.Close()

	t.Setenv("DSDOCK_FILE_CDN", "off") // exercise the tarball path
	c := New(srv.URL)
	ctx := context.Background()
	p, err := c.Packument(ctx, "@scope/pkg")
	if err != nil || p.DistTags["latest"] != "1.1.0" || len(p.Versions) != 2 || string(p.Versions["1.1.0"].Engines) == "" {
		t.Fatalf("packument = %+v, %v", p, err)
	}
	m, err := c.Manifest(ctx, "@scope/pkg", "")
	if err != nil || m.Scripts["postinstall"] == "" {
		t.Fatalf("manifest = %+v, %v", m, err)
	}
	files, err := c.ReadFiles(ctx, m, "./cordis.patch.yml", "missing.yml")
	if err != nil {
		t.Fatal(err)
	}
	if string(files["cordis.patch.yml"]) == "" || len(files) != 1 {
		t.Fatalf("files = %v", files)
	}
	if _, err := c.Manifest(ctx, "nope", "1.0.0"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing package err = %v", err)
	}
}

func TestGitHubRepo(t *testing.T) {
	cases := map[string]string{
		`"https://github.com/PlutoKeating/dsh-lark-bot"`:                     "PlutoKeating/dsh-lark-bot",
		`{"type":"git","url":"git+https://github.com/liustack/modlens.git"}`: "liustack/modlens",
		`{"url":"git@github.com:owner/repo.git"}`:                            "owner/repo",
		`"github:owner/repo"`:                                                "owner/repo",
		`{"url":"https://github.com/o/r/tree/main/packages/x"}`:              "o/r",
		`"https://gitlab.com/o/r"`:                                           "",
		`null`:                                                               "",
	}
	for in, want := range cases {
		if got := GitHubRepo([]byte(in)); got != want {
			t.Errorf("GitHubRepo(%s) = %q, want %q", in, got, want)
		}
	}
}

func TestReadFilesPrefersCDN(t *testing.T) {
	var tarballHits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/cdn/@scope/pkg@1.0.0/cordis.patch.yml":
			w.Write([]byte("- insert: []\n"))
		case "/t.tgz":
			tarballHits++
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	t.Setenv("DSDOCK_FILE_CDN", srv.URL+"/cdn")
	c := New(srv.URL)
	m := &Manifest{Name: "@scope/pkg", Version: "1.0.0"}
	m.Dist.Tarball, m.Dist.UnpackedSize = srv.URL+"/t.tgz", 24<<20
	files, err := c.ReadFiles(context.Background(), m, "./cordis.patch.yml", "absent.yml")
	if err != nil || string(files["cordis.patch.yml"]) == "" || len(files) != 1 || tarballHits != 0 {
		t.Fatalf("files = %v, err = %v, tarball hits = %d", files, err, tarballHits)
	}

	t.Setenv("DSDOCK_FILE_CDN", "off")
	if _, err := c.ReadFiles(context.Background(), m); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("large package without CDN: %v", err)
	}
}
