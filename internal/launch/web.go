// Package launch opens DSH's own interfaces from DSDock: a DSH web instance
// DSDock starts and owns, and the official desktop app when it is installed.
//
// DSH web prints its address on stdout with a token minted for that process
// (`dsh web: http://127.0.0.1:PORT/?token=...`), and that line is the only
// place the token appears. An instance someone else started cannot be
// opened without it, so DSDock runs its own: on a free port, so it never
// takes the 3080 a terminal `dsh --profile web` would use, and stopped when
// DSDock quits.
package launch

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/hikariming/dsdock/internal/dshenv"
	"github.com/hikariming/dsdock/internal/proc"
	"github.com/hikariming/dsdock/internal/redact"
)

// WebProfile is the shipped template that serves the Web UI.
const WebProfile = "web"

// readyTimeout covers a cold start: dsh installs the template profile the
// first time it is used.
const readyTimeout = 3 * time.Minute

// Web is DSDock's DSH web instance; the zero value is ready to use.
type Web struct {
	// LogPath receives the instance's output, tokens redacted.
	LogPath string

	mu       sync.Mutex
	cmd      *exec.Cmd
	url      string
	ready    chan struct{} // closed once url is set or the process ended
	exited   chan struct{}
	startErr error
}

// URL is the running instance's address, token included; "" when none runs.
func (w *Web) URL() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.cmd == nil {
		return ""
	}
	return w.url
}

// Start returns the running instance's address, starting one when none
// runs and waiting until it is ready. Concurrent calls share one start.
func (w *Web) Start(ctx context.Context, env *dshenv.Env) (string, error) {
	return w.start(ctx, func() *exec.Cmd {
		return env.Command(context.Background(), "--profile", WebProfile, "--no-open", "--port", "0")
	})
}

func (w *Web) start(ctx context.Context, command func() *exec.Cmd) (string, error) {
	w.mu.Lock()
	if w.cmd == nil {
		if err := w.spawn(command()); err != nil {
			w.mu.Unlock()
			return "", err
		}
	}
	ready, exited := w.ready, w.exited
	w.mu.Unlock()

	timer := time.NewTimer(readyTimeout)
	defer timer.Stop()
	select {
	case <-ready:
	case <-timer.C:
		w.Stop()
		return "", errors.New("DSH web 迟迟没有启动好，已经停掉；详情见 " + w.LogPath)
	case <-ctx.Done():
		return "", ctx.Err() // keep it starting: the next call picks it up
	}
	if u := w.URL(); u != "" {
		return u, nil
	}
	<-exited // ready without an address: it ended
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.startErr == nil {
		return "", errors.New("DSH web 没能启动")
	}
	return "", w.startErr
}

// spawn starts the process; w.mu is held.
func (w *Web) spawn(cmd *exec.Cmd) error {
	if cmd.Dir == "" {
		cmd.Dir, _ = os.UserHomeDir() // where quick prompts run too
	}
	proc.NewGroup(cmd)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = cmd.Stdout // one stream: the log and the error tail
	var log io.Writer = io.Discard
	var logFile *os.File
	if w.LogPath != "" {
		_ = os.MkdirAll(filepath.Dir(w.LogPath), 0o755)
		if f, err := os.Create(w.LogPath); err == nil {
			logFile, log = f, f
		}
	}
	if err := cmd.Start(); err != nil {
		if logFile != nil {
			logFile.Close()
		}
		return fmt.Errorf("启动 DSH web：%w", err)
	}
	w.cmd, w.url, w.startErr = cmd, "", nil
	w.ready, w.exited = make(chan struct{}), make(chan struct{})
	ready, exited := w.ready, w.exited

	go func() {
		var tail []string
		once := sync.Once{}
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
		for sc.Scan() {
			line := sc.Text()
			if u, ok := ParseReady(line); ok {
				w.mu.Lock()
				if w.cmd == cmd {
					w.url = u
				}
				w.mu.Unlock()
				once.Do(func() { close(ready) })
			}
			clean := redact.Text(line)
			fmt.Fprintln(log, clean)
			if tail = append(tail, clean); len(tail) > 20 {
				tail = tail[1:]
			}
		}
		err := cmd.Wait()
		if logFile != nil {
			logFile.Close()
		}
		w.mu.Lock()
		if w.cmd == cmd {
			w.cmd, w.url = nil, ""
			w.startErr = exitError(err, tail)
		}
		w.mu.Unlock()
		close(exited)
		once.Do(func() { close(ready) })
	}()
	return nil
}

// Stop ends the instance, if one runs, and waits for it to go.
func (w *Web) Stop() {
	w.mu.Lock()
	cmd, exited := w.cmd, w.exited
	w.mu.Unlock()
	if cmd == nil {
		return
	}
	proc.KillGroup(cmd)
	select {
	case <-exited:
	case <-time.After(5 * time.Second):
	}
}

// ParseReady reads dsh web's ready line. Other `dsh web:` lines (opening
// the browser, plain-HTTP warnings) are not addresses.
func ParseReady(line string) (string, bool) {
	rest, ok := strings.CutPrefix(strings.TrimSpace(line), "dsh web: ")
	if !ok || !(strings.HasPrefix(rest, "http://") || strings.HasPrefix(rest, "https://")) || strings.ContainsAny(rest, " \t") {
		return "", false
	}
	return rest, true
}

func exitError(err error, tail []string) error {
	why := ""
	for i := len(tail) - 1; i >= 0; i-- {
		if t := strings.TrimSpace(tail[i]); t != "" {
			why = t
			break
		}
	}
	if why == "" && err != nil {
		why = err.Error()
	}
	if why == "" {
		why = "进程已退出"
	}
	return errors.New("DSH web 没能启动：" + why)
}
