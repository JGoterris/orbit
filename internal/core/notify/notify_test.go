package notify

import (
	"encoding/base64"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/JGoterris/orbit/internal/core/resources"
)

type fakeSup struct {
	mu    sync.Mutex
	views map[string]ServiceView
	subs  []func(string)
	names []string
}

func newFake() *fakeSup {
	f := &fakeSup{names: []string{"api", "web", "db"}, views: map[string]ServiceView{}}
	for _, n := range f.names {
		f.views[n] = ServiceView{Status: "healthy"}
	}
	return f
}

func (f *fakeSup) ProjectName() string { return "shop" }
func (f *fakeSup) Names() []string     { return f.names }
func (f *fakeSup) ServiceView(n string) ServiceView {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.views[n]
}
func (f *fakeSup) OnChange(fn func(string)) func() {
	f.mu.Lock()
	f.subs = append(f.subs, fn)
	f.mu.Unlock()
	return func() {
		f.mu.Lock()
		f.subs = nil
		f.mu.Unlock()
	}
}
func (f *fakeSup) set(name, status string, extra ...string) {
	v := ServiceView{Status: status}
	if len(extra) > 0 {
		v.Error = extra[0]
		v.Health = extra[0]
	}
	f.mu.Lock()
	cur := f.views[name]
	v.Resources = cur.Resources
	f.views[name] = v
	subs := append([]func(string){}, f.subs...)
	f.mu.Unlock()
	for _, s := range subs {
		s(name)
	}
}
func (f *fakeSup) setRes(name string, r *resources.Info) {
	f.mu.Lock()
	v := f.views[name]
	v.Resources = r
	f.views[name] = v
	subs := append([]func(string){}, f.subs...)
	f.mu.Unlock()
	for _, s := range subs {
		s(name)
	}
}

type harness struct {
	sup  *fakeSup
	mu   sync.Mutex
	sent []Notice
	t    int64
	stop func()
}

func (h *harness) got() []Notice {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]Notice(nil), h.sent...)
}

func setup(enabled func() bool) *harness {
	h := &harness{sup: newFake()}
	opts := Options{
		Send:    func(n Notice) { h.mu.Lock(); h.sent = append(h.sent, n); h.mu.Unlock() },
		GroupMs: 10, Now: func() int64 { h.mu.Lock(); defer h.mu.Unlock(); return h.t },
		Enabled: enabled,
	}
	if opts.Enabled == nil {
		opts.Enabled = func() bool { return true }
	}
	h.stop = Attach(h.sup, opts)
	return h
}

func (h *harness) advance(ms int64) { h.mu.Lock(); h.t += ms; h.mu.Unlock() }
func settle()                       { time.Sleep(60 * time.Millisecond) }

func TestCrashUnhealthyAndRecovery(t *testing.T) {
	h := setup(nil)
	h.sup.set("api", "crashed", "exited with code 1")
	settle()
	want := []Notice{{Title: "orbit · shop", Body: "✖  api crashed: exited with code 1", Urgent: true}}
	if got := h.got(); !reflect.DeepEqual(got, want) {
		t.Fatalf("sent = %+v", got)
	}
	h.sup.set("web", "unhealthy", "HTTP 503")
	settle()
	if got := h.got(); len(got) != 2 || got[1].Body != "●  web is unhealthy: HTTP 503" {
		t.Fatalf("sent = %+v", got)
	}
	h.sup.set("web", "healthy")
	settle()
	if got := h.got(); len(got) != 3 || got[2].Body != "✔  web recovered" || got[2].Urgent {
		t.Fatalf("sent = %+v", got)
	}
}

func TestNeverDownIsNotARecoveryAndStoppingIsNotAFailure(t *testing.T) {
	h := setup(nil)
	h.sup.set("api", "starting")
	h.sup.set("api", "healthy")
	h.sup.set("db", "stopping")
	h.sup.set("db", "stopped")
	settle()
	if got := h.got(); len(got) != 0 {
		t.Fatalf("sent = %+v", got)
	}
}

func TestACascadeIsASingleNotification(t *testing.T) {
	h := setup(nil)
	h.sup.set("db", "crashed", "exited with code 1")
	h.sup.set("api", "failed", "dependency not ready: db")
	h.sup.set("web", "failed", "dependency not ready: db")
	settle()
	got := h.got()
	if len(got) != 1 || got[0].Body != "✖  3 services down: db, api, web" {
		t.Fatalf("sent = %+v", got)
	}
}

func TestARestartLoopIsReportedOncePerMinute(t *testing.T) {
	h := setup(nil)
	for i := 0; i < 3; i++ {
		h.sup.set("api", "starting")
		h.sup.set("api", "crashed", "boom")
		h.advance(5_000)
	}
	settle()
	if n := len(h.got()); n != 1 {
		t.Fatalf("sent %d", n)
	}
	h.advance(60_000)
	h.sup.set("api", "starting")
	h.sup.set("api", "crashed", "boom")
	settle()
	if n := len(h.got()); n != 2 {
		t.Fatalf("sent %d", n)
	}
}

