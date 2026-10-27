package versions

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Masterminds/semver/v3"

	"github.com/hikariming/dsdock/internal/dshenv"
)

// buildPolicy says which dependency install scripts may run. In M0 only
// dsh-subprocess-local's script mattered: it restores the executable bit on
// node-pty's prebuilt macOS spawn-helper. node-pty and koffi ship or fetch
// prebuilt binaries without their scripts (running node-pty's would compile
// C++ on the user's machine), and the rest do nothing useful. Anything not
// listed is skipped with a warning (strictDepBuilds: false) instead of
// failing the install.
var buildPolicy = []struct {
	pkg   string
	allow bool
}{
	{"@deepseek-ai/dsh-subprocess-local", true},
	{"node-pty", false},
	{"koffi", false},
	{"protobufjs", false},
	{"@google/genai", false},
}

func workspaceYAML() string {
	var b strings.Builder
	b.WriteString("# Written by DSDock. One DSH release per directory; see LESSONS.md.\nnodeLinker: hoisted\nstrictDepBuilds: false\nallowBuilds:\n")
	for _, p := range buildPolicy {
		fmt.Fprintf(&b, "  %q: %v\n", p.pkg, p.allow)
	}
	return b.String()
}

// Release is one published dsh version.
type Release struct {
	Version    string    `json:"version"`
	Published  time.Time `json:"published"`
	Tags       []string  `json:"tags,omitempty"`
	Deprecated bool      `json:"deprecated,omitempty"`
	Installed  bool      `json:"installed"`
}

// Available lists published releases, newest first.
func (m *Manager) Available(ctx context.Context) ([]Release, error) {
	r, err := m.Registry.Releases(ctx, Package)
	if err != nil {
		return nil, err
	}
	tags := map[string][]string{}
	for tag, v := range r.DistTags {
		tags[v] = append(tags[v], tag)
	}
	installed := map[string]bool{}
	if list, err := m.List(); err == nil {
		for _, i := range list {
			if i.Source == Managed {
				installed[i.Version] = true
			}
		}
	}
	var out []Release
	for v, meta := range r.Versions {
		rel := Release{Version: v, Deprecated: meta.Deprecated != "", Installed: installed[v]}
		rel.Tags = tags[v]
		sort.Strings(rel.Tags)
		if t, err := time.Parse(time.RFC3339, r.Time[v]); err == nil {
			rel.Published = t
		}
		out = append(out, rel)
	}
	sort.Slice(out, func(a, b int) bool {
		va, ea := semver.NewVersion(out[a].Version)
		vb, eb := semver.NewVersion(out[b].Version)
		if ea != nil || eb != nil {
			return out[a].Published.After(out[b].Published)
		}
		return va.GreaterThan(vb)
	})
	return out, nil
}

// ResolveVersion turns a version or dist-tag (latest, next, alpha) into an
// exact version.
func (m *Manager) ResolveVersion(ctx context.Context, want string) (string, error) {
	if _, err := semver.StrictNewVersion(want); err == nil {
		return want, nil
	}
	r, err := m.Registry.Releases(ctx, Package)
	if err != nil {
		return "", err
	}
	if v, ok := r.DistTags[want]; ok {
		return v, nil
	}
	return "", fmt.Errorf("%q 既不是版本号，也不是发布渠道（可用：%s）", want, strings.Join(keys(r.DistTags), "、"))
}

// Install installs a release (a version or a dist-tag) if it is not there
// yet. pnpm must be on PATH.
func (m *Manager) Install(ctx context.Context, want string) (*Installed, bool, error) {
	version, err := m.ResolveVersion(ctx, want)
	if err != nil {
		return nil, false, err
	}
	if i, err := m.Get(version); err == nil {
		return i, false, nil
	}

	dir := m.dirFor(version)
	if err := os.RemoveAll(dir); err != nil { // leftovers of an interrupted install
		return nil, false, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, false, err
	}
	marker := filepath.Join(dir, installing)
	ok := false
	defer func() {
		if !ok {
			os.RemoveAll(dir)
		}
	}()
	files := map[string]string{
		installing:            time.Now().UTC().Format(time.RFC3339),
		"package.json":        `{"private": true, "description": "DSH ` + version + `, installed by DSDock"}` + "\n",
		"pnpm-workspace.yaml": workspaceYAML(),
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			return nil, false, err
		}
	}

	m.say("下载并安装 DSH %s", version)
	if err := runPnpm(ctx, dir, "add", Package+"@"+version); err != nil {
		return nil, false, err
	}

	entry := filepath.Join(dir, "node_modules", "@deepseek-ai", "dsh", "lib", "bin.js")
	m.say("检查安装结果")
	got, err := entryVersion(ctx, entry)
	if err != nil {
		return nil, false, err
	}
	if got != version {
		return nil, false, fmt.Errorf("装好的 dsh 报告版本 %s，预期 %s", got, version)
	}
	inst := &Installed{
		Name: version, Version: version, Dir: dir, Entry: entry, Source: Managed,
		SessionFormat: sessionFormat(filepath.Join(dir, "node_modules")),
		NodeRange:     fetchNodeRange(ctx, version),
		InstalledAt:   time.Now().UTC(),
	}
	if err := os.Remove(marker); err != nil {
		return nil, false, err
	}
	if err := m.put(inst); err != nil {
		return nil, false, err
	}
	ok = true
	return inst, true, nil
}

