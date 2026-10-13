//go:build !windows

package console

import (
	"bytes"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/JGoterris/orbit/internal/config"
)

func svc(edit func(*config.ServiceConfig)) *config.ServiceConfig {
	s := &config.ServiceConfig{Name: "db", Type: config.TypeProcess, Cwd: "/tmp", Env: map[string]string{"A": "1"}}
	if edit != nil {
		edit(s)
	}
	return s
}

func TestCommandProcessUsesConsoleOrShell(t *testing.T) {
	t.Setenv("SHELL", "/bin/zsh")
	spec, err := Command(svc(func(s *config.ServiceConfig) { s.Console = "psql -U me" }), ServiceState{Status: "stopped"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(spec.Argv, []string{"/bin/sh", "-c", "psql -U me"}) || spec.Cwd != "/tmp" || spec.Title != "psql -U me" {
		t.Fatalf("spec = %+v", spec)
	}
	if spec.Env["A"] != "1" || spec.Env["ORBIT_SERVICE"] != "db" {
		t.Fatalf("env = %v", spec.Env)
	}
	shell, _ := Command(svc(nil), ServiceState{Status: "running"})
	if !reflect.DeepEqual(shell.Argv, []string{"/bin/zsh"}) {
		t.Fatalf("shell argv = %v", shell.Argv)
	}
}

func TestCommandExternalRunsOnTheHost(t *testing.T) {
	spec, err := Command(svc(func(s *config.ServiceConfig) { s.Type = config.TypeExternal; s.Console = "psql $DB" }), ServiceState{Status: "healthy"})
	if err != nil || !reflect.DeepEqual(spec.Argv, []string{"/bin/sh", "-c", "psql $DB"}) || spec.Cwd != "/tmp" {
		t.Fatalf("spec = %+v, err = %v", spec, err)
	}
}

func TestCommandContainersNeedToBeRunning(t *testing.T) {
	d := svc(func(s *config.ServiceConfig) { s.Type = config.TypeDocker; s.Console = "psql" })
	for _, st := range []ServiceState{{Status: "stopped"}, {Status: "running"}} {
		if _, err := Command(d, st); err == nil || err.Error() != "db is not running" {
			t.Errorf("state %+v: err = %v", st, err)
		}
	}
	spec, err := Command(d, ServiceState{Status: "healthy", ContainerID: "abc"})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"docker", "exec", "-it", "abc", "sh", "-c", "psql"}; !reflect.DeepEqual(spec.Argv, want) {
		t.Fatalf("argv = %v", spec.Argv)
	}
}

func TestCommandMissingRequiredEnvFile(t *testing.T) {
	s := svc(func(s *config.ServiceConfig) {
		s.EnvFiles = []config.EnvFileRef{{Path: "/no/such.env", Required: true}}
	})
	if _, err := Command(s, ServiceState{}); err == nil || !strings.Contains(err.Error(), "env_file not found") {
		t.Fatalf("err = %v", err)
	}
}

func waitDone(t *testing.T, s *Session) {
	t.Helper()
	select {
	case <-s.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("the session did not end")
	}
}

func TestSessionTalksThroughAPTYAndKeepsABacklog(t *testing.T) {
	s, err := NewSession(Spec{Argv: []string{"/bin/sh", "-c", "read x; echo got:$x"}, Env: map[string]string{}, Title: "t"}, 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	var seen bytes.Buffer
	var mu = make(chan struct{}, 1)
	mu <- struct{}{}
	s.Subscribe(func(b []byte) { <-mu; seen.Write(b); mu <- struct{}{} })
	// the program may not be reading yet when the first bytes arrive: keep typing until it answers
	stop := time.NewTicker(50 * time.Millisecond)
	defer stop.Stop()
	go func() {
		for range stop.C {
			s.Write([]byte("hi\r"))
		}
	}()
	waitDone(t, s)
	if s.ExitCode() != 0 || s.Alive() {
		t.Fatalf("exit = %d, alive = %v", s.ExitCode(), s.Alive())
	}
	<-mu
	got := seen.String()
	mu <- struct{}{}
	if !strings.Contains(got, "got:hi") || !strings.Contains(string(s.Backlog()), "got:hi") {
		t.Fatalf("subscriber saw %q, backlog %q", got, s.Backlog())
	}
}

func TestSessionReportsExitCode(t *testing.T) {
	s, err := NewSession(Spec{Argv: []string{"/bin/sh", "-c", "exit 7"}}, 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	waitDone(t, s)
	if s.ExitCode() != 7 {
		t.Fatalf("exit = %d", s.ExitCode())
	}
}

func TestSessionHasAControllingTerminal(t *testing.T) {
	// an interactive shell needs one for job control: `tty` fails without it, and stty sees our size
	s, err := NewSession(Spec{Argv: []string{"/bin/sh", "-c", "tty >/dev/null && stty size"}}, 100, 30)
	if err != nil {
		t.Fatal(err)
	}
	waitDone(t, s)
	if s.ExitCode() != 0 || !strings.Contains(string(s.Backlog()), "30 100") {
		t.Fatalf("exit = %d, output = %q", s.ExitCode(), s.Backlog())
	}
}

func TestManagerReusesLiveSessionsAndKillEndsThem(t *testing.T) {
	m := NewManager()
	spec := Spec{Argv: []string{"/bin/sh", "-c", "sleep 30"}, Env: map[string]string{}, Title: "t"}
	a, err := m.Open("x", spec, 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := m.Open("x", spec, 80, 24)
	if a != b || !m.Has("x") {
		t.Fatal("a live session must be reused")
	}
	m.CloseAll()
	waitDone(t, a)
	if m.Has("x") || a.Alive() {
		t.Fatal("CloseAll must end the session")
	}
}

func TestMain(m *testing.M) { os.Exit(m.Run()) }
