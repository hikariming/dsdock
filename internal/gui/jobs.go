package gui

import (
	"sync"
	"time"

	"github.com/hikariming/dsdock/internal/core"
	"github.com/hikariming/dsdock/internal/redact"
)

// Job is long work the page polls: installs, boot tests, rescue. Jobs run
// one at a time, so two changes never race on a profile and progress lines
// always belong to the job that printed them.
type Job struct {
	ID       string    `json:"id"`
	Kind     string    `json:"kind"`
	Status   string    `json:"status"` // running, done, failed
	Progress []string  `json:"progress"`
	Result   any       `json:"result,omitempty"`
	Error    string    `json:"error,omitempty"`
	Started  time.Time `json:"started"`
	Ended    time.Time `json:"ended,omitzero"`
}

type jobs struct {
	core *core.Core
	run  sync.Mutex // one job at a time

	mu   sync.Mutex
	byID map[string]*Job
}

func newJobs(c *core.Core) *jobs { return &jobs{core: c, byID: map[string]*Job{}} }

// start queues work and returns its job at once.
func (js *jobs) start(kind string, work func() (any, error)) *Job {
	j := &Job{ID: randomHex(8), Kind: kind, Status: "running", Started: time.Now().UTC()}
	js.mu.Lock()
	js.byID[j.ID] = j
	js.gc()
	js.mu.Unlock()

	go func() {
		js.run.Lock()
		defer js.run.Unlock()
		js.core.SetProgress(func(msg string) {
			js.mu.Lock()
			j.Progress = append(j.Progress, redact.Text(msg))
			js.mu.Unlock()
		})
		defer js.core.SetProgress(nil)

		res, err := work()
		js.mu.Lock()
		defer js.mu.Unlock()
		j.Ended = time.Now().UTC()
		if err != nil {
			j.Status, j.Error = "failed", redact.Text(err.Error())
			return
		}
		j.Status, j.Result = "done", res
	}()
	return j
}

// get returns a copy safe to encode while the job runs.
func (js *jobs) get(id string) (Job, bool) {
	js.mu.Lock()
	defer js.mu.Unlock()
	j, ok := js.byID[id]
	if !ok {
		return Job{}, false
	}
	c := *j
	c.Progress = append([]string(nil), j.Progress...)
	return c, true
}

// gc drops finished jobs older than an hour. Callers hold mu.
func (js *jobs) gc() {
	for id, j := range js.byID {
		if j.Status != "running" && time.Since(j.Ended) > time.Hour {
			delete(js.byID, id)
		}
	}
}
