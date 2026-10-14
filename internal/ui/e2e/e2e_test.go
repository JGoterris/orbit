//go:build !windows

// Package e2e drives the real orbit binary inside a pseudo-terminal and reads the screen back through the
// terminal emulator, the way a user would see it.
package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JGoterris/orbit/internal/core/console"
	"github.com/JGoterris/orbit/internal/ui/term"
)

var binary string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "orbit-e2e-bin")
	if err != nil {
		panic(err)
	}
	binary = filepath.Join(dir, "orbit")
	out, err := exec.Command("go", "build", "-o", binary, "../../../cmd/orbit").CombinedOutput()
	if err != nil {
		panic(string(out))
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

type screen struct {
	t       *testing.T
	session *console.Session
	term    *term.Terminal
}

func start(t *testing.T, dir string, cols, rows int, args ...string) *screen {
	t.Helper()
	state := t.TempDir()
	s, err := console.NewSession(console.Spec{
		Argv: append([]string{binary}, args...), Cwd: dir,
		Env: map[string]string{"XDG_STATE_HOME": state, "XDG_CONFIG_HOME": t.TempDir(), "NO_COLOR": ""},
	}, cols, rows)
	if err != nil {
		t.Fatal(err)
	}
	tm := term.New(cols, rows, writerFunc(func(b []byte) { s.Write(b) }))
	s.Attach(func(b []byte) { tm.Write(b) })
	sc := &screen{t: t, session: s, term: tm}
	t.Cleanup(func() {
		s.Kill()
		select { // let it finish writing into the temp dirs before they are removed
		case <-s.Done():
		case <-time.After(5 * time.Second):
		}
		tm.Close()
	})
	return sc
}

type writerFunc func([]byte)

func (f writerFunc) Write(b []byte) (int, error) { f(b); return len(b), nil }

func (s *screen) text() string { return s.term.String() }

func (s *screen) send(keys string) { s.session.Write([]byte(keys)) }

func (s *screen) waitFor(what string, contains ...string) string {
	s.t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		txt := s.text()
		ok := true
		for _, c := range contains {
			if !strings.Contains(txt, c) {
				ok = false
			}
		}
		if ok {
			return txt
		}
		time.Sleep(50 * time.Millisecond)
	}
	s.t.Fatalf("timed out waiting for %s (%v); the screen is:\n%s", what, contains, s.text())
	return ""
}

func (s *screen) dump() { s.t.Logf("\n%s", s.text()) }

const project = `name: demo
env:
  LOG_LEVEL: info
services:
  db:
    cmd: echo db-ready; sleep 60
    description: the database
  api:
    cmd: echo api-up; echo "ERROR something broke" >&2; sleep 60
    port: 4100
    depends_on: [db]
  worker:
    cmd: sleep 60
    depends_on: [db]
groups:
  backend: [db, api]
`

func projectDir(t *testing.T, yaml string) string {
	t.Helper()
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "orbit.yaml"), []byte(yaml), 0o644)
	return dir
}

func TestDashboardShowsServicesAndLogs(t *testing.T) {
	sc := start(t, projectDir(t, project), 120, 32, "--no-daemon", "-u")
	sc.waitFor("db running", "Services 3/3", "db-ready", "the database")
	sc.send("j") // api
	sc.waitFor("api selected", "Logs · api", "api-up", "ERROR something broke")
	sc.send("3")
	sc.waitFor("logs view", "Logs · all services", "db-ready", "api-up")
	sc.dump()
}

func TestGraphViewAndNavigation(t *testing.T) {
	sc := start(t, projectDir(t, project), 120, 32, "--no-daemon", "-u")
	sc.waitFor("started", "Services 3/3")
	sc.send("2")
	sc.waitFor("graph", "Dependency graph", "needs", "starts before")
	sc.dump()
	sc.send("\x1b[C") // right: towards what depends on db
	time.Sleep(200 * time.Millisecond)
	sc.dump()
}

