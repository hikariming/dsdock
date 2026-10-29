// Package usage adds up the model tokens recorded in DSH's session logs.
//
// Every session lives in $DSH_HOME/sessions/<cwd>/<session-id>/ as a
// zstd-compressed JSONL log (session.v4.jsonl.zstd). Each model reply is an
// `assistant/message` record whose data carries the reply's usage and the
// provider and model that produced it. Nothing here talks to the network:
// the figures are what this machine's sessions recorded.
//
// Logs only grow, and a user can have thousands, so each file's totals are
// cached by size and modification time; a refresh re-reads only the
// sessions that changed.
package usage

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/klauspost/compress/zstd"
)

// LogName is the file a session's log is written to.
const LogName = "session.v4.jsonl.zstd"

// Tokens is a sum of usage records.
type Tokens struct {
	Input      int64 `json:"input"` // uncached input
	Output     int64 `json:"output"`
	CacheRead  int64 `json:"cacheRead"`
	CacheWrite int64 `json:"cacheWrite"`
	Total      int64 `json:"total"`
	Requests   int   `json:"requests"`
}

func (t *Tokens) add(o Tokens) {
	t.Input += o.Input
	t.Output += o.Output
	t.CacheRead += o.CacheRead
	t.CacheWrite += o.CacheWrite
	t.Total += o.Total
	t.Requests += o.Requests
}

// Session is one session's totals.
type Session struct {
	ID    string `json:"id"`
	Title string `json:"title,omitempty"`
	Cwd   string `json:"cwd,omitempty"`
	Model string `json:"model,omitempty"` // the model of its last reply
	Start int64  `json:"start"`           // ms since epoch
	Last  int64  `json:"last"`
	Tokens
}

// fileStats is what one log adds up to, cached between scans.
type fileStats struct {
	Size    int64                        `json:"size"`
	ModTime int64                        `json:"mod"`
	Days    map[string]map[string]Tokens `json:"days"` // day -> model -> tokens
	Session Session                      `json:"session"`
}

// Day is one calendar day (local time) of usage.
type Day struct {
	Date   string            `json:"date"` // 2006-01-02
	Models map[string]Tokens `json:"models,omitempty"`
	Tokens
}

// Report is usage over the last Days days, ending today.
type Report struct {
	Since    string            `json:"since"`
	Days     []Day             `json:"days"` // every day in the window, oldest first
	Models   map[string]Tokens `json:"models"`
	Total    Tokens            `json:"total"`
	Today    Tokens            `json:"today"`
	Last30   Tokens            `json:"last30"`
	Top      []Session         `json:"top"` // most tokens within the window
	Sessions int               `json:"sessions"`
	Skipped  int               `json:"skipped,omitempty"` // logs that could not be read
}

// Scanner reads a DSH home's sessions.
type Scanner struct {
	Home      string // DSH_HOME
	CachePath string // where per-file totals are kept; "" disables the cache

	mu sync.Mutex
}

// Scan reports usage over the last days days (today included).
func (s *Scanner) Scan(days int, now time.Time) (*Report, error) {
	s.mu.Lock() // one scan at a time keeps the cache file consistent
	defer s.mu.Unlock()
	if days < 1 {
		days = 1
	}
	logs, err := filepath.Glob(filepath.Join(s.Home, "sessions", "*", "*", LogName))
	if err != nil {
		return nil, err
	}
	cache := s.loadCache()
	fresh := make(map[string]*fileStats, len(logs))
	var (
		mu      sync.Mutex
		skipped int
		wg      sync.WaitGroup
		work    = make(chan string)
	)
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for path := range work {
				fi, err := os.Stat(path)
				if err != nil {
					continue
				}
				if c := cache[path]; c != nil && c.Size == fi.Size() && c.ModTime == fi.ModTime().UnixNano() {
					mu.Lock()
					fresh[path] = c
					mu.Unlock()
					continue
				}
				st, err := readLog(path, now.Location())
				mu.Lock()
				if err != nil {
					skipped++
				} else {
					st.Size, st.ModTime = fi.Size(), fi.ModTime().UnixNano()
					fresh[path] = st
				}
				mu.Unlock()
			}
		}()
	}
	for _, p := range logs {
		work <- p
	}
	close(work)
	wg.Wait()
	s.saveCache(fresh)

	r := build(fresh, days, now)
	r.Skipped = skipped
	return r, nil
}

func build(files map[string]*fileStats, days int, now time.Time) *Report {
	today := now.Format(time.DateOnly)
	first := now.AddDate(0, 0, -(days - 1)).Format(time.DateOnly)
	from30 := now.AddDate(0, 0, -29).Format(time.DateOnly)
	r := &Report{Since: first, Models: map[string]Tokens{}, Top: []Session{}}
	byDate := map[string]*Day{}
	for d := 0; d < days; d++ {
		date := now.AddDate(0, 0, -(days-1)+d).Format(time.DateOnly)
		r.Days = append(r.Days, Day{Date: date})
	}
	for i := range r.Days {
		byDate[r.Days[i].Date] = &r.Days[i]
	}
	for _, f := range files {
		var inWindow Tokens
		for date, models := range f.Days {
			for model, t := range models {
				if date >= from30 && date <= today {
					r.Last30.add(t)
				}
				d := byDate[date]
				if d == nil {
					continue
				}
				d.add(t)
				if d.Models == nil {
					d.Models = map[string]Tokens{}
				}
				m := d.Models[model]
				m.add(t)
				d.Models[model] = m
				m = r.Models[model]
				m.add(t)
				r.Models[model] = m
				r.Total.add(t)
				inWindow.add(t)
				if date == today {
					r.Today.add(t)
				}
			}
		}
		if inWindow.Requests > 0 {
			s := f.Session
			s.Tokens = inWindow
			r.Top = append(r.Top, s)
			r.Sessions++
		}
	}
	sort.Slice(r.Top, func(i, j int) bool { return r.Top[i].Total > r.Top[j].Total })
	if len(r.Top) > 5 {
		r.Top = r.Top[:5]
	}
	return r
}

