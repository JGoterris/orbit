//go:build !windows

package supervisor

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/JGoterris/orbit/internal/config"
	"github.com/JGoterris/orbit/internal/core/logs"
	"github.com/JGoterris/orbit/internal/state"
)

func svc(name, cmd string, edit ...func(*config.ServiceConfig)) *config.ServiceConfig {
	cwd, _ := os.Getwd()
	s := &config.ServiceConfig{
		Name: name, Type: config.TypeProcess, Cmd: cmd, Cwd: cwd, Env: map[string]string{}, EnvFiles: []config.EnvFileRef{},
		DependsOn: []string{}, Restart: config.RestartNo, StartTimeout: 5000, StopTimeout: 1000, Autostart: true,
		LeakDetection: true, Ports: []string{}, Volumes: []string{}, DockerArgs: []string{},
	}
	for _, e := range edit {
		e(s)
	}
	return s
}

func cfgOf(svcs ...*config.ServiceConfig) *config.OrbitConfig {
	cwd, _ := os.Getwd()
	c := &config.OrbitConfig{Name: "t", Root: cwd, Services: map[string]*config.ServiceConfig{}, Groups: map[string][]string{}}
	for _, s := range svcs {
		c.Services[s.Name] = s
		c.Order = append(c.Order, s.Name)
	}
	return c
}

func newSup(t *testing.T, c *config.OrbitConfig) *Supervisor {
	t.Helper()
	s := New(c, Options{StateDir: t.TempDir()})
	t.Cleanup(func() { s.Dispose() })
	return s
}

func eventually(t *testing.T, what string, d time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func hasLine(s *Supervisor, service string, pred func(logs.Line) bool) bool {
	for _, l := range s.Logs.Lines(service) {
		if pred(l) {
			return true
		}
	}
	return false
}

func lineText(text string) func(logs.Line) bool {
	return func(l logs.Line) bool { return l.Text == text }
}

func pidGone(pid int) bool { return syscall.Kill(-pid, 0) != nil }

func TestMemoryOverItsLimitIsFlagged(t *testing.T) {
	s := newSup(t, cfgOf(svc("hog", "sleep 30", func(c *config.ServiceConfig) { c.MemLimit = 1024 }), svc("calm", "sleep 30")))
	s.Init()
	s.StartMany([]string{"hog", "calm"})
	eventually(t, "the memory level", 15*time.Second, func() bool {
		r := s.State("hog").Resources
		return r != nil && r.Level == "over"
	})
	if r := s.State("hog").Resources; r.MemLimit != 1024 {
		t.Errorf("limit = %d", r.MemLimit)
	}
	if s.State("calm").Resources != nil {
		t.Error("a service without a limit has nothing to flag")
	}
	buckets, err := s.HistoryOf("hog", 0)
	if err != nil || len(buckets) == 0 || buckets[0].Mem <= 0 {
		t.Fatalf("history = %v, %v", buckets, err)
	}
	s.Stop("hog", StopOptions{})
	if s.State("hog").Resources != nil {
		t.Error("resources must clear when the service stops")
	}
	if _, err := s.HistoryOf("nope", 0); err == nil || !strings.Contains(err.Error(), "unknown service") {
		t.Errorf("history of unknown = %v", err)
	}
}

func TestDependenciesStartFirstAndStopLast(t *testing.T) {
	s := newSup(t, cfgOf(
		svc("db", "echo db-up; sleep 30"),
		svc("api", "echo api-up; sleep 30", func(c *config.ServiceConfig) { c.DependsOn = []string{"db"} }),
	))
	var order []string
	s.OnChange(func(n string) {
		if n != "" && s.State(n).Status == Running {
			for _, o := range order {
				if o == n {
					return
				}
			}
			order = append(order, n)
		}
	})
	if !s.Start("api") {
		t.Fatal("api did not start")
	}
	if strings.Join(order, ",") != "db,api" {
		t.Fatalf("order = %v", order)
	}
	eventually(t, "api's output", 3*time.Second, func() bool { return hasLine(s, "api", lineText("api-up")) })
	s.Stop("db", StopOptions{})
	if s.State("api").Status != Stopped || s.State("db").Status != Stopped {
		t.Fatalf("api = %s, db = %s", s.State("api").Status, s.State("db").Status)
	}
}

func TestHealthChecksGateReadiness(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) < 4 {
			w.WriteHeader(503)
			return
		}
		w.Write([]byte("ok"))
	}))
	defer srv.Close()
	s := newSup(t, cfgOf(svc("srv", "sleep 30", func(c *config.ServiceConfig) {
		c.Health = &config.HealthCheck{Http: srv.URL, Interval: 100, Timeout: 500}
	})))
	if !s.Start("srv") {
		t.Fatal("srv did not become ready")
	}
	if st := s.State("srv").Status; st != Healthy {
		t.Fatalf("status = %s", st)
	}
	if calls.Load() < 4 {
		t.Fatalf("it was ready after %d checks, before the 503s ended", calls.Load())
	}
}