func TestDisabledSendsNothingAndStopDetaches(t *testing.T) {
	off := setup(func() bool { return false })
	off.sup.set("api", "crashed")
	settle()
	if len(off.got()) != 0 {
		t.Fatal("sent while disabled")
	}
	gone := setup(nil)
	gone.stop()
	gone.sup.set("api", "crashed")
	settle()
	if len(gone.got()) != 0 {
		t.Fatal("sent after stop")
	}
}

func TestMemoryOverTheLimitIsUrgentAndReportedOnce(t *testing.T) {
	h := setup(nil)
	const mb = 1024 * 1024
	h.sup.setRes("api", &resources.Info{MemLimit: 1024 * mb, Level: resources.LevelWarn})
	settle()
	if len(h.got()) != 0 {
		t.Fatal("warn must be silent")
	}
	over := &resources.Info{MemLimit: 1024 * mb, Level: resources.LevelOver}
	h.sup.setRes("api", over)
	h.sup.setRes("api", over)
	settle()
	want := []Notice{{Title: "orbit · shop", Body: "▲  api is over its memory limit (limit 1.0G)", Urgent: true}}
	if got := h.got(); !reflect.DeepEqual(got, want) {
		t.Fatalf("sent = %+v", got)
	}
}

func TestALeakIsReportedOnceWithItsRate(t *testing.T) {
	h := setup(nil)
	const mb = 1024 * 1024
	leak := &resources.Info{Leak: &resources.Leak{PerMin: 5 * mb, EtaMs: 40 * 60_000}}
	h.sup.setRes("web", leak)
	h.sup.setRes("web", leak)
	settle()
	got := h.got()
	if len(got) != 1 || got[0].Urgent || got[0].Body != "↗  web may be leaking memory: +5.0M/min, limit in ~40m00s" {
		t.Fatalf("sent = %+v", got)
	}
	h.sup.setRes("web", nil)
	h.advance(5 * 60_000)
	h.sup.setRes("web", leak)
	settle()
	if len(h.got()) != 1 {
		t.Fatal("the long throttle did not hold")
	}
	h.sup.setRes("web", nil)
	h.advance(11 * 60_000)
	h.sup.setRes("web", leak)
	settle()
	if len(h.got()) != 2 {
		t.Fatalf("sent %d", len(h.got()))
	}
}

func which(found ...string) func(string) string {
	return func(c string) string {
		for _, f := range found {
			if f == c {
				return "/bin/" + c
			}
		}
		return ""
	}
}

func TestBackendSelection(t *testing.T) {
	cases := []struct {
		env  BackendEnv
		want string
	}{
		{BackendEnv{"linux", true, which("powershell.exe", "notify-send")}, "powershell"},
		{BackendEnv{"linux", true, which("notify-send")}, "notify-send"},
		{BackendEnv{"darwin", false, which("osascript")}, "osascript"},
		{BackendEnv{"linux", false, which("notify-send")}, "notify-send"},
		{BackendEnv{"linux", false, which()}, ""},
	}
	for _, c := range cases {
		b := PickBackend(c.env)
		got := ""
		if b != nil {
			got = b.Name
		}
		if got != c.want {
			t.Errorf("%+v = %q, want %q", c.env, got, c.want)
		}
	}
}

func TestTextsReachTheProgramAsData(t *testing.T) {
	n := Notice{Title: "orbit · it's", Body: "✖  api crashed: -x ‘quoted’", Urgent: true}
	ns := PickBackend(BackendEnv{"linux", false, which("notify-send")}).Argv(n)
	if want := []string{"/bin/notify-send", "-a", "orbit", "-u", "critical", "--", n.Title, n.Body}; !reflect.DeepEqual(ns, want) {
		t.Errorf("notify-send = %v", ns)
	}
	mac := PickBackend(BackendEnv{"darwin", false, which("osascript")}).Argv(n)
	if !reflect.DeepEqual(mac[len(mac)-2:], []string{n.Title, n.Body}) {
		t.Errorf("osascript tail = %v", mac[len(mac)-2:])
	}
	for i, a := range mac {
		if i > 0 && mac[i-1] == "-e" && strings.Contains(a, "crashed") {
			t.Errorf("the text leaked into the script: %q", a)
		}
	}
}

func TestPowershellQuotesCannotEndTheString(t *testing.T) {
	n := Notice{Title: "orbit · it's", Body: "✖  api crashed: -x ‘quoted’"}
	script := PowershellScript(n)
	if !strings.Contains(script, "InnerText = 'orbit · it''s'") || !strings.Contains(script, "-x ''quoted''") {
		t.Fatalf("script = %s", script)
	}
	argv := PickBackend(BackendEnv{"linux", true, which("powershell.exe")}).Argv(n)
	raw, err := base64.StdEncoding.DecodeString(argv[len(argv)-1])
	if err != nil {
		t.Fatal(err)
	}
	units := make([]uint16, len(raw)/2)
	for i := range units {
		units[i] = uint16(raw[2*i]) | uint16(raw[2*i+1])<<8
	}
	if got := string(utf16.Decode(units)); got != script {
		t.Fatalf("encoded script differs:\n%s", got)
	}
}
