package preflight

import "testing"

func TestCompat(t *testing.T) {
	const dsh = "0.2.1-rc.4"
	cases := []struct {
		name           string
		engines, peers string
		unmet          bool
		bad            bool
	}{
		{"no requirements", ``, ``, false, false},
		{"engines range includes prerelease runtime", `{"dsh":">=0.2.0"}`, ``, false, false},
		{"engines too new", `{"dsh":">=99.0.0"}`, ``, true, false},
		{"engines decides alone", `{"dsh":">=0.2.0"}`, `{"@deepseek-ai/dsh":">=9"}`, false, false},
		{"engines without dsh falls back to peers", `{"node":">=20"}`, `{"@deepseek-ai/dsh-base":"^0.3.0"}`, true, false},
		{"unrelated peers ignored", ``, `{"react":"^18"}`, false, false},
		{"workspace peer means this dsh", ``, `{"@deepseek-ai/dsh":"workspace:^"}`, false, false},
		{"workspace engines is unmet", `{"dsh":"workspace:^"}`, ``, true, false},
		{"empty range unmet", `{"dsh":""}`, ``, true, false},
		{"caret on 0.x prerelease", ``, `{"@deepseek-ai/dsh":"^0.2.1-rc.1"}`, false, false},
		{"engines not an object", `"dsh"`, ``, false, true},
		{"engines.dsh not a string", `{"dsh":1}`, ``, false, true},
		{"peers not an object", ``, `[]`, false, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			u, err := Compat([]byte(c.engines), []byte(c.peers), dsh)
			if (err != nil) != c.bad {
				t.Fatalf("err = %v", err)
			}
			if (len(u) > 0) != c.unmet {
				t.Fatalf("unmet = %v", u)
			}
		})
	}
}