func TestOverlays(t *testing.T) {
	sc := start(t, projectDir(t, project), 120, 40, "--no-daemon")
	sc.waitFor("ready", "Services 0/3")
	sc.send("?")
	sc.waitFor("help", "Keys", "press any key to close")
	sc.send("x") // any key closes it... and must not stop anything
	sc.waitFor("closed", "Services 0/3")
	sc.send(":")
	sc.waitFor("palette", "Command palette", "Start all services")
	sc.send("restart wor")
	sc.waitFor("filtered", "Restart worker")
	sc.dump()
	sc.send("\x1b")
	time.Sleep(150 * time.Millisecond) // esc and the next key arriving together would be alt+key
	sc.send("T")
	sc.waitFor("themes", "Theme", "catppuccin-mocha")
	sc.dump()
	sc.send("\x1b")
	time.Sleep(150 * time.Millisecond)
	sc.send("e")
	sc.waitFor("env", "Env · db", "LOG_LEVEL")
	sc.dump()
}

func TestFilterZoomAndQuit(t *testing.T) {
	sc := start(t, projectDir(t, project), 120, 32, "--no-daemon", "-u")
	sc.waitFor("started", "db-ready")
	sc.send("3")
	sc.waitFor("logs view", "api-up")
	sc.send("/")
	sc.send("ERROR")
	sc.waitFor("filter", "filter ERROR", "2 matches")
	sc.send("\r")
	sc.send("z")
	time.Sleep(200 * time.Millisecond)
	sc.dump()
	sc.send("q")
	sc.waitFor("quit confirm", "Quit", "stop all")
	sc.send("s")
	deadline := time.Now().Add(10 * time.Second)
	for sc.session.Alive() && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if sc.session.Alive() {
		t.Fatalf("orbit did not exit:\n%s", sc.text())
	}
	if sc.session.ExitCode() != 0 {
		t.Fatalf("exit code %d", sc.session.ExitCode())
	}
}

func TestConsoleOverlay(t *testing.T) {
	yaml := "services:\n  sh:\n    cmd: sleep 60\n    console: sh\n"
	sc := start(t, projectDir(t, yaml), 120, 32, "--no-daemon")
	sc.waitFor("ready", "Services 0/1")
	sc.send("i")
	sc.waitFor("console", "ctrl+] hide")
	time.Sleep(300 * time.Millisecond)
	sc.send("echo from-console-$((6*7))\r")
	sc.waitFor("console output", "from-console-42")
	sc.dump()
	sc.send("\x1d") // ctrl+]
	sc.waitFor("back", "Services 0/1", "sh")
	sc.send("i") // the live console is reused
	sc.waitFor("console again", "from-console-42")
}

func TestProjectPickerOpensInABareFolderAndSwitches(t *testing.T) {
	target := projectDir(t, "name: target\nservices:\n  one:\n    cmd: sleep 60\n")
	bare := t.TempDir()
	sc := start(t, bare, 120, 30, "--no-daemon")
	sc.waitFor("picker", "Projects", "type a folder path")
	sc.send(target + "/")
	sc.waitFor("typed path", "Open folder "+target)
	sc.send("\r")
	sc.waitFor("switched", "orbit · target", "Services 0/1", "one")
	sc.dump()
}

func TestProjectsAndOpenCommands(t *testing.T) {
	cfgHome, stateHome := t.TempDir(), t.TempDir()
	run := func(args ...string) (string, int) {
		cmd := exec.Command(binary, args...)
		cmd.Env = append(os.Environ(), "XDG_CONFIG_HOME="+cfgHome, "XDG_STATE_HOME="+stateHome, "NO_COLOR=1")
		out, err := cmd.CombinedOutput()
		code := 0
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		}
		return string(out), code
	}
	if out, _ := run("projects"); !strings.Contains(out, "no projects yet") {
		t.Fatalf("empty registry: %q", out)
	}
	dir := projectDir(t, "name: listed\nservices: {}\n")
	// opening it with the TUI would need a terminal: register it the way orbit does
	os.MkdirAll(filepath.Join(cfgHome, "orbit"), 0o755)
	os.WriteFile(filepath.Join(cfgHome, "orbit", "projects.json"),
		[]byte(`{"projects":[{"path":"`+dir+`","name":"listed","lastOpened":1,"pinned":true}]}`), 0o644)
	out, _ := run("projects")
	if !strings.Contains(out, "★ listed") || !strings.Contains(out, dir) {
		t.Fatalf("projects = %q", out)
	}
	out, code := run("open", "nope")
	if code != 1 || !strings.Contains(out, `no project "nope"`) {
		t.Fatalf("open nope: code %d %q", code, out)
	}
	out, code = run("open")
	if code != 1 || !strings.Contains(out, "usage: orbit open") {
		t.Fatalf("open: code %d %q", code, out)
	}
}
