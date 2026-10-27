package plugins

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/JGoterris/orbit/internal/platform"
	"github.com/JGoterris/orbit/pluginsdk/protocol"
)

// TestMain lets the test binary be the plugin: a Process runs os.Args[0] with ORBIT_TEST_PLUGIN set.
func TestMain(m *testing.M) {
	if mode := os.Getenv("ORBIT_TEST_PLUGIN"); mode != "" {
		fakePlugin(mode)
		return
	}
	os.Exit(m.Run())
}

func fakePlugin(mode string) {
	if f := os.Getenv("ORBIT_TEST_LAUNCHES"); f != "" {
		if w, err := os.OpenFile(f, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644); err == nil {
			w.WriteString("x")
			w.Close()
		}
	}
	switch mode {
	case "crash":
		os.Exit(3)
	case "hang":
		time.Sleep(time.Hour) // a bare select{} makes the runtime of macOS and Windows kill the process as deadlocked
	}
	out := os.Stdout
	send := func(b []byte) { out.Write(b) }
	if mode == "junk" {
		out.WriteString("hello, I print to stdout by mistake\n{not json}\n")
	}
	protocol.Read(os.Stdin, func(m protocol.Message) {
		if !m.IsRequest() {
			return
		}
		switch m.Method {
		case protocol.MethodInitialize:
			send(protocol.Response(*m.ID, protocol.InitializeResult{Name: "fake"}))
			if mode == "deaf" {
				time.Sleep(time.Hour) // never reads stdin again
			}
			send(protocol.Notification(protocol.NotifyToast, protocol.Toast{Text: "ready", Tone: protocol.ToneOK}))
		case protocol.MethodShutdown:
			send(protocol.Response(*m.ID, struct{}{}))
			os.Exit(0)
		case protocol.MethodViewData:
			var p protocol.ViewDataParams
			json.Unmarshal(m.Params, &p)
			send(protocol.Response(*m.ID, protocol.ViewData{List: &protocol.ListData{
				Items: []protocol.Item{{ID: p.Pane, Cells: map[string]protocol.Cell{"name": {Text: p.Context.Service}}}},
			}}))
		case "emit":
			send(protocol.Notification(protocol.NotifyBadges, protocol.BadgesSet{Target: "service:api", Items: []protocol.Badge{{Text: "\x1b[31m!41", Tone: "bogus"}}}))
			send(protocol.Notification(protocol.NotifyBadges, protocol.BadgesSet{Target: "header", Items: []protocol.Badge{{Text: "no"}}}))
			send(protocol.Notification(protocol.NotifyInvalidate, protocol.Invalidate{View: "v"}))
			send(protocol.Response(*m.ID, struct{}{}))
		case "boom":
			send(protocol.Failure(*m.ID, protocol.ErrPlugin, "it broke"))
		case "hang":
		default:
			send(protocol.Failure(*m.ID, protocol.ErrMethod, "unknown"))
		}
	}, nil)
}

func fastTimings(t *testing.T) {
	t.Helper()
	i, s, r := initTimeout, shutdownWait, retryStep
	initTimeout, shutdownWait, retryStep = 300*time.Millisecond, 300*time.Millisecond, 20*time.Millisecond
	t.Cleanup(func() { initTimeout, shutdownWait, retryStep = i, s, r })
	t.Setenv("XDG_STATE_HOME", t.TempDir())
}

type recorder struct {
	mu       sync.Mutex
	messages []protocol.Message
	statuses []Status
}

func (r *recorder) message(m protocol.Message) {
	r.mu.Lock()
	r.messages = append(r.messages, m)
	r.mu.Unlock()
}

func (r *recorder) status(s Status, _ string) {
	r.mu.Lock()
	r.statuses = append(r.statuses, s)
	r.mu.Unlock()
}

func (r *recorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.messages)
}

func startFake(t *testing.T, mode string) (*Process, *recorder) {
	t.Helper()
	fastTimings(t)
	return launchFake(t, mode)
}

func launchFake(t *testing.T, mode string) (*Process, *recorder) {
	t.Helper()
	t.Setenv("ORBIT_TEST_PLUGIN", mode)
	pl := &Plugin{Dir: t.TempDir(), Manifest: &protocol.Manifest{
		Name: "fake", Run: &protocol.Command{Command: []string{os.Args[0]}},
	}}
	rec := &recorder{}
	p := NewProcess(pl, protocol.InitializeParams{API: protocol.API, ProjectDir: t.TempDir()}, rec.message, rec.status)
	p.Start()
	t.Cleanup(p.Stop)
	return p, rec
}

func waitStatus(t *testing.T, p *Process, want Status) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if s, _ := p.Status(); s == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	s, e := p.Status()
	t.Fatalf("status is %s (%s), want %s", s, e, want)
}

