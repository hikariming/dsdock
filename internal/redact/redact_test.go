package redact

import (
	"strings"
	"testing"
)

func TestText(t *testing.T) {
	cases := map[string]string{
		"dsh web: http://127.0.0.1:38081/?token=FAKEtoken0123456789abcdefFAKE": "dsh web: http://127.0.0.1:38081/?token=<redacted>",
		"key sk-abcdefghijklmnop1234 used":                                     "key sk-<redacted> used",
		"Authorization: Bearer abc.def.ghi":                                    "Authorization: Bearer <redacted>",
		`{"apiKey": "supersecretvalue"}`:                                       `{"apiKey": "<redacted>"}`,
		"DEEPSEEK_API_KEY=sk-0123456789abcdefXYZ":                              "DEEPSEEK_API_KEY=<redacted>",
		"nothing to see here, token count is 5":                                "nothing to see here, token count is 5",
	}
	for in, want := range cases {
		if got := Text(in); got != want {
			t.Errorf("Text(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTextKeepsLength(t *testing.T) {
	in := strings.Repeat("plain log line\n", 3)
	if Text(in) != in {
		t.Error("plain text changed")
	}
}