// record is the part of a log line this package reads.
type record struct {
	Type string `json:"type"`
	ID   string `json:"id"`  // session
	Cwd  string `json:"cwd"` // session
	Time int64  `json:"time"`
	// CreatedAt is the session record's timestamp.
	CreatedAt int64 `json:"createdAt"`
	Data      struct {
		Title   string `json:"title"` // session/title
		Usage   *usage `json:"usage"`
		Message struct {
			Source struct {
				Provider string `json:"provider"`
				Model    string `json:"model"`
			} `json:"source"`
		} `json:"message"`
	} `json:"data"`
}

type usage struct {
	InputTokens      int64 `json:"inputTokens"`
	OutputTokens     int64 `json:"outputTokens"`
	TotalTokens      int64 `json:"totalTokens"`
	CacheReadTokens  int64 `json:"cacheReadTokens"`
	CacheWriteTokens int64 `json:"cacheWriteTokens"`
}

// wanted are the record types read; the rest (tool output, mostly) is
// skipped without decoding.
var wanted = [][]byte{
	[]byte(`assistant/message"`),
	[]byte(`"session"`),
	[]byte(`"session/title"`),
}

// readLog adds up one session log. A log still being written may end in a
// partial frame or line; what was read up to there counts.
func readLog(path string, loc *time.Location) (*fileStats, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	dec, err := zstd.NewReader(f, zstd.WithDecoderConcurrency(1), zstd.WithDecoderMaxMemory(1<<30))
	if err != nil {
		return nil, err
	}
	defer dec.Close()
	st := &fileStats{Days: map[string]map[string]Tokens{}}
	st.Session.ID = filepath.Base(filepath.Dir(path))
	br := bufio.NewReaderSize(dec, 256*1024)
	lines := 0
	for {
		line, err := br.ReadSlice('\n')
		if errors.Is(err, bufio.ErrBufferFull) {
			// a record longer than the buffer (a big tool result): not one we read
			for errors.Is(err, bufio.ErrBufferFull) {
				_, err = br.ReadSlice('\n')
			}
			continue
		}
		if len(line) > 0 && want(line) {
			lines++
			var rec record
			if json.Unmarshal(line, &rec) == nil {
				st.take(&rec, loc)
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) || lines > 0 {
				break // the end, or a log cut short while being written
			}
			return nil, err
		}
	}
	return st, nil
}

func want(line []byte) bool {
	head := line
	if len(head) > 80 {
		head = head[:80] // "type" comes first
	}
	if !bytes.Contains(head, []byte(`"type"`)) {
		return false
	}
	for _, w := range wanted {
		if bytes.Contains(head, w) {
			return true
		}
	}
	return false
}

func (st *fileStats) take(rec *record, loc *time.Location) {
	s := &st.Session
	switch rec.Type {
	case "session":
		if rec.ID != "" {
			s.ID = rec.ID
		}
		s.Cwd = rec.Cwd
		if s.Start == 0 {
			s.Start = rec.CreatedAt
		}
	case "session/title":
		if t := strings.TrimSpace(rec.Data.Title); t != "" {
			s.Title = t
		}
	case "assistant/message":
		u := rec.Data.Usage
		if u == nil {
			return
		}
		t := Tokens{Input: u.InputTokens, Output: u.OutputTokens, CacheRead: u.CacheReadTokens, CacheWrite: u.CacheWriteTokens, Total: u.TotalTokens, Requests: 1}
		if t.Total == 0 {
			t.Total = t.Input + t.Output + t.CacheRead + t.CacheWrite
		}
		model := rec.Data.Message.Source.Model
		if model == "" {
			model = "unknown"
		}
		day := time.UnixMilli(rec.Time).In(loc).Format(time.DateOnly)
		if st.Days[day] == nil {
			st.Days[day] = map[string]Tokens{}
		}
		m := st.Days[day][model]
		m.add(t)
		st.Days[day][model] = m
		s.Model = model
		if s.Start == 0 {
			s.Start = rec.Time
		}
		if rec.Time > s.Last {
			s.Last = rec.Time
		}
	}
}

// cacheVersion changes whenever reading a log changes, so totals cached
// by an older DSDock are read again.
const cacheVersion = 2

type cacheFile struct {
	Version int                   `json:"version"`
	Files   map[string]*fileStats `json:"files"`
}

func (s *Scanner) loadCache() map[string]*fileStats {
	if s.CachePath == "" {
		return map[string]*fileStats{}
	}
	var c cacheFile
	if b, err := os.ReadFile(s.CachePath); err == nil && json.Unmarshal(b, &c) == nil && c.Version == cacheVersion && c.Files != nil {
		return c.Files
	}
	return map[string]*fileStats{}
}

func (s *Scanner) saveCache(files map[string]*fileStats) {
	if s.CachePath == "" {
		return
	}
	b, err := json.Marshal(cacheFile{Version: cacheVersion, Files: files})
	if err != nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(s.CachePath), 0o755)
	tmp := s.CachePath + ".tmp"
	if os.WriteFile(tmp, b, 0o600) == nil {
		_ = os.Rename(tmp, s.CachePath)
	}
}
