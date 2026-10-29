package usage

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
)

func writeLog(t *testing.T, home, cwd, id string, lines []string, truncate int) string {
	t.Helper()
	dir := filepath.Join(home, "sessions", cwd, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, LogName)
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	enc, _ := zstd.NewWriter(f)
	for _, l := range lines {
		enc.Write([]byte(l + "\n"))
		enc.Flush() // one frame block per line, as a growing log has
	}
	enc.Close()
	f.Close()
	if truncate > 0 {
		fi, _ := os.Stat(path)
		os.Truncate(path, fi.Size()-int64(truncate))
	}
	return path
}

func reply(ms int64, model string, in, out, cache int64) string {
	return `{"type":"assistant/message","seq":1,"time":` + itoa(ms) + `,"data":{"usage":{"inputTokens":` + itoa(in) +
		`,"outputTokens":` + itoa(out) + `,"cacheReadTokens":` + itoa(cache) + `,"totalTokens":` + itoa(in+out+cache) +
		`},"message":{"role":"assistant","source":{"kind":"model","provider":"deepseek-official","model":"` + model + `"}}}}`
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func TestScan(t *testing.T) {
	home := t.TempDir()
	now := time.Date(2026, 10, 29, 15, 0, 0, 0, time.Local)
	today := now.UnixMilli()
	yesterday := now.AddDate(0, 0, -1).UnixMilli()
	old := now.AddDate(0, 0, -60).UnixMilli()
	big := `{"type":"tool/result","seq":5,"time":1,"data":{"output":"` + strings.Repeat("x", 600*1024) + `"}}`
	writeLog(t, home, "--a--", "session-1", []string{
		`{"type":"session","version":4,"id":"session-1","createdAt":` + itoa(yesterday) + `,"cwd":"/a"}`,
		`{"type":"session/title","seq":2,"time":1,"data":{"title":"找 PDF"}}`,
		reply(yesterday, "deepseek-flash", 100, 20, 900),
		big,
		reply(today, "deepseek-v4-pro", 50, 10, 0),
	}, 0)
	writeLog(t, home, "--b--", "session-2", []string{
		`{"type":"session","version":4,"id":"session-2","createdAt":` + itoa(old) + `,"cwd":"/b"}`,
		reply(old, "deepseek-flash", 1000, 1000, 0),
		reply(today, "deepseek-flash", 7, 3, 0),
		reply(today, "deepseek-flash", 7, 3, 0)[:70], // half written: DSH is still appending
	}, 5) // and the frame's checksum is not there yet

	s := &Scanner{Home: home, CachePath: filepath.Join(t.TempDir(), "cache.json")}
	r, err := s.Scan(30, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Days) != 30 || r.Days[29].Date != "2026-10-29" {
		t.Fatalf("days = %d, last %v", len(r.Days), r.Days[len(r.Days)-1].Date)
	}
	if r.Today.Total != 60+10 || r.Today.Requests != 2 {
		t.Errorf("today = %+v", r.Today)
	}
	if r.Total.CacheRead != 900 || r.Models["deepseek-flash"].Total != 1020+10 {
		t.Errorf("total = %+v, models = %+v", r.Total, r.Models)
	}
	if r.Sessions != 2 || r.Top[0].ID != "session-1" || r.Top[0].Title != "找 PDF" || r.Top[0].Cwd != "/a" {
		t.Errorf("top = %+v", r.Top)
	}
	if r.Skipped != 0 {
		t.Errorf("skipped %d", r.Skipped)
	}

	// a second scan reads the cache; changing a log re-reads only that one
	again, err := s.Scan(90, now)
	if err != nil || again.Total.Total != r.Total.Total+2000 {
		t.Fatalf("90 days = %+v, %v", again.Total, err)
	}
}

func TestScanRealLogs(t *testing.T) {
	home := os.Getenv("DSDOCK_IT_DSH_HOME")
	if home == "" {
		t.Skip("set DSDOCK_IT_DSH_HOME to read a real DSH home's sessions")
	}
	r, err := (&Scanner{Home: home}).Scan(365, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%d sessions, %d skipped, total %+v, models %v", r.Sessions, r.Skipped, r.Total, r.Models)
	if r.Skipped != 0 {
		t.Errorf("%d logs could not be read", r.Skipped)
	}
}
