package gui

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/hikariming/dsdock/internal/proc"
	"github.com/hikariming/dsdock/internal/rescue"
	"github.com/hikariming/dsdock/internal/shim"
	"github.com/hikariming/dsdock/internal/txn"
	"github.com/hikariming/dsdock/internal/versions"
)

// Background work must outlive the request that started it.
func bg() context.Context { return context.Background() }

func (s *Server) meta(w http.ResponseWriter, r *http.Request) (any, error) {
	return map[string]any{"version": s.version, "home": s.core.Home, "dataDir": s.core.DataDir}, nil
}

func (s *Server) status(w http.ResponseWriter, r *http.Request) (any, error) {
	return s.core.Status(r.Context(), r.URL.Query().Get("profile"))
}

func (s *Server) outdated(w http.ResponseWriter, r *http.Request) (any, error) {
	q := r.URL.Query()
	return s.core.Outdated(r.Context(), q.Get("profile"), q.Get("pre") == "1")
}

func (s *Server) snapshots(w http.ResponseWriter, r *http.Request) (any, error) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 {
		limit = 50
	}
	return s.core.Store.List(s.core.Home, q.Get("profile"), limit)
}

func (s *Server) takeSnapshot(w http.ResponseWriter, r *http.Request) (any, error) {
	var req struct{ Profile string }
	if err := decode(r, &req); err != nil {
		return nil, err
	}
	if _, err := s.core.LoadProfile(req.Profile); err != nil {
		return nil, err
	}
	eng, err := s.core.EngineFor(req.Profile)
	if err != nil {
		return nil, err
	}
	return s.core.TakeSnapshot(r.Context(), eng, req.Profile)
}

func (s *Server) ops(w http.ResponseWriter, r *http.Request) (any, error) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	return s.core.Store.Ops(s.core.Home, q.Get("profile"), limit)
}

// ---------- plugin plans ----------

func (s *Server) remember(p *pendingPlan) string {
	id := randomHex(8)
	p.expires = time.Now().Add(10 * time.Minute)
	s.plans.Store(id, p)
	s.plans.Range(func(k, v any) bool { // drop stale plans
		if time.Now().After(v.(*pendingPlan).expires) {
			s.plans.Delete(k)
		}
		return true
	})
	return id
}

func (s *Server) take(id string) (*pendingPlan, error) {
	v, ok := s.plans.LoadAndDelete(id)
	if !ok || time.Now().After(v.(*pendingPlan).expires) {
		return nil, badRequest("这个操作已经过期，请重新检查")
	}
	return v.(*pendingPlan), nil
}

// makePlan checks a change and returns it for confirmation. Planning can
// take a few seconds (registry, CDN, a dump) but needs no job.
func (s *Server) makePlan(w http.ResponseWriter, r *http.Request) (any, error) {
	var req struct {
		Kind, Profile, Target, To string
		Pre                       bool
	}
	if err := decode(r, &req); err != nil {
		return nil, err
	}
	ctx := r.Context()
	var err error
	p := &pendingPlan{}
	switch req.Kind {
	case "add":
		cwd, _ := os.UserHomeDir()
		p.plan, err = s.core.PlanAdd(ctx, req.Profile, req.Target, cwd, req.Pre)
	case "update":
		p.plan, err = s.core.PlanUpdate(ctx, req.Profile, req.Target, req.To, req.Pre)
	case "remove":
		p.plan, err = s.core.PlanRemove(ctx, req.Profile, req.Target)
	case "disable", "enable":
		p.plan, err = s.core.PlanToggle(ctx, req.Profile, req.Target, req.Kind == "disable")
	default:
		return nil, badRequest("未知的操作 %q", req.Kind)
	}
	if err != nil {
		return nil, err
	}
	resp := map[string]any{
		"plan": p.plan, "blocked": p.plan.Blocked(), "needsConfirmation": p.plan.NeedsConfirmation(),
	}
	if !p.plan.NoChange {
		resp["id"] = s.remember(p)
	}
	return resp, nil
}

func (s *Server) applyPlan(w http.ResponseWriter, r *http.Request) (any, error) {
	var req struct {
		Smoke, Force bool
	}
	if err := decode(r, &req); err != nil {
		return nil, err
	}
	p, err := s.take(r.PathValue("id"))
	if err != nil {
		return nil, err
	}
	if p.plan == nil {
		return nil, badRequest("不是插件操作")
	}
	if p.plan.Blocked() && !req.Force {
		return nil, badRequest("检查没有通过，不能执行")
	}
	plan := p.plan
	return s.jobs.start(plan.Kind, func() (any, error) { return plan.Apply(bg(), req.Smoke) }), nil
}

// ---------- restore / diagnose / smoke ----------

