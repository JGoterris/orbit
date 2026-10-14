//go:build !windows

package session

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/JGoterris/orbit/internal/config"
	"github.com/JGoterris/orbit/internal/core/supervisor"
	"github.com/JGoterris/orbit/internal/platform"
	"github.com/JGoterris/orbit/internal/projects"
	"github.com/JGoterris/orbit/internal/state"
)

func project(t *testing.T, name, services string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "orbit.yaml"), []byte("name: "+name+"\n"+services), 0o644)
	return dir
}

func open(t *testing.T, dir string) (*Session, *supervisor.Supervisor) {
	t.Helper()
	cfg, err := config.LoadConfig(config.LoadOptions{Dir: dir, Env: map[string]string{}})
	if err != nil {
		t.Fatal(err)
	}
	sup := supervisor.New(cfg, supervisor.Options{})
	if holder := state.AcquireLock(sup.StateDir()); holder != 0 {
		t.Fatalf("lock held by %d", holder)
	}
	sup.Init()
	return New(sup, Options{Version: "test"}), sup
}

func env(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
}

func alive(pid int) bool { return syscall.Kill(pid, 0) == nil }

func TestSwitchStopsTheOldServicesAndRegistersTheNewProject(t *testing.T) {
	env(t)
	a := project(t, "one", "services:\n  s:\n    cmd: sleep 30\n")
	b := project(t, "two", "services: {}\n")
	s, first := open(t, a)
	if !first.Start("s") {
		t.Fatal("s did not start")
	}
	pid := first.State("s").Pid

	next, relaunch, err := s.SwitchTo(b, "stop")
	if err != nil || relaunch != nil {
		t.Fatalf("switch: %v %v", err, relaunch)
	}
	if next != s.Sup() || s.Sup().Config().Name != "two" {
		t.Fatal("the session did not move to the new project")
	}
	if got := projects.Read(); len(got) != 1 || got[0].Path != b {
		t.Fatalf("registry = %+v", got)
	}
	deadline := time.Now().Add(3 * time.Second)
	for alive(pid) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if alive(pid) {
		t.Fatal("the old service is still running")
	}
	// the old project's lock is free, the new one is ours
	if holder := state.AcquireLock(first.StateDir()); holder != 0 {
		t.Fatalf("old lock held by %d", holder)
	}
	state.ReleaseLock(first.StateDir())
	s.Sup().Dispose()
	state.ReleaseLock(s.Sup().StateDir())
}

func TestDetachLeavesTheOldServicesRunningAndComingBackPicksThemUp(t *testing.T) {
	env(t)
	a := project(t, "keep", "services:\n  s:\n    cmd: sleep 30\n")
	b := project(t, "other", "services: {}\n")
	s, old := open(t, a)
	if !old.Start("s") {
		t.Fatal("s did not start")
	}
	pid := old.State("s").Pid
	if _, _, err := s.SwitchTo(b, "detach"); err != nil {
		t.Fatal(err)
	}
	defer syscall.Kill(-pid, syscall.SIGKILL)
	if !alive(pid) {
		t.Fatal("detach stopped the service")
	}
	if st := projects.StatusOf(projects.Entry{Path: a, Name: "keep"}); st.Running != 1 {
		t.Fatalf("status = %+v", st)
	}
	// ...and coming back picks it up again
	if _, _, err := s.SwitchTo(a, "stop"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for !s.Sup().IsUp("s") && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if !s.Sup().IsUp("s") {
		t.Fatal("the service was not picked up again")
	}
	found := false
	for _, l := range s.Sup().LogStore().Lines("s") {
		found = found || strings.Contains(l.Text, "re-attached to running process")
	}
	if !found {
		t.Fatal("no re-attach line in the log")
	}
	s.Sup().Dispose()
	state.ReleaseLock(s.Sup().StateDir())
}

func TestErrorsLeaveTheCurrentProjectUntouched(t *testing.T) {
	env(t)
	a := project(t, "stay", "services:\n  s:\n    cmd: sleep 30\n")
	s, sup := open(t, a)
	if !sup.Start("s") {
		t.Fatal("s did not start")
	}
	defer func() { sup.Dispose(); state.ReleaseLock(sup.StateDir()) }()

	check := func(dir, want string) {
		t.Helper()
		_, _, err := s.SwitchTo(dir, "stop")
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("SwitchTo(%s) = %v, want %q", dir, err, want)
		}
	}
	check(a, "already open")
	check(filepath.Join(t.TempDir(), "missing-dir"), "is not a folder")
	check(filepath.Join(a, "orbit.yaml"), "is not a folder")
	broken := t.TempDir()
	os.WriteFile(filepath.Join(broken, "orbit.yaml"), []byte("services: [oops"), 0o644)
	check(broken, "invalid YAML")

	// another live orbit has the target open: our parent is always alive
	taken := project(t, "taken", "services: {}\n")
	cfg, _ := config.LoadConfig(config.LoadOptions{Dir: taken})
	probe := supervisor.New(cfg, supervisor.Options{})
	os.MkdirAll(probe.StateDir(), 0o755)
	os.WriteFile(filepath.Join(probe.StateDir(), "orbit.lock"), []byte("1"), 0o644)
	if platform.PidAlive(1) {
		check(taken, "pid 1")
	}
	if s.Sup() != supervisorLike(sup) || !sup.IsUp("s") {
		t.Fatal("the current project changed")
	}
}

func supervisorLike(s *supervisor.Supervisor) supervisor.Like { return s }
