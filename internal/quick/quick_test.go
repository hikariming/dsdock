package quick

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/hikariming/dsdock/internal/dshenv"
)

func TestParseApproval(t *testing.T) {
	a, err := parseApproval(`{"id":"x","toolName":"bash","callId":"c1","reason":{"en":"Allow it","zh":"允许本次操作"}}`)
	if err != nil || a.Reason != "允许本次操作" || a.CallID != "c1" {
		t.Fatalf("%+v %v", a, err)
	}
	a, _ = parseApproval(`{"id":"x","toolName":"bash","reason":"plain"}`)
	if a.Reason != "plain" {
		t.Fatalf("%+v", a)
	}
	if _, err := parseApproval(`{"toolName":"bash"}`); err == nil {
		t.Fatal("approval without id accepted")
	}
}

func TestCommandOf(t *testing.T) {
	if c := commandOf(json.RawMessage(`{"description":"d","command":"docker start web"}`)); c != "docker start web" {
		t.Fatalf("%q", c)
	}
	if c := commandOf(json.RawMessage(`{"pattern":"*.go"}`)); c != "" {
		t.Fatalf("%q", c)
	}
}

func TestExplain(t *testing.T) {
	if e := explain("noise\ndsh: AUTH: Authentication Fails, Your api key: ****0000 is invalid\n"); !strings.Contains(e, "API key") {
		t.Fatalf("%q", e)
	}
	if e := explain("x\ndsh: SERVER: boom\n"); e != "dsh: SERVER: boom" {
		t.Fatalf("%q", e)
	}
}

// mockModel stands in for DeepSeek's Anthropic-style Messages API: the first
// turn runs a command that needs danger-full-access, the second answers in
// text depending on whether the command ran.
func mockModel(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		var body struct {
			Model    string `json:"model"`
			Messages []struct {
				Content json.RawMessage `json:"content"`
			} `json:"messages"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		last := string(body.Messages[len(body.Messages)-1].Content)
		w.Header().Set("Content-Type", "text/event-stream")
		send := func(event string, data any) {
			b, _ := json.Marshal(data)
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b)
		}
		send("message_start", map[string]any{"type": "message_start", "message": map[string]any{"id": "msg", "type": "message", "role": "assistant", "model": body.Model, "content": []any{}, "stop_reason": nil, "usage": map[string]int{"input_tokens": 1, "output_tokens": 0}}})
		if !strings.Contains(last, "tool_result") {
			args, _ := json.Marshal(map[string]string{"description": "test", "command": "echo dsdock-mock-ran", "sandbox_permissions": "danger-full-access", "justification": "测试"})
			send("content_block_start", map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "tool_use", "id": "toolu_1", "name": "bash", "input": map[string]any{}}})
			send("content_block_delta", map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "input_json_delta", "partial_json": string(args)}})
			send("content_block_stop", map[string]any{"type": "content_block_stop", "index": 0})
			send("message_delta", map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": "tool_use"}, "usage": map[string]int{"output_tokens": 1}})
		} else {
			answer := "rejected"
			if strings.Contains(last, "dsdock-mock-ran") {
				answer = "ran"
			}
			send("content_block_start", map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "text", "text": ""}})
			send("content_block_delta", map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "text_delta", "text": answer}})
			send("content_block_stop", map[string]any{"type": "content_block_stop", "index": 0})
			send("message_delta", map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": "end_turn"}, "usage": map[string]int{"output_tokens": 1}})
		}
		send("message_stop", map[string]any{"type": "message_stop"})
	}))
}

// TestIntegrationApproval runs a real headless dsh against the mock model.
// It needs DSDOCK_IT_DSH (dsh's lib/bin.js) and costs no model tokens.
func TestIntegrationApproval(t *testing.T) {
	bin := os.Getenv("DSDOCK_IT_DSH")
	if bin == "" {
		t.Skip("set DSDOCK_IT_DSH to run against a real dsh")
	}
	model := mockModel(t)
	defer model.Close()
	home := t.TempDir()
	env, err := dshenv.ForEntry(home, bin, "")
	if err != nil {
		t.Fatal(err)
	}
	if n := os.Getenv("DSDOCK_IT_NODE"); n != "" {
		env.Node = n
	}
	data := t.TempDir()
	// Point the model at the mock: one more overlay, the way a test would.
	base := filepath.Join(data, "mock.patch.yml")
	os.WriteFile(base, []byte("- id: llm-deepseek\n  config:\n    baseURL: "+model.URL+"\n"), 0o644)
	t.Setenv("DEEPSEEK_API_KEY", "sk-mock")
	cwd := t.TempDir()

	for _, c := range []struct{ answer, want string }{{Allow, "ran"}, {Reject, "rejected"}} {
		t.Run(c.answer, func(t *testing.T) {
			r := &Runner{Env: env, DataDir: data, Cwd: cwd, ExtraPatches: []string{base}}
			var mu sync.Mutex
			var asked []Approval
			var types []string
			res, err := r.Run(context.Background(), "run it", "", func(e Event) {
				mu.Lock()
				types = append(types, e.Type)
				mu.Unlock()
			}, func(a Approval) string {
				mu.Lock()
				asked = append(asked, a)
				mu.Unlock()
				return c.answer
			})
			if err != nil {
				t.Fatal(err)
			}
			if res.Final != c.want || res.ExitCode != 0 || !strings.HasPrefix(res.SessionID, "session-") {
				t.Fatalf("result = %+v", res)
			}
			if len(asked) != 1 || asked[0].Command != "echo dsdock-mock-ran" || asked[0].Reason == "" {
				t.Fatalf("approvals = %+v", asked)
			}
			joined := strings.Join(types, ",")
			if !strings.Contains(joined, "tool_call") || !strings.Contains(joined, "final") {
				t.Fatalf("events = %s", joined)
			}
			// The session is stored where DSH web lists sessions from.
			matches, _ := filepath.Glob(filepath.Join(home, "sessions", "*", res.SessionID))
			if len(matches) != 1 {
				t.Fatalf("session %s not stored under %s/sessions", res.SessionID, home)
			}
		})
	}
}
