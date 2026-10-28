// Package quick asks DSH one thing from DSDock: the menu bar panel's prompt.
//
// It runs the headless profile (one task, then exit) with two additions
// passed as a --patch overlay, so no profile file changes:
//
//   - the credential store is switched back on, so keys saved in DSH web work
//     (headless otherwise reads only the environment and .env files);
//   - dsdock-approval.mjs answers approval requests by asking DSDock. headless
//     has no answerer of its own, so every action outside the sandbox would
//     be rejected without the user ever seeing it.
//
// Sessions land in $DSH_HOME/sessions like any other, so DSH web lists them
// (outside a workspace they appear under "Ungrouped").
package quick

import (
	"bufio"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/hikariming/dsdock/internal/dshenv"
	"github.com/hikariming/dsdock/internal/proc"
	"github.com/hikariming/dsdock/internal/redact"
)

//go:embed assets/dsdock-approval.mjs
var approvalPlugin []byte

const overlay = `# Written by DSDock for quick prompts; passed with --patch, never installed.
# Keys saved in DSH web: headless turns the credential store off by default.
- id: credentials
  disabled: false
- id: authorization
  disabled: false
- id: deepseek-account
  disabled: false
# Ask DSDock when an action needs approval instead of rejecting it unseen.
- insert:
    - id: dsdock-approval
      name: ./dsdock-approval.mjs
`

