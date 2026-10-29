package balance

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fakeDeepSeek(t *testing.T, wantKey string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+wantKey {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Write([]byte(`{"is_available":true,"balance_infos":[{"currency":"CNY","total_balance":"110.00","granted_balance":"10.00","topped_up_balance":"100.00"}]}`))
	}))
	t.Cleanup(srv.Close)
	old := Endpoint
	Endpoint = srv.URL
	t.Cleanup(func() { Endpoint = old })
}

func TestStoredKeyWins(t *testing.T) {
	home := t.TempDir()
	os.WriteFile(filepath.Join(home, ".credentials.yaml"), []byte("version: 1\nrefs:\n  DEEPSEEK_API_KEY: sk-stored\n"), 0o600)
	os.WriteFile(filepath.Join(home, ".env"), []byte("DEEPSEEK_API_KEY=sk-dotenv\n"), 0o600)
	t.Setenv("DEEPSEEK_API_KEY", "sk-env")
	fakeDeepSeek(t, "sk-stored")
	r := Lookup(context.Background(), home, nil)
	if r.Kind != KindAPIKey || r.Error != "" || !r.Available || len(r.Infos) != 1 || r.Infos[0].Total != "110.00" {
		t.Fatalf("result = %+v", r)
	}
}

func TestEnvThenDotenv(t *testing.T) {
	home := t.TempDir()
	os.WriteFile(filepath.Join(home, ".env"), []byte("# keys\nexport DEEPSEEK_API_KEY=\"sk-dotenv\"\n"), 0o600)
	t.Setenv("DEEPSEEK_API_KEY", "")
	fakeDeepSeek(t, "sk-dotenv")
	if r := Lookup(context.Background(), home, nil); r.Error != "" || r.Source != "$DSH_HOME/.env" {
		t.Fatalf("result = %+v", r)
	}
}

func TestBadKeyIsNotEchoed(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DEEPSEEK_API_KEY", "sk-secret-wrong")
	fakeDeepSeek(t, "sk-right")
	r := Lookup(context.Background(), home, nil)
	if r.Error == "" || strings.Contains(r.Error, "sk-") {
		t.Fatalf("result = %+v", r)
	}
}

func TestAccountAndNone(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "")
	home := t.TempDir()
	if r := Lookup(context.Background(), home, nil); r.Kind != KindNone {
		t.Fatalf("empty home: %+v", r)
	}
	os.WriteFile(filepath.Join(home, ".credentials.yaml"), []byte("version: 1\nrecords:\n  deepseek-account/default:\n    kind: grant\n    payload: {token: x}\n"), 0o600)
	if r := Lookup(context.Background(), home, nil); r.Kind != KindAccount {
		t.Fatalf("account: %+v", r)
	}
}
