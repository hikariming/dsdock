package gui

import (
	"context"
	"net/http"

	"github.com/hikariming/dsdock/internal/launch"
)

// launchInfo is what the panel's launch row shows.
func (s *Server) launchInfo(w http.ResponseWriter, r *http.Request) (any, error) {
	apps := launch.FindApps(r.Context())
	if apps == nil {
		apps = []launch.App{}
	}
	return map[string]any{"webRunning": s.web.URL() != "", "apps": apps, "download": launch.DownloadURL}, nil
}

// launchWeb opens DSH web in the browser, starting DSDock's instance first
// when it is not running. The address carries the instance's token, so it
// goes straight to the browser and never to the page.
func (s *Server) launchWeb(w http.ResponseWriter, r *http.Request) (any, error) {
	if err := s.openWeb(r.Context()); err != nil {
		return nil, err
	}
	return map[string]bool{"ok": true}, nil
}

// OpenWeb is the tray menu's "open DSH web"; it may take a while.
func (s *Server) OpenWeb() { _ = s.openWeb(context.Background()) }

func (s *Server) openWeb(ctx context.Context) error {
	env, _, err := s.core.EnvFor(launch.WebProfile)
	if err != nil {
		return err
	}
	u, err := s.web.Start(ctx, env)
	if err != nil {
		return err
	}
	return Open(u)
}

func (s *Server) launchApp(w http.ResponseWriter, r *http.Request) (any, error) {
	var req struct{ ID string }
	if err := decode(r, &req); err != nil {
		return nil, err
	}
	if req.ID == "download" {
		return map[string]bool{"ok": true}, Open(launch.DownloadURL)
	}
	for _, a := range launch.FindApps(r.Context()) {
		if a.ID == req.ID {
			return map[string]bool{"ok": true}, launch.Open(a)
		}
	}
	return nil, &apiError{http.StatusNotFound, "没有找到这个桌面版"}
}