func TestReadyWhenLogGatesDependents(t *testing.T) {
	for _, redirect := range []string{"", " >&2"} {
		t.Run("redirect"+redirect, func(t *testing.T) {
			s := newSup(t, cfgOf(
				svc("worker", "sleep 0.5; echo Listening on 1"+redirect+"; sleep 30", func(c *config.ServiceConfig) {
					c.ReadyWhen = &config.ReadyWhen{Log: "Listening on"}
				}),
				svc("app", "sleep 30", func(c *config.ServiceConfig) { c.DependsOn = []string{"worker"} }),
			))
			var events []string
			s.Logs.OnLine(func(l logs.Line) {
				if l.Text == "Listening on 1" {
					events = append(events, "line")
				}
			})
			s.OnChange(func(n string) {
				if n == "app" && s.State("app").Status == Starting {
					events = append(events, "app")
				}
			})
			if !s.Start("app") {
				t.Fatal("app did not start")
			}
			if len(events) == 0 || events[0] != "line" {
				t.Fatalf("events = %v", events)
			}
			if s.State("worker").Status != Running {
				t.Fatalf("worker = %s", s.State("worker").Status)
			}
		})
	}
}

func TestReadyWhenNeverPrinted(t *testing.T) {
	s := newSup(t, cfgOf(
		svc("worker", "sleep 30", func(c *config.ServiceConfig) {
			c.ReadyWhen = &config.ReadyWhen{Log: "never"}
			c.StartTimeout = 800
		}),
		svc("app", "sleep 30", func(c *config.ServiceConfig) { c.DependsOn = []string{"worker"} }),
	))
	if s.Start("app") {
		t.Fatal("app must not start")
	}
	if s.State("worker").Status != Unhealthy || s.State("app").Status != Failed {
		t.Fatalf("worker = %s, app = %s", s.State("worker").Status, s.State("app").Status)
	}
}

func TestReadyWhenPlusHealthBothMustPass(t *testing.T) {
	s := newSup(t, cfgOf(svc("w", "sleep 1; echo ready-now; sleep 30", func(c *config.ServiceConfig) {
		c.ReadyWhen = &config.ReadyWhen{Log: "/READY/i"}
		c.Health = &config.HealthCheck{Cmd: "true", Interval: 100, Timeout: 500}
	})))
	started := time.Now()
	if !s.Start("w") {
		t.Fatal("w did not start")
	}
	if time.Since(started) < 900*time.Millisecond {
		t.Fatalf("ready after %v, before its log line", time.Since(started))
	}
	if s.State("w").Status != Healthy {
		t.Fatalf("status = %s", s.State("w").Status)
	}
}

