package catalog

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// Two repositories publish "dsh-lark-bot"; dshfind flags the copy.
const doc = `{"as_of":"2026-10-27T00:00:00Z","data":[
 {"full_name":"PlutoKeating/dsh-lark-bot","is_risky":false,"archived":false,"pushed_at":"2026-10-20T00:00:00Z","score":82,"grade":"A","install":{"pkg_name":"dsh-lark-bot"}},
 {"full_name":"tarraencompassing61/dsh-lark-bot","is_risky":true,"risk_note":"假冒仓库","archived":false,"pushed_at":"2026-10-12T00:00:00Z","score":null,"grade":null,"install":{"pkg_name":"dsh-lark-bot"}},
 {"full_name":"o/solo","is_risky":false,"archived":true,"pushed_at":"2025-01-01T00:00:00Z","install":{"pkg_name":"solo"}}
]}`

func TestLookup(t *testing.T) {
	ix, err := parse([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	m, others := ix.Lookup("dsh-lark-bot", "plutokeating/DSH-LARK-BOT")
	if m == nil || m.Risky || len(others) != 1 || !others[0].Risky {
		t.Fatalf("official: match=%+v others=%v", m, others)
	}
	m, _ = ix.Lookup("dsh-lark-bot", "tarraencompassing61/dsh-lark-bot")
	if m == nil || !m.Risky {
		t.Fatalf("impostor not matched: %+v", m)
	}
	if m, others := ix.Lookup("dsh-lark-bot", ""); m != nil || len(others) != 2 {
		t.Fatalf("ambiguous name must not pick one: %+v %v", m, others)
	}
	if m, _ := ix.Lookup("solo", ""); m == nil || !m.Archived {
		t.Fatalf("unique name: %+v", m)
	}
	if m, _ := ix.Lookup("solo", "someone/else"); m != nil {
		t.Fatalf("a known repo that dshfind lacks must not fall back to the name: %+v", m)
	}
	var nilIx *Index
	if m, o := nilIx.Lookup("x", ""); m != nil || o != nil {
		t.Fatal("nil index")
	}
}

func TestLoadCachesAndRevalidates(t *testing.T) {
	var hits, notModified atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.Header.Get("If-None-Match") == `W/"v1"` {
			notModified.Add(1)
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", `W/"v1"`)
		w.Write([]byte(doc))
	}))
	t.Setenv("DSDOCK_CATALOG_URL", srv.URL)
	t.Setenv("DSDOCK_NO_CATALOG", "")
	dir := t.TempDir()
	ctx := context.Background()

	if _, err := Load(ctx, dir); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(ctx, dir); err != nil { // fresh cache: no request
		t.Fatal(err)
	}
	if hits.Load() != 1 {
		t.Fatalf("hits = %d, want 1", hits.Load())
	}

	writeMeta(dir+"/dshfind-catalog.json.meta", meta{ETag: `W/"v1"`, FetchedAt: time.Now().Add(-2 * TTL)})
	ix, err := Load(ctx, dir)
	if err != nil || notModified.Load() != 1 {
		t.Fatalf("revalidation: %v, 304s = %d", err, notModified.Load())
	}
	if m, _ := ix.Lookup("solo", ""); m == nil {
		t.Fatal("cached data lost after 304")
	}

	srv.Close() // offline with a stale cache still works
	writeMeta(dir+"/dshfind-catalog.json.meta", meta{ETag: `W/"v1"`, FetchedAt: time.Now().Add(-2 * TTL)})
	if _, err := Load(ctx, dir); err != nil {
		t.Fatalf("offline with cache: %v", err)
	}
}

func TestDisabled(t *testing.T) {
	t.Setenv("DSDOCK_NO_CATALOG", "1")
	if _, err := Load(context.Background(), t.TempDir()); err == nil {
		t.Fatal("expected an error when disabled")
	}
}
