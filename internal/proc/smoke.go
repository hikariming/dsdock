// Package proc boots dsh to see whether a profile really starts, and finds
// dsh processes that are already running.
//
// Two facts from M0 shape this package: a plugin that throws while loading
// does not make dsh exit (it only prints "N entry did not activate" on
// stderr), and killing a wrapper instead of the whole process group leaves an
// orphaned Node process behind.
package proc

import (
	"bufio"
	"context"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hikariming/dsdock/internal/dshenv"
	"github.com/hikariming/dsdock/internal/layer"
	"github.com/hikariming/dsdock/internal/profile"
	"github.com/hikariming/dsdock/internal/redact"
)

// SmokeOptions tunes a boot check.
type SmokeOptions struct {
	// Timeout bounds the whole check. A fresh dsh install once took more than
	// 30 s to print anything on its first boot (M0 V10).
	Timeout time.Duration
	// Quiet is how long a profile without the Web app must stay silent before
	// it counts as settled; such profiles print no ready line.
	Quiet time.Duration
}

// DefaultSmoke matches the M0 recommendation.
var DefaultSmoke = SmokeOptions{Timeout: 45 * time.Second, Quiet: 4 * time.Second}

// SmokeResult is what one boot showed.
type SmokeResult struct {
	Ready    bool             `json:"ready"`
	Exited   bool             `json:"exited"`
	ExitCode int              `json:"exitCode,omitempty"`
	TimedOut bool             `json:"timedOut,omitempty"`
	Duration time.Duration    `json:"duration"`
	Inactive []layer.Inactive `json:"inactive,omitempty"`
	Stdout   string           `json:"stdout,omitempty"`
	Stderr   string           `json:"stderr,omitempty"`
}

// OK means dsh started and every plugin activated.
func (r *SmokeResult) OK() bool { return r.Ready && len(r.Inactive) == 0 }

const readyPrefix = "dsh web: http"

// Smoke boots the profile on a free local port, waits until it is ready,
// exits or times out, then stops the whole process group.
func Smoke(ctx context.Context, env *dshenv.Env, p *profile.Profile, opt SmokeOptions) (*SmokeResult, error) {
	args := []string{"--profile", p.Name}
	web := p.HasWeb()
	if web {
		port, err := freePort()
		if err != nil {
			return nil, err
		}
		args = append(args, "--no-open", "--port", strconv.Itoa(port))
	}
	cmd := env.Command(context.WithoutCancel(ctx), args...)
	newGroup(cmd)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}

	start := time.Now()
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	var (
		mu         sync.Mutex
		out, errb  strings.Builder
		lastOutput = start
		ready      = make(chan struct{})
		readyOnce  sync.Once
	)
	var wg sync.WaitGroup
	pump := func(r io.Reader, buf *strings.Builder, isStdout bool) {
		defer wg.Done()
		sc := bufio.NewScanner(r)
		sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
		for sc.Scan() {
			line := sc.Text()
			mu.Lock()
			buf.WriteString(line)
			buf.WriteByte('\n')
			lastOutput = time.Now()
			mu.Unlock()
			if isStdout && strings.HasPrefix(line, readyPrefix) {
				readyOnce.Do(func() { close(ready) })
			}
		}
	}
	wg.Add(2)
	go pump(stdout, &out, true)
	go pump(stderr, &errb, false)

	exited := make(chan error, 1)
	go func() {
		wg.Wait() // pipes must be drained before Wait
		exited <- cmd.Wait()
	}()

	res := &SmokeResult{}
	deadline := time.NewTimer(opt.Timeout)
	defer deadline.Stop()
	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()

	var waitErr error
	done := false
wait:
	for {
		select {
		case <-ready:
			res.Ready = true
			time.Sleep(500 * time.Millisecond) // let trailing warnings arrive
			break wait
		case waitErr = <-exited:
			res.Exited, done = true, true
			break wait
		case <-deadline.C:
			res.TimedOut = true
			break wait
		case <-ctx.Done():
			killGroup(cmd)
			<-exited
			return nil, ctx.Err()
		case <-tick.C:
			if web {
				continue
			}
			mu.Lock()
			quiet := time.Since(lastOutput)
			mu.Unlock()
			if quiet >= opt.Quiet {
				res.Ready = true
				break wait
			}
		}
	}
	res.Duration = time.Since(start)
	if !done {
		killGroup(cmd)
		waitErr = <-exited
	}
	if res.Exited {
		res.Ready = false
		if cmd.ProcessState != nil {
			res.ExitCode = cmd.ProcessState.ExitCode()
		}
	}
	_ = waitErr // a killed group always reports an error; the state above is what matters

	mu.Lock()
	res.Stdout, res.Stderr = redact.Text(out.String()), redact.Text(errb.String())
	mu.Unlock()
	res.Inactive = layer.ParseWarnings(res.Stderr).Inactive
	return res, nil
}

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}