func TestProcessCallsAndNotifications(t *testing.T) {
	p, rec := startFake(t, "ok")
	waitStatus(t, p, StatusRunning)

	var data protocol.ViewData
	err := p.Call(protocol.MethodViewData, protocol.ViewDataParams{View: "v", Pane: "list", Context: protocol.Context{Service: "api"}}, &data, time.Second)
	if err != nil || data.List == nil || data.List.Items[0].Cells["name"].Text != "api" {
		t.Fatalf("view.data = %+v, %v", data, err)
	}

	err = p.Call("boom", nil, nil, time.Second)
	if err == nil || err.Error() != "it broke" {
		t.Fatalf("a plugin error must come back as it is: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for rec.count() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if rec.count() == 0 || rec.messages[0].Method != protocol.NotifyToast {
		t.Fatalf("the toast notification did not arrive: %+v", rec.messages)
	}
}

func TestProcessCallTimesOut(t *testing.T) {
	p, _ := startFake(t, "ok")
	waitStatus(t, p, StatusRunning)
	start := time.Now()
	if err := p.Call("hang", nil, nil, 100*time.Millisecond); err == nil || !strings.Contains(err.Error(), "did not answer") {
		t.Fatalf("want a timeout, got %v", err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("the timeout did not apply")
	}
	if err := p.Call(protocol.MethodViewData, protocol.ViewDataParams{Pane: "x"}, nil, time.Second); err != nil {
		t.Fatalf("a timed out request must not break the next one: %v", err)
	}
}

func TestProcessIgnoresJunkOnStdout(t *testing.T) {
	p, _ := startFake(t, "junk")
	waitStatus(t, p, StatusRunning)
	if err := p.Call(protocol.MethodViewData, protocol.ViewDataParams{Pane: "x"}, nil, time.Second); err != nil {
		t.Fatal(err)
	}
	log, _ := os.ReadFile(LogFile("fake"))
	if !strings.Contains(string(log), "I print to stdout by mistake") {
		t.Fatalf("the junk should be in the plugin log, got %q", log)
	}
}

func TestProcessFailsAfterRetries(t *testing.T) {
	launches := filepath.Join(t.TempDir(), "launches")
	t.Setenv("ORBIT_TEST_LAUNCHES", launches)
	fastTimings(t)
	initTimeout = 5 * time.Second // starting the test binary can take longer than 300ms on Windows; the crash must win the race
	p, _ := launchFake(t, "crash")
	waitStatus(t, p, StatusFailed)
	if _, msg := p.Status(); !strings.Contains(msg, "exit") {
		t.Fatalf("the reason should say how it died: %q", msg)
	}
	if err := p.Call(protocol.MethodViewData, nil, nil, time.Second); err == nil {
		t.Fatal("a failed plugin must refuse calls")
	}
	got, _ := os.ReadFile(launches)
	if len(got) != 1+maxRetries {
		t.Fatalf("it should start once and retry %d times, started %d times", maxRetries, len(got))
	}
}

func TestProcessThatNeverInitializesFails(t *testing.T) {
	p, _ := startFake(t, "hang")
	waitStatus(t, p, StatusFailed)
	if _, msg := p.Status(); !strings.Contains(msg, "initialize") {
		t.Fatalf("want an initialize error, got %q", msg)
	}
}

func TestProcessMissingExecutableIsFatal(t *testing.T) {
	fastTimings(t)
	pl := &Plugin{Dir: t.TempDir(), Manifest: &protocol.Manifest{
		Name: "gone", Run: &protocol.Command{Command: []string{"./no-such-binary"}},
	}}
	rec := &recorder{}
	p := NewProcess(pl, protocol.InitializeParams{ProjectDir: t.TempDir()}, rec.message, rec.status)
	p.Start()
	t.Cleanup(p.Stop)
	waitStatus(t, p, StatusFailed)
	p.Stop()
	if p.logFile() != nil {
		t.Fatal("the log must be closed once the plugin is given up on: Windows cannot delete an open file")
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	for _, s := range rec.statuses {
		if s == StatusStarting && len(rec.statuses) > 2 {
			t.Fatalf("a missing binary must not be retried: %v", rec.statuses)
		}
	}
}

func TestProcessStopKillsIt(t *testing.T) {
	p, _ := startFake(t, "ok")
	waitStatus(t, p, StatusRunning)
	p.mu.Lock()
	pid := p.pid
	p.mu.Unlock()
	if !platform.PidAlive(pid) {
		t.Fatal("the plugin should be running")
	}
	p.Stop()
	waitStatus(t, p, StatusStopped)
	deadline := time.Now().Add(2 * time.Second)
	for platform.PidAlive(pid) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if platform.PidAlive(pid) {
		t.Fatal("Stop left the process alive")
	}
}

func TestProcessStopDoesNotWaitForAHungPlugin(t *testing.T) {
	p, _ := startFake(t, "ok")
	waitStatus(t, p, StatusRunning)
	// the fake never answers "hang", but it does answer shutdown, so freeze it with a request first
	go p.Call("hang", nil, nil, 5*time.Second)
	time.Sleep(50 * time.Millisecond)
	start := time.Now()
	p.Stop()
	if d := time.Since(start); d > 3*time.Second {
		t.Fatalf("Stop took %s", d)
	}
}

func TestNotifyNeverBlocksOnAPluginThatDoesNotRead(t *testing.T) {
	p, _ := startFake(t, "deaf")
	waitStatus(t, p, StatusRunning)
	big := strings.Repeat("x", 4096)
	start := time.Now()
	for i := 0; i < 2000; i++ {
		p.Notify(protocol.NotifyEvent, protocol.Event{Kind: protocol.EventLog, Text: big})
	}
	if d := time.Since(start); d > time.Second {
		t.Fatalf("notifying took %s: the caller was blocked by the plugin", d)
	}
	if p.dropped.Load() == 0 {
		t.Fatal("what the plugin cannot take must be dropped, not queued forever")
	}
	// a call must give up too, and Stop must not wait for a plugin that is not reading
	if err := p.Call(protocol.MethodViewData, protocol.ViewDataParams{Pane: "x"}, nil, 200*time.Millisecond); err == nil {
		t.Fatal("a call to a deaf plugin must time out")
	}
	stopped := time.Now()
	p.Stop()
	if d := time.Since(stopped); d > 4*time.Second {
		t.Fatalf("Stop took %s", d)
	}
}