func TestCrashAndRestartOnFailure(t *testing.T) {
	s := newSup(t, cfgOf(
		svc("bad", "echo boom >&2; exit 3", func(c *config.ServiceConfig) {
			c.Restart = config.RestartOnFailure
			c.Health = &config.HealthCheck{Cmd: "false", Interval: 100, Timeout: 100}
		}),
		svc("app", "sleep 30", func(c *config.ServiceConfig) { c.DependsOn = []string{"bad"} }),
	))
	if s.Start("app") {
		t.Fatal("app must not start")
	}
	if s.State("app").Status != Failed || s.State("bad").Status != Crashed {
		t.Fatalf("app = %s, bad = %s", s.State("app").Status, s.State("bad").Status)
	}
	if !hasLine(s, "bad", func(l logs.Line) bool { return l.Stream == logs.Stderr && l.Text == "boom" }) {
		t.Fatal("stderr line missing")
	}
	if code := s.State("bad").ExitCode; code == nil || *code != 3 {
		t.Fatalf("exit code = %v", code)
	}
	eventually(t, "a restart", 5*time.Second, func() bool { return s.State("bad").Restarts >= 1 })
	s.Dispose()
	if st := s.State("bad").Status; st != Stopped {
		t.Fatalf("after dispose = %s", st)
	}
}

func TestKillsTheWholeProcessTree(t *testing.T) {
	s := newSup(t, cfgOf(svc("tree", "sleep 60 & sleep 60 & wait")))
	s.Start("tree")
	pid := s.State("tree").Pid
	time.Sleep(200 * time.Millisecond)
	s.Stop("tree", StopOptions{})
	if !pidGone(pid) {
		t.Fatal("process group still alive")
	}
}

func TestEnvFilesAreReReadOnEveryStart(t *testing.T) {
	dir := t.TempDir()
	envPath := filepath.Join(dir, ".env.local")
	os.WriteFile(envPath, []byte("GREETING=hola\nWHO=file"), 0o644)
	s := newSup(t, cfgOf(svc("echo", `echo "$GREETING $WHO"; sleep 30`, func(c *config.ServiceConfig) {
		c.Cwd = dir
		c.Env = map[string]string{"WHO": "inline"}
		c.EnvFiles = []config.EnvFileRef{{Path: envPath, Required: true}}
	})))
	s.Start("echo")
	eventually(t, "first output", 3*time.Second, func() bool { return hasLine(s, "echo", lineText("hola inline")) })
	os.WriteFile(envPath, []byte("GREETING=adios"), 0o644)
	s.Restart("echo")
	eventually(t, "second output", 3*time.Second, func() bool { return hasLine(s, "echo", lineText("adios inline")) })
	s.Stop("echo", StopOptions{})
	os.Remove(envPath)
	if s.Start("echo") {
		t.Fatal("start must fail without its env file")
	}
	if e := s.State("echo").Error; !strings.Contains(e, "env_file not found") {
		t.Fatalf("error = %q", e)
	}
}

func TestOneshotTasksGateDependentsOnSuccess(t *testing.T) {
	s := newSup(t, cfgOf(
		svc("build", "sleep 0.2; echo built", func(c *config.ServiceConfig) { c.Oneshot = true; c.Restart = config.RestartAlways }),
		svc("app", "echo app-up; sleep 30", func(c *config.ServiceConfig) { c.DependsOn = []string{"build"} }),
		svc("broken", "exit 2", func(c *config.ServiceConfig) { c.Oneshot = true }),
		svc("needs-broken", "sleep 30", func(c *config.ServiceConfig) { c.DependsOn = []string{"broken"} }),
	))
	if !s.Start("app") {
		t.Fatal("app did not start")
	}
	if s.State("build").Status != Exited || !s.IsReady("build") {
		t.Fatalf("build = %s", s.State("build").Status)
	}
	// already done: starting another dependent does not re-run it
	s.Stop("app", StopOptions{})
	s.Start("app")
	n := 0
	for _, l := range s.Logs.Lines("build") {
		if l.Text == "built" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("build ran %d times", n)
	}
	time.Sleep(1200 * time.Millisecond)
	if r := s.State("build").Restarts; r != 0 {
		t.Fatalf("restart: always looped a finished task (%d)", r)
	}
	if s.Start("needs-broken") {
		t.Fatal("needs-broken must not start")
	}
	if s.State("broken").Status != Crashed {
		t.Fatalf("broken = %s", s.State("broken").Status)
	}
}

func hookList(cmds ...string) []config.Hook {
	out := make([]config.Hook, len(cmds))
	for i, c := range cmds {
		out[i] = config.Hook{Cmd: c, Timeout: 5000}
	}
	return out
}

