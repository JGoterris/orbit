//go:build !windows

package ipc

import (
	"bufio"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/JGoterris/orbit/internal/config"
	"github.com/JGoterris/orbit/internal/core/logs"
	"github.com/JGoterris/orbit/internal/core/supervisor"
	"github.com/JGoterris/orbit/internal/state"
)

// TestMain lets the test binary stand in for `orbit daemon`, which the daemon tests start as a child process.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "daemon" {
		var opts config.LoadOptions
		if len(os.Args) > 3 && os.Args[2] == "-c" {
			opts.File = os.Args[3]
		} else if len(os.Args) > 2 {
			opts.Dir = os.Args[2]
		}
		cfg, err := config.LoadConfig(opts)
		if err != nil {
			os.Stderr.WriteString(err.Error() + "\n")
			os.Exit(1)
		}
		os.Exit(RunDaemon(cfg, DaemonOptions{Version: "test"}))
	}
	os.Exit(m.Run())
}

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

func cfgOf(name string, svcs ...*config.ServiceConfig) *config.OrbitConfig {
	cwd, _ := os.Getwd()
	c := &config.OrbitConfig{Name: name, Root: cwd, Services: map[string]*config.ServiceConfig{}, Groups: map[string][]string{}}
	var all []string
	for _, s := range svcs {
		c.Services[s.Name] = s
		c.Order = append(c.Order, s.Name)
		all = append(all, s.Name)
	}
	c.Groups["all"] = all
	c.GroupOrder = []string{"all"}
	return c
}

type env struct {
	sup      *supervisor.Supervisor
	server   *Server
	client   *Client
	mu       sync.Mutex
	shutdown string
}

func (e *env) shutdownHow() string { e.mu.Lock(); defer e.mu.Unlock(); return e.shutdown }

