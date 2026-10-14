//go:build !windows

package runner

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/JGoterris/orbit/internal/config"
	"github.com/JGoterris/orbit/internal/core/logs"
	"github.com/JGoterris/orbit/internal/platform"
	"github.com/JGoterris/orbit/internal/state"
)

func TestSplitArgs(t *testing.T) {
	got := SplitArgs(`redis-server --save "" --appendonly 'yes no'`)
	want := []string{"redis-server", "--save", "", "--appendonly", "yes no"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("SplitArgs = %q, want %q", got, want)
	}
	if got := SplitArgs(`a\ b "c \"d\""`); !reflect.DeepEqual(got, []string{"a b", `c "d"`}) {
		t.Fatalf("escapes = %q", got)
	}
}

func TestContainerName(t *testing.T) {
	if got := ContainerName("My Shop", "api/v1"); got != "orbit-my-shop-api-v1" {
		t.Fatalf("name = %q", got)
	}
}

type events struct {
	mu   sync.Mutex
	exit *struct {
		code *int
		sig  string
	}
	lines []string
}

func (e *events) cb() Callbacks {
	return Callbacks{
		Log: func(s logs.Stream, text string) { e.mu.Lock(); e.lines = append(e.lines, text); e.mu.Unlock() },
		Exit: func(code *int, sig string) {
			e.mu.Lock()
			e.exit = &struct {
				code *int
				sig  string
			}{code, sig}
			e.mu.Unlock()
		},
	}
}

func (e *events) exited() bool { e.mu.Lock(); defer e.mu.Unlock(); return e.exit != nil }

func until(t *testing.T, what string, cond func() bool) {
	t.Helper()
	end := time.Now().Add(8 * time.Second)
	for time.Now().Before(end) {
		if cond() {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func procSvc(cmd string, dir string) *config.ServiceConfig {
	return &config.ServiceConfig{Name: "p", Type: config.TypeProcess, Cmd: cmd, Cwd: dir, Env: map[string]string{}}
}

func TestProcessRunnerStartsReportsItsPidAndStops(t *testing.T) {
	dir := t.TempDir()
	ev := &events{}
	r := newProcess(procSvc("sleep 60", dir), ev.cb(), state.Files(filepath.Join(dir, "a"), "p"))
	if err := r.Start(); err != nil {
		t.Fatal(err)
	}
	if r.Pid() <= 0 || !platform.PidAlive(r.Pid()) {
		t.Fatalf("pid = %d", r.Pid())
	}
	r.Stop(3000)
	until(t, "the exit callback", ev.exited)
	until(t, "the pid to go", func() bool { return !platform.PidAlive(r.Pid()) })
}

func TestProcessRunnerKeepsTheExitCode(t *testing.T) {
	dir := t.TempDir()
	ev := &events{}
	files := state.Files(filepath.Join(dir, "b"), "p")
	r := newProcess(procSvc("echo hi; exit 3", dir), ev.cb(), files)
	if err := r.Start(); err != nil {
		t.Fatal(err)
	}
	until(t, "the exit", ev.exited)
	if ev.exit.code == nil || *ev.exit.code != 3 {
		t.Fatalf("exit = %+v", ev.exit)
	}
	data, _ := os.ReadFile(files.Exit)
	if strings.TrimSpace(string(data)) != "3" {
		t.Fatalf("exit file = %q", data)
	}
	until(t, "the output", func() bool {
		ev.mu.Lock()
		defer ev.mu.Unlock()
		for _, l := range ev.lines {
			if l == "hi" {
				return true
			}
		}
		return false
	})
}

func TestProcessRunnerRejectsAMissingWorkingDirectory(t *testing.T) {
	r := newProcess(procSvc("true", "/definitely/not/here"), (&events{}).cb(), state.Files(t.TempDir(), "p"))
	if err := r.Start(); err == nil || !strings.Contains(err.Error(), "working directory does not exist") {
		t.Fatalf("err = %v", err)
	}
}

func TestProcessRunnerPassesTheServiceEnvironment(t *testing.T) {
	dir := t.TempDir()
	ev := &events{}
	svc := procSvc(`echo "$GREETING"`, dir)
	svc.Env = map[string]string{"GREETING": "hola"}
	r := newProcess(svc, ev.cb(), state.Files(dir, "p"))
	r.Start()
	until(t, "the greeting", func() bool {
		ev.mu.Lock()
		defer ev.mu.Unlock()
		return len(ev.lines) > 0 && ev.lines[0] == "hola"
	})
}

func TestExternalRunnerDoesNothing(t *testing.T) {
	r := New(&config.ServiceConfig{Type: config.TypeExternal}, "p", Callbacks{}, state.ProcFiles{})
	ok, _ := r.Attach(nil)
	if r.Start() != nil || !ok || r.Stop(0) != nil {
		t.Fatal("an external runner must be a no-op")
	}
}
