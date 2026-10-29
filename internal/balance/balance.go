// Package balance asks DeepSeek how much credit the user's API key has left.
//
// The key is found where DSH looks for it, in DSH's order: the credential
// store ($DSH_HOME/.credentials.yaml, where DSH web saves keys), the
// environment, then $DSH_HOME/.env. It is sent to api.deepseek.com and
// nowhere else, and never appears in errors or logs.
//
// Users signed in with a DeepSeek account instead of a key have a different
// wallet that only DSH's account service can read; Lookup says so, and the
// page links to DSH web for it.
package balance

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Endpoint is DeepSeek's balance API (a variable for tests).
var Endpoint = "https://api.deepseek.com/user/balance"

const keyName = "DEEPSEEK_API_KEY"

// Kinds of result.
const (
	KindAPIKey  = "api-key" // queried with the key
	KindAccount = "account" // signed in with an account: see DSH web
	KindNone    = "none"    // no key and no sign-in found
)

// Info is one currency's balance; amounts are decimal strings, as sent.
type Info struct {
	Currency string `json:"currency"`
	Total    string `json:"total"`
	Granted  string `json:"granted"`
	ToppedUp string `json:"toppedUp"`
}

// Result is what the page shows.
type Result struct {
	Kind      string `json:"kind"`
	Source    string `json:"source,omitempty"` // where the key came from
	Available bool   `json:"available"`
	Infos     []Info `json:"infos,omitempty"`
	Error     string `json:"error,omitempty"`
	Checked   int64  `json:"checked"` // ms since epoch
}

// Lookup finds the user's key and asks DeepSeek. client may be nil.
func Lookup(ctx context.Context, home string, client *http.Client) *Result {
	r := &Result{Checked: time.Now().UnixMilli()}
	key, source, account := findKey(home)
	switch {
	case key != "":
		r.Kind, r.Source = KindAPIKey, source
	case account:
		r.Kind = KindAccount
		return r
	default:
		r.Kind = KindNone
		return r
	}
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, Endpoint, nil)
	if err != nil {
		r.Error = err.Error()
		return r
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)
	resp, err := client.Do(req)
	if err != nil {
		r.Error = "连不上 api.deepseek.com"
		return r
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		r.Error = "API key 无效"
		return r
	}
	if resp.StatusCode != http.StatusOK {
		r.Error = fmt.Sprintf("DeepSeek 返回 %s", resp.Status)
		return r
	}
	var body struct {
		IsAvailable  bool `json:"is_available"`
		BalanceInfos []struct {
			Currency        string `json:"currency"`
			TotalBalance    string `json:"total_balance"`
			GrantedBalance  string `json:"granted_balance"`
			ToppedUpBalance string `json:"topped_up_balance"`
		} `json:"balance_infos"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(nil, resp.Body, 1<<20)).Decode(&body); err != nil {
		r.Error = "看不懂 DeepSeek 的回复"
		return r
	}
	r.Available = body.IsAvailable
	for _, b := range body.BalanceInfos {
		r.Infos = append(r.Infos, Info{Currency: b.Currency, Total: b.TotalBalance, Granted: b.GrantedBalance, ToppedUp: b.ToppedUpBalance})
	}
	return r
}

// findKey returns the API key and where it came from, and whether a
// DeepSeek account sign-in is stored.
func findKey(home string) (key, source string, account bool) {
	var store struct {
		Refs    map[string]string `yaml:"refs"`
		Records map[string]any    `yaml:"records"`
	}
	if b, err := os.ReadFile(filepath.Join(home, ".credentials.yaml")); err == nil && yaml.Unmarshal(b, &store) == nil {
		for id := range store.Records {
			if strings.Contains(id, "deepseek-account") {
				account = true
			}
		}
		if k := strings.TrimSpace(store.Refs[keyName]); k != "" {
			return k, "DSH 保存的 key", account
		}
	}
	if k := strings.TrimSpace(os.Getenv(keyName)); k != "" {
		return k, "环境变量", account
	}
	if k := dotenv(filepath.Join(home, ".env"), keyName); k != "" {
		return k, "$DSH_HOME/.env", account
	}
	return "", "", account
}

// dotenv reads one variable from a .env file.
func dotenv(path, name string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(sc.Text()), "export "))
		k, v, ok := strings.Cut(line, "=")
		if !ok || strings.TrimSpace(k) != name {
			continue
		}
		v = strings.TrimSpace(v)
		if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[len(v)-1] == v[0] {
			v = v[1 : len(v)-1]
		}
		return v
	}
	return ""
}
