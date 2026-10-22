package cli

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JGoterris/orbit/internal/config"
	"github.com/JGoterris/orbit/internal/core/logs"
	"github.com/JGoterris/orbit/internal/state"
)

// `orbit logs --json` without a running orbit reads the log files and prints one JSON object per line.
func TestLogsJSON(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	cfg := &config.OrbitConfig{Root: t.TempDir(), Name: "demo", Order: []string{"api", "web"}, Services: map[string]*config.ServiceConfig{
		"api": {Name: "api", Type: config.TypeProcess},
		"web": {Name: "web", Type: config.TypeProcess},
	}}
	files := state.Files(state.Dir(cfg), "api")
	os.MkdirAll(filepath.Dir(files.Out), 0o755)
	os.WriteFile(files.Out, []byte("listening on :3000\nGET /\n"), 0o644)
	os.WriteFile(files.Err, []byte("boom\n"), 0o644)

	r, w, _ := os.Pipe()
	stdout := os.Stdout
	os.Stdout = w
	code := RunLogs(cfg, nil, LogsOptions{Lines: 10, JSON: true})
	os.Stdout = stdout
	w.Close()
	out, _ := io.ReadAll(r)
	if code != 0 {
		t.Fatalf("exit code = %d", code)
	}

	var got []jsonLine
	for _, raw := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		var l jsonLine
		if err := json.Unmarshal([]byte(raw), &l); err != nil {
			t.Fatalf("not JSON: %q (%v)", raw, err)
		}
		got = append(got, l)
	}
	want := []jsonLine{
		{Service: "api", Stream: logs.Stdout, Text: "listening on :3000"},
		{Service: "api", Stream: logs.Stdout, Text: "GET /"},
		{Service: "api", Stream: logs.Stderr, Text: "boom"},
		{Service: "web", Stream: logs.System, Text: "no logs"},
	}
	if len(got) != len(want) {
		t.Fatalf("lines = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}