func (s *Server) restore(w http.ResponseWriter, r *http.Request) (any, error) {
	var req struct {
		Snapshot string
		Smoke    bool
	}
	if err := decode(r, &req); err != nil {
		return nil, err
	}
	target, err := s.core.Store.Get(req.Snapshot)
	if err != nil {
		return nil, badRequest("%v", err)
	}
	return s.jobs.start("restore", func() (any, error) {
		eng, err := s.core.EngineFor(target.Profile)
		if err != nil {
			return nil, err
		}
		return eng.Restore(bg(), target.ID, txnOptions(req.Smoke))
	}), nil
}

func (s *Server) diagnose(w http.ResponseWriter, r *http.Request) (any, error) {
	var req struct {
		Profile string
		Boot    bool
	}
	if err := decode(r, &req); err != nil {
		return nil, err
	}
	if _, err := s.core.LoadProfile(req.Profile); err != nil {
		return nil, err
	}
	return s.jobs.start("diagnose", func() (any, error) {
		eng, err := s.core.EngineFor(req.Profile)
		if err != nil {
			return nil, err
		}
		return rescue.Diagnose(bg(), eng, req.Profile, rescue.Options{Boot: req.Boot, SmokeOpts: proc.DefaultSmoke})
	}), nil
}

func (s *Server) smoke(w http.ResponseWriter, r *http.Request) (any, error) {
	var req struct{ Profile string }
	if err := decode(r, &req); err != nil {
		return nil, err
	}
	p, err := s.core.LoadProfile(req.Profile)
	if err != nil {
		return nil, err
	}
	return s.jobs.start("smoke", func() (any, error) {
		env, _, err := s.core.EnvFor(p.Name)
		if err != nil {
			return nil, err
		}
		return proc.Smoke(bg(), env, p, proc.DefaultSmoke)
	}), nil
}

// ---------- versions ----------

func (s *Server) versions(w http.ResponseWriter, r *http.Request) (any, error) {
	vers := s.core.Vers
	installed, err := vers.List()
	if err != nil {
		return nil, err
	}
	def, _ := vers.Default()
	prev, _ := vers.Previous()
	pins, _ := vers.Pins("")
	var mine []versions.Pin
	for _, p := range pins {
		if p.Home == s.core.Home {
			mine = append(mine, p)
		}
	}
	shimPath := filepath.Join(s.core.ShimDir(), "dsh")
	_, statErr := os.Stat(shimPath)
	if statErr != nil {
		_, statErr = os.Stat(shimPath + ".exe")
	}
	return map[string]any{
		"installed": installed, "default": def, "previous": prev, "pins": mine,
		"shimDir": s.core.ShimDir(), "shimInstalled": statErr == nil, "pathHint": shim.PathHint(s.core.ShimDir()),
	}, nil
}

func (s *Server) available(w http.ResponseWriter, r *http.Request) (any, error) {
	return s.core.Vers.Available(r.Context())
}

func (s *Server) installVersion(w http.ResponseWriter, r *http.Request) (any, error) {
	var req struct{ Want string }
	if err := decode(r, &req); err != nil {
		return nil, err
	}
	if req.Want == "" {
		return nil, badRequest("要安装哪个版本？")
	}
	return s.jobs.start("install-dsh", func() (any, error) {
		inst, fresh, err := s.core.Vers.Install(bg(), req.Want)
		if err != nil {
			return nil, err
		}
		madeDefault := false
		if def, _ := s.core.Vers.Default(); def == "" {
			if err := s.core.Vers.SetDefault(inst.Name); err != nil {
				return nil, err
			}
			madeDefault = true
		}
		return map[string]any{"installed": inst, "new": fresh, "default": madeDefault}, nil
	}), nil
}

func (s *Server) versionPlan(w http.ResponseWriter, r *http.Request) (any, error) {
	var req struct{ Kind, Name, Profile string }
	if err := decode(r, &req); err != nil {
		return nil, err
	}
	p := &pendingPlan{}
	var err error
	switch req.Kind {
	case "use":
		p.sw, err = s.core.PlanUse(r.Context(), req.Name)
	case "pin":
		p.sw, err = s.core.PlanPin(r.Context(), req.Profile, req.Name)
	default:
		return nil, badRequest("未知的操作 %q", req.Kind)
	}
	if err != nil {
		return nil, err
	}
	resp := map[string]any{"plan": p.sw, "needsConfirmation": p.sw.NeedsConfirmation(), "affected": p.sw.Affected()}
	if !p.sw.Same {
		resp["id"] = s.remember(p)
	}
	return resp, nil
}