func TestPreStartRunsBeforeCmd(t *testing.T) {
	m := filepath.Join(t.TempDir(), "m")
	s := newSup(t, cfgOf(svc("a", "cat "+m+"; sleep 30", func(c *config.ServiceConfig) {
		c.Hooks = &config.Hooks{PreStart: hookList("echo prepared | tee " + m)}
	})))
	if !s.Start("a") {
		t.Fatal("a did not start")
	}
	eventually(t, "both outputs", 3*time.Second, func() bool {
		n := 0
		for _, l := range s.Logs.Lines("a") {
			if l.Text == "prepared" {
				n++
			}
		}
		return n == 2
	})
	if !hasLine(s, "a", func(l logs.Line) bool { return strings.HasPrefix(l.Text, "✓ pre_start") }) {
		t.Error("no success line for the hook")
	}
}

func TestFailingPreStartFailsTheServiceAndItsDependents(t *testing.T) {
	m := filepath.Join(t.TempDir(), "m")
	s := newSup(t, cfgOf(
		svc("a", "touch "+m+"; sleep 30", func(c *config.ServiceConfig) { c.Hooks = &config.Hooks{PreStart: hookList("exit 1")} }),
		svc("b", "sleep 30", func(c *config.ServiceConfig) { c.DependsOn = []string{"a"} }),
	))
	if s.Start("b") {
		t.Fatal("b must not start")
	}
	if st := s.State("a"); st.Status != Failed || !strings.Contains(st.Error, "pre_start failed") {
		t.Fatalf("a = %+v", st)
	}
	if s.State("b").Status != Failed {
		t.Fatalf("b = %s", s.State("b").Status)
	}
	if _, err := os.Stat(m); err == nil {
		t.Fatal("cmd ran despite the failed pre_start")
	}
}

func TestFailingPostStartKeepsTheServiceUp(t *testing.T) {
	s := newSup(t, cfgOf(svc("a", "sleep 30", func(c *config.ServiceConfig) {
		c.Hooks = &config.Hooks{PostStart: hookList("exit 4")}
	})))
	if !s.Start("a") {
		t.Fatal("a did not start")
	}
	eventually(t, "the post_start error", 3*time.Second, func() bool { return strings.Contains(s.State("a").Error, "post_start failed") })
	if s.State("a").Status != Running {
		t.Fatalf("status = %s", s.State("a").Status)
	}
}

func TestPostStopRunsOnStopAndAfterACrash(t *testing.T) {
	m := filepath.Join(t.TempDir(), "m")
	s := newSup(t, cfgOf(
		svc("stopped", "sleep 30", func(c *config.ServiceConfig) {
			c.Hooks = &config.Hooks{PostStop: hookList("echo $ORBIT_SERVICE > " + m)}
		}),
		svc("crashy", "sleep 0.2; exit 3", func(c *config.ServiceConfig) {
			c.Hooks = &config.Hooks{PostStop: hookList("echo code=$ORBIT_EXIT_CODE")}
		}),
	))
	s.Start("stopped")
	s.Stop("stopped", StopOptions{})
	data, _ := os.ReadFile(m)
	if strings.TrimSpace(string(data)) != "stopped" {
		t.Fatalf("marker = %q", data)
	}
	s.Start("crashy")
	eventually(t, "the crash and its post_stop", 5*time.Second, func() bool {
		return s.State("crashy").Status == Crashed && hasLine(s, "crashy", lineText("code=3"))
	})
}