func setup(t *testing.T, name string, svcs ...*config.ServiceConfig) *env {
	t.Helper()
	e := &env{}
	e.sup = supervisor.New(cfgOf(name, svcs...), supervisor.Options{StateDir: t.TempDir()})
	e.server = NewServer(e.sup, ServerOptions{Version: "test", OnShutdown: func(how string) {
		e.mu.Lock()
		e.shutdown = how
		e.mu.Unlock()
	}})
	started, err := e.server.Start()
	if err != nil || !started {
		t.Fatalf("server did not start: %v %v", started, err)
	}
	c, err := Connect(e.server.Path(), 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	e.client = c
	t.Cleanup(func() {
		c.Close()
		e.server.Close()
		e.sup.Dispose()
	})
	return e
}

func until(t *testing.T, what string, cond func() bool) {
	t.Helper()
	end := time.Now().Add(8 * time.Second)
	for time.Now().Before(end) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestHelloAndSnapshot(t *testing.T) {
	e := setup(t, "hello", svc("a", "sleep 30"))
	var hello Hello
	if err := e.client.Call(MethodHello, nil, &hello); err != nil {
		t.Fatal(err)
	}
	if hello.Protocol != Protocol || hello.Pid != os.Getpid() || len(hello.Config.Order) != 1 || hello.Config.Order[0] != "a" {
		t.Fatalf("hello = %+v", hello)
	}
	var snap []supervisor.ServiceState
	e.client.Call(MethodSnapshot, nil, &snap)
	if len(snap) != 1 || snap[0].Name != "a" || snap[0].Status != supervisor.Stopped {
		t.Fatalf("snapshot = %+v", snap)
	}
}

func TestCommandsDriveTheSupervisorAndGroupsExpand(t *testing.T) {
	e := setup(t, "cmds", svc("a", "sleep 30"), svc("b", "sleep 30"))
	var res struct{ OK bool }
	if err := e.client.Call(MethodStart, map[string]any{"services": []string{"all"}}, &res); err != nil || !res.OK {
		t.Fatalf("start: %v %+v", err, res)
	}
	if e.sup.State("a").Status != supervisor.Running || e.sup.State("b").Status != supervisor.Running {
		t.Fatal("services are not running")
	}
	e.client.Call(MethodStop, map[string]any{"services": []string{"a"}}, nil)
	if e.sup.State("a").Status != supervisor.Stopped || e.sup.State("b").Status != supervisor.Running {
		t.Fatal("only a should have stopped")
	}
	e.client.Call(MethodStopAll, nil, nil)
	if e.sup.State("b").Status != supervisor.Stopped {
		t.Fatal("stopAll left b running")
	}
}

func TestHistoryOverTheSocket(t *testing.T) {
	e := setup(t, "buckets", svc("a", "sleep 30"))
	var b []map[string]float64
	if err := e.client.Call(MethodHistory, map[string]any{"service": "a"}, &b); err != nil || len(b) != 0 {
		t.Fatalf("empty history = %v, %v", b, err)
	}
	err := e.client.Call(MethodHistory, map[string]any{"service": "nope"}, nil)
	if err == nil || !strings.Contains(err.Error(), `unknown service "nope"`) {
		t.Fatalf("err = %v", err)
	}
}

func TestErrorsForUnknownServicesAndMethods(t *testing.T) {
	e := setup(t, "errs", svc("a", "sleep 30"))
	cases := []struct {
		method string
		params any
		want   string
	}{
		{MethodStart, map[string]any{"services": []string{"nope"}}, `unknown service or group "nope"`},
		{MethodStart, map[string]any{}, "list of names"},
		{"bogus", nil, "unknown method"},
	}
	for _, c := range cases {
		err := e.client.Call(c.method, c.params, nil)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want %q", c.method, err, c.want)
		}
	}
}

func TestSubscribersReceiveStateChangesAndLogLines(t *testing.T) {
	e := setup(t, "subs", svc("a", "echo hello-ipc; sleep 30"))
	var mu sync.Mutex
	var states []supervisor.Status
	var texts []string
	e.client.OnNotification(func(m Message) {
		mu.Lock()
		defer mu.Unlock()
		switch m.Method {
		case NotifyState:
			var p struct{ State supervisor.ServiceState }
			json.Unmarshal(m.Params, &p)
			states = append(states, p.State.Status)
		case NotifyLog:
			var l logs.Line
			json.Unmarshal(m.Params, &l)
			texts = append(texts, l.Text)
		}
	})
	e.client.Call(MethodSubscribe, map[string]any{"logs": true}, nil)
	e.client.Call(MethodStart, map[string]any{"services": []string{"a"}}, nil)
	until(t, "the notifications", func() bool {
		mu.Lock()
		defer mu.Unlock()
		hasRunning, hasLine := false, false
		for _, s := range states {
			hasRunning = hasRunning || s == supervisor.Running
		}
		for _, x := range texts {
			hasLine = hasLine || x == "hello-ipc"
		}
		return hasRunning && hasLine
	})
}

func TestLogsRequestHonoursSinceSeq(t *testing.T) {
	e := setup(t, "hist", svc("a", "sleep 30"))
	e.sup.Log("a", "one")
	e.sup.Log("a", "two")
	var all []logs.Line
	e.client.Call(MethodLogs, map[string]any{"service": "a"}, &all)
	if len(all) != 2 || all[0].Text != "one" {
		t.Fatalf("all = %+v", all)
	}
	var rest []logs.Line
	e.client.Call(MethodLogs, map[string]any{"service": "a", "sinceSeq": all[0].Seq}, &rest)
	if len(rest) != 1 || rest[0].Text != "two" {
		t.Fatalf("rest = %+v", rest)
	}
}

func TestShutdownIsAcknowledgedBeforeItRuns(t *testing.T) {
	e := setup(t, "down", svc("a", "sleep 30"))
	var res struct{ OK bool }
	if err := e.client.Call(MethodShutdown, map[string]any{"how": "detach"}, &res); err != nil || !res.OK {
		t.Fatalf("shutdown: %v %+v", err, res)
	}
	until(t, "the callback", func() bool { return e.shutdownHow() == "detach" })
}

func TestASecondServerRefusesALiveSocketAndAStaleOneIsReplaced(t *testing.T) {
	e := setup(t, "dup", svc("a", "sleep 30"))
	again := NewServer(e.sup, ServerOptions{})
	if started, _ := again.Start(); started {
		t.Fatal("a second server took a live socket")
	}
	e.server.Close()
	if _, err := os.Stat(e.server.Path()); err == nil {
		t.Fatal("the socket file survived Close")
	}
	os.WriteFile(e.server.Path(), nil, 0o600) // not a listening socket
	replacement := NewServer(e.sup, ServerOptions{})
	started, err := replacement.Start()
	if err != nil || !started {
		t.Fatalf("stale socket was not replaced: %v %v", started, err)
	}
	replacement.Close()
}

func TestMalformedInputGetsAParseErrorAndKeepsTheConnection(t *testing.T) {
	e := setup(t, "bad", svc("a", "sleep 30"))
	raw, err := net.Dial("unix", e.server.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	raw.Write([]byte("not json\n"))
	raw.SetReadDeadline(time.Now().Add(3 * time.Second))
	line, err := bufio.NewReader(raw).ReadBytes('\n')
	if err != nil {
		t.Fatal(err)
	}
	var m Message
	json.Unmarshal(line, &m)
	if m.Error == nil || m.Error.Code != ErrParse {
		t.Fatalf("reply = %s", line)
	}
	var hello Hello
	if err := e.client.Call(MethodHello, nil, &hello); err != nil || hello.Protocol != Protocol {
		t.Fatalf("other connection broke: %v", err)
	}
}

func TestSocketPath(t *testing.T) {
	if got := SocketPath("/tmp/x"); got != "/tmp/x/orbit.sock" {
		t.Errorf("short = %q", got)
	}
	long := "/tmp/" + strings.Repeat("a", 120)
	p := SocketPath(long)
	if len(p) >= 104 || !strings.HasSuffix(p, ".sock") {
		t.Errorf("long = %q (%d)", p, len(p))
	}
	if SocketPath(long) != p {
		t.Error("the fallback path must be deterministic")
	}
}

// ---------------------------------------------------------------- Remote

func remote(t *testing.T, e *env) *Remote {
	t.Helper()
	r, err := ConnectRemote(e.server.Path())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(r.Detach)
	if err := r.Init(); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestRemoteMirrorsConfigStateAndForwardsCommands(t *testing.T) {
	e := setup(t, "mirror", svc("db", "sleep 30"), svc("api", "sleep 30", func(c *config.ServiceConfig) { c.DependsOn = []string{"db"} }))
	r := remote(t, e)
	if got := strings.Join(r.Names(), ","); got != "db,api" {
		t.Fatalf("names = %s", got)
	}
	if got := strings.Join(r.Order(), ","); got != "db,api" {
		t.Fatalf("order = %s", got)
	}
	if got := r.Dependents()["db"]; len(got) != 1 || got[0] != "api" {
		t.Fatalf("dependents = %v", got)
	}
	if r.StateDir() != e.sup.StateDir() {
		t.Fatal("state dir differs")
	}
	var mu sync.Mutex
	changed := map[string]bool{}
	r.OnChange(func(n string) { mu.Lock(); changed[n] = true; mu.Unlock() })
	if !r.Start("api") {
		t.Fatal("api did not start")
	}
	until(t, "the mirror to follow", func() bool {
		return r.State("api").Status == supervisor.Running && r.State("db").Status == supervisor.Running
	})
	if e.sup.State("api").Status != supervisor.Running || !r.IsUp("api") || r.OwnedRunningCount() != 2 {
		t.Fatal("mirror and supervisor disagree")
	}
	mu.Lock()
	if !changed["api"] {
		t.Error("no change event for api")
	}
	mu.Unlock()
	r.Stop("db", supervisor.StopOptions{})
	until(t, "everything to stop", func() bool { return !r.IsUp("api") && !r.IsUp("db") })
}

func TestALateClientSeesStateAndLogHistory(t *testing.T) {
	e := setup(t, "late", svc("a", "echo before; sleep 30"))
	e.sup.Start("a")
	until(t, "the first line", func() bool {
		for _, l := range e.sup.Logs.Lines("a") {
			if l.Text == "before" {
				return true
			}
		}
		return false
	})
	r := remote(t, e)
	if r.State("a").Status != supervisor.Running || r.State("a").Pid != e.sup.State("a").Pid {
		t.Fatalf("state = %+v", r.State("a"))
	}
	found := false
	for _, l := range r.LogStore().Lines("a") {
		found = found || l.Text == "before"
	}
	if !found {
		t.Fatal("history missing")
	}
	e.sup.Log("a", "after")
	until(t, "the live line", func() bool {
		n := 0
		for _, l := range r.LogStore().Lines("a") {
			if l.Text == "after" {
				n++
			}
		}
		return n == 1
	})
}

func TestClearingLogsClearsBothSides(t *testing.T) {
	e := setup(t, "clear", svc("a", "sleep 30"))
	r := remote(t, e)
	e.sup.Log("a", "x")
	until(t, "the line", func() bool { return len(r.LogStore().Lines("a")) == 1 })
	r.ClearLogs("a")
	if len(r.LogStore().Lines("a")) != 0 {
		t.Fatal("mirror not cleared")
	}
	until(t, "the daemon to clear", func() bool { return len(e.sup.Logs.Lines("a")) == 0 })
}

func TestDisposeAsksTheDaemonToQuitAndDetachLeavesItAlone(t *testing.T) {
	a := setup(t, "quit", svc("a", "sleep 30"))
	ra, _ := ConnectRemote(a.server.Path())
	ra.Init()
	closed := make(chan struct{})
	ra.OnClose(func() { close(closed) })
	ra.Detach()
	select {
	case <-closed:
	case <-time.After(3 * time.Second):
		t.Fatal("no close event")
	}
	if ra.Connected() || a.shutdownHow() != "" {
		t.Fatal("detaching must not shut the daemon down")
	}

	b := setup(t, "quit2", svc("a", "sleep 30"))
	rb, _ := ConnectRemote(b.server.Path())
	rb.Init()
	disposed := make(chan struct{})
	go func() { rb.Dispose(); close(disposed) }()
	until(t, "the shutdown request", func() bool { return b.shutdownHow() == "stop" })
	b.server.Close() // what a real daemon does next
	select {
	case <-disposed:
	case <-time.After(5 * time.Second):
		t.Fatal("dispose did not return after the hang-up")
	}
	if rb.Connected() {
		t.Fatal("still connected")
	}
}

func TestRefusesADaemonThatSpeaksAnotherProtocol(t *testing.T) {
	path := filepath.Join(t.TempDir(), "p.sock")
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		br := bufio.NewReader(conn)
		line, _ := br.ReadBytes('\n')
		var m Message
		json.Unmarshal(line, &m)
		res, _ := json.Marshal(map[string]any{"protocol": 99, "version": "9.9.9", "pid": 1, "stateDir": "/x"})
		conn.Write(encode(Message{ID: m.ID, Result: res}))
		br.ReadBytes('\n')
	}()
	_, err = ConnectRemote(path)
	if err == nil || !strings.Contains(err.Error(), "protocol 99") {
		t.Fatalf("err = %v", err)
	}
}

// ---------------------------------------------------------------- daemon

func project(t *testing.T, yaml string) *config.OrbitConfig {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "proj")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "orbit.yaml"), []byte(yaml), 0o644)
	cfg, err := config.LoadConfig(config.LoadOptions{Dir: dir, Env: map[string]string{}})
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func alive(pid int) bool { return syscall.Kill(pid, 0) == nil }

