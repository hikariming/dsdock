package proc

import (
	"reflect"
	"testing"
)

func TestParsePS(t *testing.T) {
	out := `  101 /opt/node/bin/node /h/v/rc4/node_modules/@deepseek-ai/dsh/lib/bin.js --profile web --no-open --port 38083
  102 node /usr/lib/node_modules/@deepseek-ai/dsh/lib/bin.js tui --resume x
  103 node /usr/lib/node_modules/@deepseek-ai/dsh/lib/bin.js plugin --profile web add x
  104 node /usr/lib/node_modules/@deepseek-ai/dsh/lib/bin.js --profile t1 --dump-config
  105 /usr/bin/vim notes.txt
  106 node C:\Users\u\AppData\Roaming\npm\node_modules\@deepseek-ai\dsh\lib\bin.js --profile=work
`
	got := parsePS(out)
	var pids []int
	var profs []string
	for _, r := range got {
		pids = append(pids, r.PID)
		profs = append(profs, r.Profile)
	}
	if !reflect.DeepEqual(pids, []int{101, 102, 106}) || !reflect.DeepEqual(profs, []string{"web", "tui", "work"}) {
		t.Errorf("pids = %v, profiles = %v", pids, profs)
	}
}
