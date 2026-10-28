package gui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hikariming/dsdock/internal/core"
)

func newServer(t *testing.T) (*Server, http.Handler) {
	t.Helper()
	t.Setenv("DSDOCK_HOME", t.TempDir())
	t.Setenv("DSH_HOME", t.TempDir())
	t.Setenv("DSDOCK_DSH", "")
	c, err := core.Open("")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	s := New(c, "test")
	s.port = 38200 // as if Serve had bound it
	return s, s.Handler()
}

func do(h http.Handler, method, path, host, token, origin, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Host = host
	if token != "" {
		req.Header.Set("X-DSDock-Token", token)
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

const host = "127.0.0.1:38200"

func TestGuard(t *testing.T) {
	s, h := newServer(t)
	tok := s.Token()
	cases := []struct {
		name                      string
		method, path, host, token string
		origin, body              string
		want                      int
	}{
		{"page without token", "GET", "/", host, "", "", "", 200},
		{"api without token", "GET", "/api/meta", host, "", "", "", 401},
		{"api with a wrong token", "GET", "/api/meta", host, "nope", "", "", 401},
		{"api with the token", "GET", "/api/meta", host, tok, "", "", 200},
		{"localhost is fine", "GET", "/api/meta", "localhost:38200", tok, "", "", 200},
		{"rebound host", "GET", "/api/meta", "evil.example:38200", tok, "", "", 403},
		{"other port", "GET", "/api/meta", "127.0.0.1:1", tok, "", "", 403},
		{"page on a rebound host", "GET", "/", "evil.example:38200", "", "", "", 403},
		{"cross-site write", "POST", "/api/snapshots", host, tok, "http://evil.example", `{"profile":"web"}`, 403},
		{"same-origin write", "POST", "/api/plan", host, tok, "http://127.0.0.1:38200", `{"kind":"nonsense"}`, 400},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := do(h, c.method, c.path, c.host, c.token, c.origin, c.body)
			if w.Code != c.want {
				t.Fatalf("status %d, want %d: %s", w.Code, c.want, w.Body)
			}
		})
	}
}

func TestSecurityHeaders(t *testing.T) {
	_, h := newServer(t)
	w := do(h, "GET", "/", host, "", "", "")
	csp := w.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "script-src 'self'") || strings.Contains(csp, "unsafe-inline") {
		t.Errorf("CSP = %q", csp)
	}
	if !strings.Contains(w.Body.String(), `<script src="app.js">`) {
		t.Error("index.html not served")
	}
}

func TestStalePlanRejected(t *testing.T) {
	s, h := newServer(t)
	w := do(h, "POST", "/api/plan/deadbeef/apply", host, s.Token(), "", `{}`)
	if w.Code != 400 || !strings.Contains(w.Body.String(), "过期") {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
}

func TestMissingProfileIs404(t *testing.T) {
	s, h := newServer(t)
	w := do(h, "POST", "/api/diagnose", host, s.Token(), "", `{"profile":"nope"}`)
	if w.Code != 404 {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
}

func TestMarketDropsDSHItself(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("is_plugin") != "1" {
			t.Errorf("market must ask for plugins only: %s", r.URL)
		}
		w.Write([]byte(`{"total":3,"total_pages":1,"data":[
		  {"full_name":"deepseek-ai/deepseek-harness","install":{"pkg_name":"@deepseek-ai/dsh-acp-app","npm_published":true}},
		  {"full_name":"o/inbox","install":{"pkg_name":"@deepseek-ai/dsh-web-app","npm_published":true}},
		  {"full_name":"o/plug","description":"<img src=x onerror=alert(1)>","is_risky":true,"risk_note":"假冒","install":{"pkg_name":"plug","npm_published":true}},
		  {"full_name":"o/gitonly","install":{"pkg_name":"","npm_published":false}}]}`))
	}))
	defer upstream.Close()
	old := MarketBase
	MarketBase = upstream.URL
	defer func() { MarketBase = old }()

	s, h := newServer(t)
	w := do(h, "GET", "/api/market?q=x", host, s.Token(), "", "")
	if w.Code != 200 {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	var resp struct{ Items []MarketItem }
	json.Unmarshal(w.Body.Bytes(), &resp)
	if len(resp.Items) != 2 {
		t.Fatalf("items = %+v", resp.Items)
	}
	if it := resp.Items[0]; it.Spec != "plug" || !it.FromNpm || !it.Risky {
		t.Errorf("npm item = %+v", it)
	}
	if it := resp.Items[1]; it.Spec != "o/gitonly" || it.FromNpm {
		t.Errorf("git item = %+v", it)
	}
}

type fakeDesktop struct {
	opened []string
	height int
}

func (f *fakeDesktop) Open(route string)      { f.opened = append(f.opened, route) }
func (f *fakeDesktop) Quit()                  {}
func (f *fakeDesktop) ResizePanel(height int) { f.height = height }

func TestDesktopActions(t *testing.T) {
	s, h := newServer(t)
	// In a browser the desktop actions do not exist.
	if w := do(h, "POST", "/api/desktop/open", host, s.Token(), "", `{"route":"/versions"}`); w.Code != 404 {
		t.Fatalf("browser: status %d", w.Code)
	}
	// Embedded in the app there is no token and no port; the Host is the
	// webview's own.
	d := &fakeDesktop{}
	s.Embed(d)
	h = s.Handler()
	if w := do(h, "POST", "/api/desktop/open", "wails.localhost", "", "", `{"route":"/versions"}`); w.Code != 200 {
		t.Fatalf("desktop: status %d: %s", w.Code, w.Body)
	}
	if w := do(h, "POST", "/api/desktop/open", "wails.localhost", "", "", `{"route":"javascript:x"}`); w.Code != 200 {
		t.Fatalf("desktop: status %d", w.Code)
	}
	if len(d.opened) != 2 || d.opened[0] != "/versions" || d.opened[1] != "/" {
		t.Fatalf("opened = %v", d.opened)
	}
	if w := do(h, "POST", "/api/desktop/resize", "wails.localhost", "", "", `{"height":420}`); w.Code != 200 || d.height != 420 {
		t.Fatalf("resize: status %d, height %d", w.Code, d.height)
	}
	if w := do(h, "POST", "/api/desktop/resize", "wails.localhost", "", "", `{"height":99999}`); w.Code != 400 {
		t.Fatalf("absurd resize accepted: %d", w.Code)
	}
	var meta map[string]any
	json.Unmarshal(do(h, "GET", "/api/meta", "wails.localhost", "", "", "").Body.Bytes(), &meta)
	if meta["desktop"] != true {
		t.Fatalf("meta = %v", meta)
	}
}
