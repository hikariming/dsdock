// Package gui serves DSDock's interface to a browser on this machine.
//
// It is a thin shell over internal/core, like the CLI. The same assets are
// what a native window (Wails) will load later.
//
// Security: the server listens on 127.0.0.1 only, every API call must carry
// a random per-run token (handed to the page in the URL fragment, which
// never reaches a server log), and the Host header must name the loopback
// address so a web page cannot reach the API through DNS rebinding.
package gui

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hikariming/dsdock/internal/core"
)

//go:embed assets
var assets embed.FS

// Desktop is what the native app does for the page: the menu bar panel
// opens the main window and quits through it.
type Desktop interface {
	Open(route string)
	Quit()
	// ResizePanel fits the menu bar panel to its content's height.
	ResizePanel(height int)
	// Attention shows the panel: a quick prompt is waiting for an answer.
	Attention()
}

// Server is one running GUI.
type Server struct {
	core    *core.Core
	token   string
	version string
	port    int
	desktop Desktop

	jobs  *jobs
	plans sync.Map // id -> *pendingPlan
	quick quickHub
}

type pendingPlan struct {
	plan    *core.Plan
	sw      *core.SwitchPlan
	expires time.Time
}

// New prepares a server. version is DSDock's own version, shown in the UI.
func New(c *core.Core, version string) *Server {
	return &Server{core: c, token: randomHex(24), version: version, jobs: newJobs(c)}
}

// Embed serves the interface inside the desktop app. Its webview fetches
// pages and API calls through the app itself, never over the network, so
// there is no port to protect and the token, Host and Origin checks are off.
func (s *Server) Embed(d Desktop) { s.desktop = d }

// Token is the per-run secret API calls must send.
func (s *Server) Token() string { return s.token }

// Serve listens on 127.0.0.1:port (0 picks a free port), reports the URL
// through ready, and runs until ctx ends.
func (s *Server) Serve(ctx context.Context, port int, ready func(url string)) error {
	l, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		return err
	}
	s.port = l.Addr().(*net.TCPAddr).Port
	srv := &http.Server{Handler: s.Handler(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()
	if ready != nil {
		ready(fmt.Sprintf("http://127.0.0.1:%d/#t=%s", s.port, s.token))
	}
	if err := srv.Serve(l); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// Handler is the whole HTTP surface, exposed for tests.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	static, _ := fs.Sub(assets, "assets")
	mux.Handle("GET /", noCache(http.FileServerFS(static)))

	api := func(pattern string, h func(w http.ResponseWriter, r *http.Request) (any, error)) {
		mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			v, err := h(w, r)
			if err != nil {
				writeErr(w, err)
				return
			}
			writeJSON(w, http.StatusOK, v)
		})
	}
	api("GET /api/meta", s.meta)
	api("GET /api/status", s.status)
	api("GET /api/outdated", s.outdated)
	api("GET /api/snapshots", s.snapshots)
	api("POST /api/snapshots", s.takeSnapshot)
	api("GET /api/ops", s.ops)
	api("POST /api/plan", s.makePlan)
	api("POST /api/plan/{id}/apply", s.applyPlan)
	api("POST /api/restore", s.restore)
	api("POST /api/diagnose", s.diagnose)
	api("POST /api/smoke", s.smoke)
	api("GET /api/versions", s.versions)
	api("GET /api/versions/available", s.available)
	api("POST /api/versions/install", s.installVersion)
	api("POST /api/versions/plan", s.versionPlan)
	api("POST /api/versions/plan/{id}/apply", s.applyVersionPlan)
	api("POST /api/versions/unpin", s.unpin)
	api("POST /api/versions/uninstall", s.uninstallVersion)
	api("GET /api/versions/detect", s.detectVersion)
	api("POST /api/versions/adopt", s.adoptVersion)
	api("POST /api/shim", s.installShim)
	api("GET /api/market", s.market)
	api("GET /api/jobs/{id}", s.job)
	api("POST /api/desktop/{action}", s.desktopAction)
	api("POST /api/quick", s.quickStart)
	api("GET /api/quick/current", s.quickCurrent)
	api("POST /api/quick/clear", s.quickClear)
	api("GET /api/quick/{id}", s.quickGet)
	api("POST /api/quick/{id}/answer", s.quickAnswer)
	api("POST /api/quick/{id}/cancel", s.quickCancel)
	if s.desktop != nil {
		return secureHeaders(mux)
	}
	return s.guard(mux)
}

// guard enforces loopback Host, same-origin writes and the token.
func (s *Server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.allowedHost(r.Host) {
			http.Error(w, "forbidden host", http.StatusForbidden)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/") {
			if o := r.Header.Get("Origin"); o != "" && !s.allowedOrigin(o) {
				http.Error(w, "forbidden origin", http.StatusForbidden)
				return
			}
			got := r.Header.Get("X-DSDock-Token")
			if subtle.ConstantTimeCompare([]byte(got), []byte(s.token)) != 1 {
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "令牌无效：请从 dsdock gui 打印的地址重新打开"})
				return
			}
		}
		secureHeaders(next).ServeHTTP(w, r)
	})
}

func secureHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self'; script-src 'self'; connect-src 'self'; frame-ancestors 'none'")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) allowedHost(host string) bool {
	p := strconv.Itoa(s.port)
	return host == "127.0.0.1:"+p || host == "localhost:"+p
}

func (s *Server) allowedOrigin(origin string) bool {
	p := strconv.Itoa(s.port)
	return origin == "http://127.0.0.1:"+p || origin == "http://localhost:"+p
}

// ---------- helpers ----------

type apiError struct {
	status int
	msg    string
}

func (e *apiError) Error() string { return e.msg }

func badRequest(format string, args ...any) error {
	return &apiError{http.StatusBadRequest, fmt.Sprintf(format, args...)}
}

func writeErr(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	var ae *apiError
	if errors.As(err, &ae) {
		status = ae.status
	}
	var np *core.ErrNoProfile
	if errors.As(err, &np) {
		status = http.StatusNotFound
	}
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func decode(r *http.Request, v any) error {
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(v); err != nil {
		return badRequest("请求格式不对：%v", err)
	}
	return nil
}

func noCache(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		h.ServeHTTP(w, r)
	})
}

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

// Open opens url in the default browser.
func Open(url string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", url).Start()
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	default:
		return exec.Command("xdg-open", url).Start()
	}
}