func TestDaemonLifecycle(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir()) // the daemon inherits it: never the real ~/.local/state
	cfg := project(t, "services:\n  w:\n    cmd: sleep 300\n    restart: always\n")
	dir := state.Dir(cfg)
	if _, err := os.Stat(SocketPath(dir)); err == nil {
		t.Fatal("socket exists before the daemon")
	}
	open := func() *Remote {
		r, err := ConnectProject(cfg)
		if err != nil {
			t.Fatal(err)
		}
		if err := r.Init(); err != nil {
			t.Fatal(err)
		}
		return r
	}
	t.Cleanup(func() { // last resort: the test failed before it shut the daemon down
		if pid := state.ReadLock(dir); pid != 0 {
			syscall.Kill(pid, syscall.SIGKILL)
		}
	})

	first := open()
	daemonPid := first.Hello.Pid
	if daemonPid == os.Getpid() || state.ReadLock(dir) != daemonPid {
		t.Fatalf("daemon pid = %d, lock = %d", daemonPid, state.ReadLock(dir))
	}
	if !first.Start("w") {
		t.Fatal("w did not start")
	}
	until(t, "w running", func() bool { return first.State("w").Status == supervisor.Running })
	servicePid := first.State("w").Pid

	// "close the terminal": the client goes away, the daemon and the service stay
	first.Detach()
	time.Sleep(100 * time.Millisecond)
	if !alive(daemonPid) || !alive(servicePid) {
		t.Fatal("the daemon or its service died with the client")
	}

	// the service dies while nobody watches: restart: always brings it back
	syscall.Kill(-servicePid, syscall.SIGKILL)
	until(t, "the old pid to go", func() bool { return !alive(servicePid) })

	// "open another terminal": same daemon, and by now it restarted the service
	second := open()
	if second.Hello.Pid != daemonPid {
		t.Fatalf("a second daemon started: %d vs %d", second.Hello.Pid, daemonPid)
	}
	until(t, "the restarted service", func() bool {
		st := second.State("w")
		return st.Status == supervisor.Running && st.Pid != servicePid && st.Pid != 0
	})
	if second.State("w").Restarts != 1 {
		t.Fatalf("restarts = %d", second.State("w").Restarts)
	}
	newPid := second.State("w").Pid
	found := false
	for _, l := range second.LogStore().Lines("w") {
		found = found || strings.Contains(l.Text, "exited with")
	}
	if !found {
		t.Fatal("the history from before the client connected is missing")
	}

	// quitting for good: services stopped, daemon gone, lock and socket released
	second.Dispose()
	until(t, "the daemon to go", func() bool { return !alive(daemonPid) })
	if alive(newPid) {
		t.Fatal("the service outlived the daemon")
	}
	if state.ReadLock(dir) != 0 {
		t.Fatal("the lock was not released")
	}
	if _, err := os.Stat(SocketPath(dir)); err == nil {
		t.Fatal("the socket was not removed")
	}
}

func TestASecondClientDoesNotStartASecondDaemon(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	cfg := project(t, "services:\n  w:\n    cmd: sleep 300\n")
	dir := state.Dir(cfg)
	t.Cleanup(func() {
		if pid := state.ReadLock(dir); pid != 0 {
			syscall.Kill(pid, syscall.SIGKILL)
		}
	})
	var wg sync.WaitGroup
	pids := make([]int, 2)
	for i := range pids {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := ConnectProject(cfg)
			if err != nil {
				t.Error(err)
				return
			}
			pids[i] = r.Hello.Pid
			r.Detach()
		}()
	}
	wg.Wait()
	if pids[0] == 0 || pids[0] != pids[1] {
		t.Fatalf("pids = %v", pids)
	}
	r, _ := ConnectProject(cfg)
	r.Init()
	r.Dispose()
	until(t, "the daemon to go", func() bool { return !alive(pids[0]) })
}