func (s *Server) applyVersionPlan(w http.ResponseWriter, r *http.Request) (any, error) {
	p, err := s.take(r.PathValue("id"))
	if err != nil {
		return nil, err
	}
	if p.sw == nil {
		return nil, badRequest("不是版本操作")
	}
	if err := p.sw.Apply(); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "running": s.core.Running(r.Context(), p.sw.Affected())}, nil
}

func (s *Server) unpin(w http.ResponseWriter, r *http.Request) (any, error) {
	var req struct{ Profile string }
	if err := decode(r, &req); err != nil {
		return nil, err
	}
	return map[string]bool{"ok": true}, s.core.Vers.Unpin(s.core.Home, req.Profile)
}

func (s *Server) uninstallVersion(w http.ResponseWriter, r *http.Request) (any, error) {
	var req struct{ Name string }
	if err := decode(r, &req); err != nil {
		return nil, err
	}
	if err := s.core.Vers.Uninstall(req.Name); err != nil {
		return nil, badRequest("%v", err)
	}
	return map[string]bool{"ok": true}, nil
}

func (s *Server) installShim(w http.ResponseWriter, r *http.Request) (any, error) {
	p, err := shim.Install(s.core.ShimDir())
	if err != nil {
		return nil, err
	}
	return map[string]string{"shim": p, "pathHint": shim.PathHint(s.core.ShimDir())}, nil
}

// ---------- market ----------

// MarketBase is dshfind's API; tests point it elsewhere.
var MarketBase = "https://api.dshfind.com"

// MarketItem is one plugin as the market page shows it.
type MarketItem struct {
	FullName    string   `json:"fullName"`
	Description string   `json:"description"`
	Stars       int      `json:"stars"`
	Grade       string   `json:"grade,omitempty"`
	Score       *float64 `json:"score,omitempty"`
	Category    string   `json:"category,omitempty"`
	Risky       bool     `json:"risky,omitempty"`
	RiskNote    string   `json:"riskNote,omitempty"`
	Archived    bool     `json:"archived,omitempty"`
	Spec        string   `json:"spec"` // what to pass to add
	FromNpm     bool     `json:"fromNpm"`
}

func (s *Server) market(w http.ResponseWriter, r *http.Request) (any, error) {
	q := r.URL.Query()
	page := q.Get("page")
	if page == "" {
		page = "1"
	}
	params := url.Values{"is_plugin": {"1"}, "per_page": {"24"}, "page": {page}}
	if term := q.Get("q"); term != "" {
		params.Set("q", term)
	}
	if sort := q.Get("sort"); sort != "" {
		params.Set("sort", sort)
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, MarketBase+"/v1/plugins?"+params.Encode(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("连不上 dshfind：%w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("dshfind 返回 %s", resp.Status)
	}
	var doc struct {
		Data []struct {
			FullName    string   `json:"full_name"`
			Description string   `json:"description"`
			Stars       int      `json:"stars"`
			Grade       string   `json:"grade"`
			Score       *float64 `json:"score"`
			Category    string   `json:"category"`
			Risky       bool     `json:"is_risky"`
			RiskNote    string   `json:"risk_note"`
			Archived    bool     `json:"archived"`
			Install     struct {
				PkgName      string `json:"pkg_name"`
				NpmPublished bool   `json:"npm_published"`
			} `json:"install"`
		} `json:"data"`
		Total      int `json:"total"`
		TotalPages int `json:"total_pages"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 16<<20)).Decode(&doc); err != nil {
		return nil, err
	}
	items := make([]MarketItem, 0, len(doc.Data))
	for _, d := range doc.Data {
		// DSH itself and its in-box bundles ship with each release; they are
		// installed through version management, never into a profile.
		if strings.HasPrefix(d.Install.PkgName, "@deepseek-ai/dsh") || d.FullName == "deepseek-ai/deepseek-harness" {
			continue
		}
		it := MarketItem{FullName: d.FullName, Description: d.Description, Stars: d.Stars, Grade: d.Grade, Score: d.Score,
			Category: d.Category, Risky: d.Risky, RiskNote: d.RiskNote, Archived: d.Archived, Spec: d.FullName}
		if d.Install.NpmPublished && d.Install.PkgName != "" {
			it.Spec, it.FromNpm = d.Install.PkgName, true
		}
		items = append(items, it)
	}
	return map[string]any{"items": items, "total": doc.Total, "totalPages": doc.TotalPages}, nil
}

func (s *Server) job(w http.ResponseWriter, r *http.Request) (any, error) {
	j, ok := s.jobs.get(r.PathValue("id"))
	if !ok {
		return nil, &apiError{http.StatusNotFound, "任务不存在"}
	}
	return j, nil
}

func txnOptions(smoke bool) txn.Options {
	return txn.Options{Smoke: smoke, SmokeOpts: proc.DefaultSmoke}
}