func TestStoppingDuringALongPreStartKillsIt(t *testing.T) {
	s := newSup(t, cfgOf(svc("a", "sleep 30", func(c *config.ServiceConfig) {
		c.Hooks = &config.Hooks{PreStart: []config.Hook{{Cmd: "sleep 30", Timeout: 60_000}}}
	})))
	done := make(chan bool)
	go func() { done <- s.Start("a") }()
	time.Sleep(300 * time.Millisecond)
	s.Stop("a", StopOptions{})
	select {
	case ok := <-done:
		if ok {
			t.Fatal("start must report false")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("start did not return after stop")
	}
	if s.State("a").Status != Stopped {
		t.Fatalf("status = %s", s.State("a").Status)
	}
}

func TestDetachLeavesProcessesRunningAndANewSessionReattaches(t *testing.T) {
	dir := t.TempDir()
	mk := func() *Supervisor {
		return New(cfgOf(svc("keep", "echo hi; echo oops >&2; sleep 30")), Options{StateDir: dir})
	}
	first := mk()
	first.Start("keep")
	pid := first.State("keep").Pid
	time.Sleep(300 * time.Millisecond)
	first.Detach()
	first.KillAllSync()
	if pidGone(pid) {
		t.Fatal("detach killed the process")
	}

	second := mk()
	t.Cleanup(func() { second.Dispose() })
	second.Init()
	if st := second.State("keep"); st.Status != Running || st.Pid != pid {
		t.Fatalf("second = %+v (pid was %d)", st, pid)
	}
	eventually(t, "the replayed output", 3*time.Second, func() bool {
		return hasLine(second, "keep", func(l logs.Line) bool { return l.Stream == logs.Stdout && l.Text == "hi" }) &&
			hasLine(second, "keep", func(l logs.Line) bool { return l.Stream == logs.Stderr && l.Text == "oops" })
	})
	second.Stop("keep", StopOptions{})
	if second.State("keep").Status != Stopped || !pidGone(pid) {
		t.Fatal("stop did not end the re-attached process")
	}
	second.Dispose()
	if n := len(state.Read(dir).Services); n != 0 {
		t.Fatalf("state still lists %d services", n)
	}
}

func TestAdoptedProcessThatExitsReportsItsCode(t *testing.T) {
	dir := t.TempDir()
	mk := func() *Supervisor {
		return New(cfgOf(svc("job", "sleep 1.5; exit 3")), Options{StateDir: dir})
	}
	first := mk()
	first.Start("job")
	first.Detach()
	second := mk()
	t.Cleanup(func() { second.Dispose() })
	second.Init()
	if second.State("job").Status != Running {
		t.Fatalf("status = %s", second.State("job").Status)
	}
	eventually(t, "the exit", 8*time.Second, func() bool { return second.State("job").Status == Crashed })
	if code := second.State("job").ExitCode; code == nil || *code != 3 {
		t.Fatalf("exit code = %v", code)
	}
}

func TestRecycledPidIsNotAdopted(t *testing.T) {
	if !strings.Contains(fmt.Sprint(os.Getenv("GOOS")), "") {
		t.Skip()
	}
	dir := t.TempDir()
	mk := func() *Supervisor { return New(cfgOf(svc("keep", "sleep 30")), Options{StateDir: dir}) }
	first := mk()
	first.Start("keep")
	pid := first.State("keep").Pid
	first.Detach()
	saved := state.Read(dir)
	e := saved.Services["keep"]
	if e.StartTime == 0 {
		syscall.Kill(-pid, syscall.SIGKILL)
		t.Skip("the platform cannot tell a start time")
	}
	e.StartTime++
	saved.Services["keep"] = e
	state.Write(dir, saved)
	second := mk()
	t.Cleanup(func() { second.Dispose() })
	second.Init()
	if st := second.State("keep").Status; st != Stopped {
		t.Fatalf("status = %s", st)
	}
	syscall.Kill(-pid, syscall.SIGKILL)
}

// ---------------------------------------------------------------- external services

func external(name, url string, edit ...func(*config.ServiceConfig)) *config.ServiceConfig {
	return svc(name, "", append([]func(*config.ServiceConfig){func(c *config.ServiceConfig) {
		c.Type, c.Cmd = config.TypeExternal, ""
		c.Health = &config.HealthCheck{Http: url, Interval: 100, Timeout: 300}
		c.StartTimeout = 800
	}}, edit...)...)
}

func okServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) }))
}

func TestExternalIsMonitoredFromInit(t *testing.T) {
	srv := okServer()
	s := newSup(t, cfgOf(external("saas", srv.URL)))
	s.Init()
	eventually(t, "healthy", 5*time.Second, func() bool { return s.State("saas").Status == Healthy })
	if s.OwnedRunningCount() != 0 {
		t.Error("quitting orbit has nothing to stop")
	}
	srv.Close()
	eventually(t, "unhealthy", 8*time.Second, func() bool { return s.State("saas").Status == Unhealthy })
}