func runPnpm(ctx context.Context, dir string, args ...string) error {
	pnpm := os.Getenv("DSDOCK_PNPM")
	if pnpm == "" {
		p, err := exec.LookPath("pnpm")
		if err != nil {
			return errors.New("没有找到 pnpm：DSH 本身也需要它（dsh plugin 会调用 pnpm），请先安装 pnpm")
		}
		pnpm = p
	}
	cmd := exec.CommandContext(ctx, pnpm, args...)
	cmd.Dir = dir
	cmd.Env = withNodeOnPath(os.Environ())
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("pnpm %s 失败：%v\n%s", strings.Join(args, " "), err, tail(out.String(), 15))
	}
	return nil
}

// withNodeOnPath puts DSDOCK_NODE's directory first so pnpm and the scripts
// it runs use the same Node as dsh will.
func withNodeOnPath(env []string) []string {
	node := os.Getenv("DSDOCK_NODE")
	if node == "" {
		return env
	}
	for i, kv := range env {
		if strings.HasPrefix(strings.ToUpper(kv), "PATH=") {
			env[i] = kv[:5] + filepath.Dir(node) + string(os.PathListSeparator) + kv[5:]
		}
	}
	return env
}

func entryVersion(ctx context.Context, entry string) (string, error) {
	env, err := dshenv.ForEntry("", entry, "")
	if err != nil {
		return "", err
	}
	tctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	cmd := env.Command(tctx, "-V")
	cmd.Env = os.Environ() // -V must not depend on DSH_HOME
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("%s -V: %w", entry, err)
	}
	return strings.TrimSpace(string(out)), nil
}

var reSessionFormat = regexp.MustCompile(`SESSION_FORMAT_VERSION\s*=\s*(\d+)`)

// sessionFormat reads the Session format a release writes; 0 if unknown.
func sessionFormat(nodeModules string) int {
	matches, _ := filepath.Glob(filepath.Join(nodeModules, "@deepseek-ai", "dsh-session", "lib", "*.js"))
	for _, f := range matches {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		if m := reSessionFormat.FindSubmatch(b); m != nil {
			n, _ := strconv.Atoi(string(m[1]))
			return n
		}
	}
	return 0
}

// RawBase serves upstream source files by tag; tests point it elsewhere.
var RawBase = "https://raw.githubusercontent.com/deepseek-ai/deepseek-harness"

// fetchNodeRange reads the Node range a release declares from its source tag
// (dsh-v<version>): apps/cli/package.json first, then the root package.json.
// It returns "" when the tag or field is missing; callers then fall back to
// dshenv.DefaultNodeRange.
func fetchNodeRange(ctx context.Context, version string) string {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	for _, file := range []string{"apps/cli/package.json", "package.json"} {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, RawBase+"/dsh-v"+version+"/"+file, nil)
		if err != nil {
			return ""
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return ""
		}
		var p struct {
			Engines struct {
				Node string `json:"node"`
			} `json:"engines"`
		}
		err = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&p)
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK && err == nil && p.Engines.Node != "" {
			return p.Engines.Node
		}
	}
	return ""
}

// ---------- adopt ----------

// Adopt registers an existing installation (the dsh on PATH outside
// skipDir, or entry when given) as "external". Nothing is moved or deleted.
func (m *Manager) Adopt(ctx context.Context, entry, skipDir string) (*Installed, error) {
	if entry == "" {
		env, err := dshenv.Discover(skipDir)
		if err != nil {
			return nil, err
		}
		entry = env.Entry
	}
	bin, err := findBinJS(entry)
	if err != nil {
		return nil, err
	}
	version, err := entryVersion(ctx, bin)
	if err != nil {
		return nil, err
	}
	pkgRoot := filepath.Dir(filepath.Dir(bin)) // .../@deepseek-ai/dsh
	sf := sessionFormat(filepath.Join(pkgRoot, "node_modules"))
	if sf == 0 {
		sf = sessionFormat(filepath.Dir(filepath.Dir(pkgRoot))) // deps hoisted next to it
	}
	inst := &Installed{
		Name: External, Version: version, Dir: pkgRoot, Entry: bin, Source: SourceExt,
		SessionFormat: sf, NodeRange: fetchNodeRange(ctx, version), InstalledAt: time.Now().UTC(),
	}
	return inst, m.put(inst)
}

// findBinJS goes from a dsh executable to the package's lib/bin.js: through
// symlinks (npm on Unix) or from an npm .cmd shim's directory (Windows).
func findBinJS(p string) (string, error) {
	if real, err := filepath.EvalSymlinks(p); err == nil {
		p = real
	}
	if strings.HasSuffix(filepath.ToSlash(p), "@deepseek-ai/dsh/lib/bin.js") {
		return p, nil
	}
	dir := filepath.Dir(p)
	for _, c := range []string{
		filepath.Join(dir, "node_modules", "@deepseek-ai", "dsh", "lib", "bin.js"),
		filepath.Join(dir, "..", "lib", "node_modules", "@deepseek-ai", "dsh", "lib", "bin.js"),
	} {
		if _, err := os.Stat(c); err == nil {
			if real, err := filepath.EvalSymlinks(c); err == nil {
				return real, nil // the same canonical form as the branch above
			}
			return filepath.Clean(c), nil
		}
	}
	return "", fmt.Errorf("找不到 %s 对应的 @deepseek-ai/dsh/lib/bin.js", p)
}

func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func tail(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
