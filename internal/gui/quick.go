package gui

import (
	"context"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/hikariming/dsdock/internal/quick"
)

// approvalTimeout rejects a question nobody answers: DSH waits meanwhile.
const approvalTimeout = 10 * time.Minute

// quickRun is one prompt from the panel, polled by the page.
type quickRun struct {
	mu        sync.Mutex
	id        string
	prompt    string
	sessionID string
	events    []quick.Event
	pending   map[string]*pendingApproval
	done      bool
	result    *quick.Result
	err       string
	cancel    context.CancelFunc
	started   time.Time
}

type pendingApproval struct {
	approval quick.Approval
	answer   chan string
}

// quickState is what the page reads.
type quickState struct {
	ID        string           `json:"id"`
	Prompt    string           `json:"prompt"`
	SessionID string           `json:"sessionId,omitempty"`
	Events    []quick.Event    `json:"events"`
	Next      int              `json:"next"` // pass as ?from= to get only newer events
	Pending   []quick.Approval `json:"pending"`
	Done      bool             `json:"done"`
	Result    *quick.Result    `json:"result,omitempty"`
	Error     string           `json:"error,omitempty"`
}

func (q *quickRun) state(from int) quickState {
	q.mu.Lock()
	defer q.mu.Unlock()
	if from < 0 || from > len(q.events) {
		from = 0
	}
	st := quickState{ID: q.id, Prompt: q.prompt, SessionID: q.sessionID, Events: append([]quick.Event(nil), q.events[from:]...),
		Next: len(q.events), Pending: []quick.Approval{}, Done: q.done, Result: q.result, Error: q.err}
	for _, p := range q.pending {
		st.Pending = append(st.Pending, p.approval)
	}
	return st
}

type quickHub struct {
	mu      sync.Mutex
	runs    map[string]*quickRun
	current *quickRun
}

func (s *Server) quickStart(w http.ResponseWriter, r *http.Request) (any, error) {
	var req struct{ Prompt, SessionID string }
	if err := decode(r, &req); err != nil {
		return nil, err
	}
	s.quick.mu.Lock()
	defer s.quick.mu.Unlock()
	if c := s.quick.current; c != nil {
		c.mu.Lock()
		busy := !c.done
		c.mu.Unlock()
		if busy {
			return nil, &apiError{http.StatusConflict, "上一个问题还在进行中"}
		}
	}
	env, _, err := s.core.EnvFor("headless")
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	run := &quickRun{id: randomHex(8), prompt: req.Prompt, sessionID: req.SessionID, pending: map[string]*pendingApproval{}, cancel: cancel, started: time.Now()}
	if s.quick.runs == nil {
		s.quick.runs = map[string]*quickRun{}
	}
	s.quick.runs[run.id] = run
	s.quick.current = run
	runner := &quick.Runner{Env: env, DataDir: s.core.DataDir}
	if p := os.Getenv("DSDOCK_QUICK_EXTRA_PATCH"); p != "" {
		runner.ExtraPatches = []string{p} // testing only: e.g. a mock model
	}

	go func() {
		defer cancel()
		res, err := runner.Run(ctx, req.Prompt, req.SessionID, func(e quick.Event) {
			if e.Type == "status" || e.Type == "thinking" {
				return // the page shows actions and answers, not progress ticks
			}
			run.mu.Lock()
			run.events = append(run.events, e)
			if e.Type == "session" {
				run.sessionID = e.SessionID
			}
			run.mu.Unlock()
		}, func(a quick.Approval) string {
			p := &pendingApproval{approval: a, answer: make(chan string, 1)}
			run.mu.Lock()
			run.pending[a.ID] = p
			run.mu.Unlock()
			if s.desktop != nil {
				s.desktop.Attention() // bring the panel up: DSH is waiting
			}
			defer func() {
				run.mu.Lock()
				delete(run.pending, a.ID)
				run.mu.Unlock()
			}()
			select {
			case ans := <-p.answer:
				return ans
			case <-time.After(approvalTimeout):
				return quick.Reject
			case <-ctx.Done():
				return quick.Reject
			}
		})
		run.mu.Lock()
		run.done, run.result = true, res
		if err != nil {
			run.err = err.Error()
			if ctx.Err() != nil {
				run.err = "已取消"
			}
		}
		run.mu.Unlock()
	}()
	return map[string]string{"id": run.id}, nil
}

func (s *Server) quickGet(w http.ResponseWriter, r *http.Request) (any, error) {
	run, err := s.quickRun(r.PathValue("id"))
	if err != nil {
		return nil, err
	}
	from, _ := strconv.Atoi(r.URL.Query().Get("from"))
	return run.state(from), nil
}

func (s *Server) quickCurrent(w http.ResponseWriter, r *http.Request) (any, error) {
	s.quick.mu.Lock()
	c := s.quick.current
	s.quick.mu.Unlock()
	if c == nil {
		return map[string]any{}, nil
	}
	return c.state(0), nil
}

func (s *Server) quickAnswer(w http.ResponseWriter, r *http.Request) (any, error) {
	run, err := s.quickRun(r.PathValue("id"))
	if err != nil {
		return nil, err
	}
	var req struct {
		ApprovalID string
		Allow      bool
	}
	if err := decode(r, &req); err != nil {
		return nil, err
	}
	run.mu.Lock()
	p := run.pending[req.ApprovalID]
	run.mu.Unlock()
	if p == nil {
		return nil, badRequest("这个请求已经处理过了")
	}
	ans := quick.Reject
	if req.Allow {
		ans = quick.Allow
	}
	select {
	case p.answer <- ans:
	default:
	}
	return map[string]bool{"ok": true}, nil
}

func (s *Server) quickCancel(w http.ResponseWriter, r *http.Request) (any, error) {
	run, err := s.quickRun(r.PathValue("id"))
	if err != nil {
		return nil, err
	}
	run.cancel()
	return map[string]bool{"ok": true}, nil
}

func (s *Server) quickClear(w http.ResponseWriter, r *http.Request) (any, error) {
	s.quick.mu.Lock()
	defer s.quick.mu.Unlock()
	if c := s.quick.current; c != nil {
		c.mu.Lock()
		busy := !c.done
		c.mu.Unlock()
		if busy {
			return nil, &apiError{http.StatusConflict, "还在进行中，先取消"}
		}
	}
	s.quick.current = nil
	return map[string]bool{"ok": true}, nil
}

func (s *Server) quickRun(id string) (*quickRun, error) {
	s.quick.mu.Lock()
	defer s.quick.mu.Unlock()
	run := s.quick.runs[id]
	if run == nil {
		return nil, &apiError{http.StatusNotFound, "没有这个对话"}
	}
	return run, nil
}