func TestExternalDependentWaitsForIt(t *testing.T) {
	// a port nothing listens on
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := l.Addr().String()
	l.Close()
	s := newSup(t, cfgOf(external("saas", "http://"+addr+"/"), svc("api", "sleep 30", func(c *config.ServiceConfig) {
		c.DependsOn = []string{"saas"}
	})))
	s.Init()
	if s.Start("api") {
		t.Fatal("api must not start while its dependency is unreachable")
	}
	if st := s.State("api"); st.Status != Failed || !strings.Contains(st.Error, "dependency not ready: saas") {
		t.Fatalf("api = %+v", st)
	}
	lst, err := net.Listen("tcp", addr)
	if err != nil {
		t.Skip("the port was taken meanwhile")
	}
	srv := &httptest.Server{Listener: lst, Config: &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })}}
	srv.Start()
	defer srv.Close()
	if !s.Start("api") {
		t.Fatal("api must start once saas answers")
	}
	if s.State("saas").Status != Healthy {
		t.Fatalf("saas = %s", s.State("saas").Status)
	}
}

func TestStopDoesNothingToExternalNorItsDependents(t *testing.T) {
	srv := okServer()
	defer srv.Close()
	s := newSup(t, cfgOf(external("saas", srv.URL), svc("api", "sleep 30", func(c *config.ServiceConfig) {
		c.DependsOn = []string{"saas"}
	})))
	if !s.Start("api") { // works without Init
		t.Fatal("api did not start")
	}
	s.Stop("saas", StopOptions{})
	if s.State("saas").Status != Healthy || s.State("api").Status != Running {
		t.Fatalf("saas = %s, api = %s", s.State("saas").Status, s.State("api").Status)
	}
	s.Stop("api", StopOptions{})
	if s.State("api").Status != Stopped {
		t.Fatalf("api = %s", s.State("api").Status)
	}
}

// ---------------------------------------------------------------- watch and reload

func TestWatchRestartsARunningServiceButNeverStartsAStoppedOne(t *testing.T) {
	dir := t.TempDir()
	w := &config.WatchConfig{Paths: []string{"*.go"}, Ignore: []string{}, Debounce: 40, Cooldown: 100}
	s := newSup(t, cfgOf(svc("api", "sleep 30", func(c *config.ServiceConfig) { c.Cwd = dir; c.Watch = w })))
	s.Init()
	os.WriteFile(filepath.Join(dir, "a.go"), []byte("1"), 0o644)
	time.Sleep(400 * time.Millisecond)
	if s.State("api").Status != Stopped {
		t.Fatal("a change must not start a stopped service")
	}
	s.Start("api")
	pid := s.State("api").Pid
	time.Sleep(150 * time.Millisecond)
	os.WriteFile(filepath.Join(dir, "b.go"), []byte("1"), 0o644)
	eventually(t, "the restart", 5*time.Second, func() bool {
		st := s.State("api")
		return st.Status == Running && st.Pid != pid && st.Pid != 0
	})
	if s.State("api").Restarts != 0 {
		t.Error("a watch restart is not a crash restart")
	}
	if !hasLine(s, "api", func(l logs.Line) bool { return strings.HasPrefix(l.Text, "↻ b.go changed") }) {
		t.Error("no restart line in the log")
	}
}

func TestToggleWatchPausesRestarts(t *testing.T) {
	dir := t.TempDir()
	w := &config.WatchConfig{Paths: []string{"*.go"}, Ignore: []string{}, Debounce: 40, Cooldown: 100}
	s := newSup(t, cfgOf(svc("api", "sleep 30", func(c *config.ServiceConfig) { c.Cwd = dir; c.Watch = w })))
	s.Init()
	s.Start("api")
	pid := s.State("api").Pid
	if got := s.ToggleWatch("api"); got != "paused" {
		t.Fatalf("toggle = %q", got)
	}
	if s.State("api").Watch != "paused" {
		t.Fatalf("watch = %q", s.State("api").Watch)
	}
	os.WriteFile(filepath.Join(dir, "c.go"), []byte("1"), 0o644)
	time.Sleep(400 * time.Millisecond)
	if s.State("api").Pid != pid {
		t.Fatal("a paused watcher restarted the service")
	}
	if got := s.ToggleWatch("api"); got != "active" {
		t.Fatalf("toggle = %q", got)
	}
}