// Event is one line of `dsh --profile headless --json`: session, status,
// text, thinking, tool_call, tool_result, final.
type Event struct {
	Type      string          `json:"type"`
	SessionID string          `json:"sessionId,omitempty"`
	Text      string          `json:"text,omitempty"`
	CallID    string          `json:"callId,omitempty"`
	Tool      string          `json:"tool,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	Status    string          `json:"status,omitempty"`
	Result    json.RawMessage `json:"result,omitempty"`
	Phase     string          `json:"phase,omitempty"`
	Reason    json.RawMessage `json:"reason,omitempty"`
}

// Approval is a question DSH asks before an action outside its sandbox.
type Approval struct {
	ID       string `json:"id"`
	ToolName string `json:"toolName"`
	CallID   string `json:"callId,omitempty"`
	Reason   string `json:"reason,omitempty"`
	// Command is what the tool call will run, when it is a shell command.
	Command string `json:"command,omitempty"`
}

// Outcomes an approval can get.
const (
	Allow  = "allowed-once"
	Reject = "rejected"
)

// Result is a finished run.
type Result struct {
	SessionID string `json:"sessionId"`
	Final     string `json:"final"`
	ExitCode  int    `json:"exitCode"`
	// Error is dsh's own explanation when the run failed, e.g. a missing key.
	Error string `json:"error,omitempty"`
}

// Runner runs quick prompts.
type Runner struct {
	Env     *dshenv.Env
	DataDir string // DSDock's data directory; the overlay is written under it
	Cwd     string // where DSH works; the user's home by default
	// ExtraPatches are more --patch overlays; tests point the model at a
	// mock with one (DSDOCK_QUICK_EXTRA_PATCH in the GUI).
	ExtraPatches []string
}

// Run asks DSH and blocks until it finishes. onEvent sees every event;
// ask answers approvals (it may block while the user decides) and must
// return Allow or Reject. sessionID continues an earlier quick conversation.
func (r *Runner) Run(ctx context.Context, prompt, sessionID string, onEvent func(Event), ask func(Approval) string) (*Result, error) {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return nil, errors.New("要问什么？")
	}
	patch, err := r.writeOverlay()
	if err != nil {
		return nil, err
	}
	args := []string{"--profile", "headless", "--patch", patch}
	for _, p := range r.ExtraPatches {
		args = append(args, "--patch", p)
	}
	args = append(args, "--json")
	if sessionID != "" {
		args = append(args, "--session-id", sessionID)
	}
	if strings.HasPrefix(prompt, "-") {
		prompt = " " + prompt // never read as an option
	}
	args = append(args, prompt)

	cmd := r.Env.Command(context.WithoutCancel(ctx), args...)
	cmd.Dir = r.Cwd
	if cmd.Dir == "" {
		cmd.Dir, _ = os.UserHomeDir()
	}
	proc.NewGroup(cmd)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	stop := context.AfterFunc(ctx, func() { proc.KillGroup(cmd) })
	defer stop()

	res := &Result{SessionID: sessionID}
	var (
		mu       sync.Mutex
		inputs   = map[string]json.RawMessage{} // callId -> tool input
		diag     strings.Builder
		answerMu sync.Mutex
		wg       sync.WaitGroup
	)
	wg.Add(2)
	go func() { // stdout: the event stream
		defer wg.Done()
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
		for sc.Scan() {
			var ev Event
			if json.Unmarshal(sc.Bytes(), &ev) != nil {
				continue
			}
			mu.Lock()
			switch ev.Type {
			case "session":
				res.SessionID = ev.SessionID
			case "tool_call":
				inputs[ev.CallID] = ev.Input
			case "final":
				res.Final = ev.Text
			}
			mu.Unlock()
			if onEvent != nil {
				onEvent(ev)
			}
		}
	}()
	go func() { // stderr: approvals and diagnostics
		defer wg.Done()
		sc := bufio.NewScanner(stderr)
		sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
		for sc.Scan() {
			line := sc.Text()
			if rest, ok := strings.CutPrefix(line, "dsdock:approval "); ok {
				a, err := parseApproval(rest)
				if err != nil {
					continue
				}
				mu.Lock()
				a.Command = commandOf(inputs[a.CallID])
				mu.Unlock()
				go func(a Approval) { // answering may take minutes
					outcome := Reject
					if ask != nil && ask(a) == Allow {
						outcome = Allow
					}
					b, _ := json.Marshal(map[string]string{"id": a.ID, "outcome": outcome})
					answerMu.Lock()
					_, _ = stdin.Write(append(b, '\n'))
					answerMu.Unlock()
				}(a)
				continue
			}
			if strings.HasPrefix(line, "dsh: reasoning:") {
				continue
			}
			mu.Lock()
			if diag.Len() < 8192 {
				diag.WriteString(redact.Text(line))
				diag.WriteByte('\n')
			}
			mu.Unlock()
		}
	}()
	wg.Wait()
	err = cmd.Wait()
	answerMu.Lock()
	stdin.Close()
	answerMu.Unlock()
	if cmd.ProcessState != nil {
		res.ExitCode = cmd.ProcessState.ExitCode()
	}
	if ctx.Err() != nil {
		return res, ctx.Err()
	}
	if res.ExitCode != 0 {
		res.Error = explain(diag.String())
	}
	var exit *exec.ExitError
	if err != nil && !errors.As(err, &exit) {
		return res, err
	}
	return res, nil
}

func (r *Runner) writeOverlay() (string, error) {
	dir := filepath.Join(r.DataDir, "quick")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dir, "dsdock-approval.mjs"), approvalPlugin, 0o644); err != nil {
		return "", err
	}
	p := filepath.Join(dir, "quick.patch.yml")
	return p, os.WriteFile(p, []byte(overlay), 0o644)
}

// parseApproval reads the plugin's line. dsh's reason may be a plain string
// or {"en": ..., "zh": ...}; the Chinese text is preferred.
func parseApproval(s string) (Approval, error) {
	var raw struct {
		ID       string          `json:"id"`
		ToolName string          `json:"toolName"`
		CallID   string          `json:"callId"`
		Reason   json.RawMessage `json:"reason"`
	}
	if err := json.Unmarshal([]byte(s), &raw); err != nil || raw.ID == "" {
		return Approval{}, fmt.Errorf("bad approval line")
	}
	a := Approval{ID: raw.ID, ToolName: raw.ToolName, CallID: raw.CallID}
	var str string
	if json.Unmarshal(raw.Reason, &str) == nil {
		a.Reason = str
	} else {
		var loc map[string]string
		if json.Unmarshal(raw.Reason, &loc) == nil {
			a.Reason = loc["zh"]
			if a.Reason == "" {
				a.Reason = loc["en"]
			}
		}
	}
	return a, nil
}

// commandOf pulls the shell command out of a bash tool call's input.
func commandOf(input json.RawMessage) string {
	var in struct {
		Command string `json:"command"`
	}
	if json.Unmarshal(input, &in) != nil {
		return ""
	}
	return in.Command
}

// explain turns dsh's error output into one line a user can act on.
func explain(diag string) string {
	for _, l := range strings.Split(diag, "\n") {
		l = strings.TrimSpace(l)
		if strings.Contains(l, "MISSING_CREDENTIAL") || strings.Contains(l, "AUTH:") {
			return "DeepSeek API key 无效或没有配置：请在 DSH web 的设置里添加 API key。（" + l + "）"
		}
	}
	for _, l := range strings.Split(diag, "\n") {
		if t := strings.TrimSpace(l); strings.HasPrefix(t, "dsh: ") || strings.HasPrefix(t, "Error: ") {
			return t
		}
	}
	return strings.TrimSpace(diag)
}