func TestReloadRestartsOnlyWhatChanged(t *testing.T) {
	s := newSup(t, cfgOf(
		svc("db", "sleep 30"),
		svc("api", "sleep 30", func(c *config.ServiceConfig) { c.DependsOn = []string{"db"} }),
		svc("old", "sleep 30"),
	))
	s.Init()
	s.StartAll()
	dbPid, apiPid := s.State("db").Pid, s.State("api").Pid

	diff, err := s.Reload(cfgOf(
		svc("db", "sleep 31"), // restart
		svc("api", "sleep 30", func(c *config.ServiceConfig) { c.DependsOn = []string{"db"}; c.Description = "hot" }), // hot
		svc("fresh", "sleep 30"),
		svc("lazy", "sleep 30", func(c *config.ServiceConfig) { c.Autostart = false }),
	))
	if err != nil {
		t.Fatal(err)
	}
	if len(diff.Removed) != 1 || diff.Removed[0] != "old" || len(diff.Added) != 2 {
		t.Fatalf("diff = %+v", diff)
	}
	eventually(t, "db and fresh running", 8*time.Second, func() bool {
		return s.State("db").Status == Running && s.State("fresh").Status == Running
	})
	if got := strings.Join(s.Names(), ","); got != "db,api,fresh,lazy" {
		t.Fatalf("names = %s", got)
	}
	if s.State("db").Pid == dbPid {
		t.Error("db should have restarted")
	}
	if s.State("api").Pid != apiPid || s.State("api").Status != Running {
		t.Error("api must keep running")
	}
	if s.Service("api").Description != "hot" {
		t.Error("the hot change was not applied")
	}
	if s.State("lazy").Status != Stopped {
		t.Error("lazy has autostart: false")
	}
	order := s.Order()
	idx := func(n string) int {
		for i, o := range order {
			if o == n {
				return i
			}
		}
		return -1
	}
	if idx("db") > idx("api") {
		t.Errorf("order = %v", order)
	}
}

func TestAChangeOnDiskIsOfferedAsPendingAndReloadAppliesIt(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "orbit.yaml")
	write := func(cmd string) { os.WriteFile(file, []byte("services:\n  a:\n    cmd: "+cmd+"\n"), 0o644) }
	write("sleep 30")
	c, err := config.LoadConfig(config.LoadOptions{File: file, Env: map[string]string{}})
	if err != nil {
		t.Fatal(err)
	}
	s := newSup(t, c)
	s.Init()

	write("sleep 31")
	eventually(t, "a pending diff", 6*time.Second, func() bool { p := s.Pending(); return p != nil && p.Diff != nil })
	if ch := s.Pending().Diff.Changed; len(ch) != 1 || ch[0].Name != "a" || ch[0].Fields[0] != "cmd" {
		t.Fatalf("pending = %+v", s.Pending().Diff)
	}
	os.WriteFile(file, []byte("services: [oops"), 0o644)
	eventually(t, "a pending error", 6*time.Second, func() bool { p := s.Pending(); return p != nil && p.Error != "" })
	if !strings.Contains(s.Pending().Error, "invalid YAML") {
		t.Fatalf("error = %q", s.Pending().Error)
	}
	write("sleep 31")
	eventually(t, "a pending diff again", 6*time.Second, func() bool { p := s.Pending(); return p != nil && p.Diff != nil })
	diff, err := s.Reload(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(diff.Changed) != 1 || s.Pending() != nil {
		t.Fatalf("diff = %+v, pending = %v", diff, s.Pending())
	}
	if s.Service("a").Cmd != "sleep 31" {
		t.Fatalf("cmd = %q", s.Service("a").Cmd)
	}
}
