package plugins_test

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/JGoterris/orbit/internal/config"
	"github.com/JGoterris/orbit/internal/core/supervisor"
	"github.com/JGoterris/orbit/internal/plugins"
	"github.com/JGoterris/orbit/internal/plugins/plugintest"
	"github.com/JGoterris/orbit/pluginsdk/protocol"
)

const orbitYAML = `
name: itest
services:
  tick:
    type: ticker
    with: { message: hi, intervalMs: 30 }
  once:
    type: ticker
    with: { message: once, intervalMs: 20, exitAfter: 2 }
  plain:
    cmd: sleep 30
`

func isolate(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("ORBIT_NO_PLUGINS", "")
}

func project(t *testing.T) (*supervisor.Supervisor, *plugins.Manager) {
	t.Helper()
	isolate(t)
	if _, err := plugins.Link(plugintest.Demo(t)); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "orbit.yaml"), []byte(orbitYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadConfig(config.LoadOptions{Dir: dir, Env: map[string]string{}})
	if err != nil {
		t.Fatal(err)
	}
	mgr := plugins.ForProject(cfg, "1.0.0")
	sup := supervisor.New(cfg, supervisor.Options{StateDir: t.TempDir(), Plugins: mgr})
	if err := sup.Init(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(sup.Dispose)
	waitPlugin(t, mgr, plugins.StatusRunning)
	return sup, mgr
}

func waitPlugin(t *testing.T, m *plugins.Manager, want plugins.Status) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		for _, i := range m.Plugins() {
			if i.Name == "demo" && i.Status == want {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("the plugin never became %s: %+v", want, m.Plugins())
}

func waitStatus(t *testing.T, sup *supervisor.Supervisor, name string, want ...supervisor.Status) supervisor.ServiceState {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		st := sup.State(name)
		for _, w := range want {
			if st.Status == w {
				return st
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("%s never became %v: %+v", name, want, sup.State(name))
	return supervisor.ServiceState{}
}

func logsOf(sup *supervisor.Supervisor, name string) string {
	var b strings.Builder
	for _, l := range sup.LogStore().Lines(name) {
		b.WriteString(l.Text + "\n")
	}
	return b.String()
}

func waitLog(t *testing.T, sup *supervisor.Supervisor, name, want string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(logsOf(sup, name), want) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("%s never logged %q:\n%s", name, want, logsOf(sup, name))
}

func TestServiceOfAPluginType(t *testing.T) {
	sup, _ := project(t)
	if got := sup.Service("tick").Type; got != "ticker" {
		t.Fatalf("type = %q", got)
	}
	sup.Start("tick")
	waitStatus(t, sup, "tick", supervisor.Running, supervisor.Healthy)
	waitLog(t, sup, "tick", "ticker started")
	waitLog(t, sup, "tick", "hi #2")
	sup.Stop("tick", supervisor.StopOptions{})
	waitStatus(t, sup, "tick", supervisor.Stopped)
	n := strings.Count(logsOf(sup, "tick"), "hi #")
	time.Sleep(150 * time.Millisecond)
	if strings.Count(logsOf(sup, "tick"), "hi #") != n {
		t.Fatal("a stopped service must not keep printing")
	}
}

func TestServiceOfAPluginTypeThatEndsByItself(t *testing.T) {
	sup, _ := project(t)
	sup.Start("once")
	st := waitStatus(t, sup, "once", supervisor.Exited)
	if st.ExitCode == nil || *st.ExitCode != 0 {
		t.Fatalf("it reported exit code 0: %+v", st)
	}
	if !strings.Contains(logsOf(sup, "once"), "once #2") {
		t.Fatalf("logs:\n%s", logsOf(sup, "once"))
	}
}

func TestPluginHostThroughARealProcess(t *testing.T) {
	sup, mgr := project(t)
	var mu sync.Mutex
	var updates []plugins.Update
	mgr.OnUpdate(func(u plugins.Update) { mu.Lock(); updates = append(updates, u); mu.Unlock() })

	d, err := mgr.Data("demo", protocol.ViewDataParams{View: "items", Pane: "list"})
	if err != nil || d.List == nil || len(d.List.Items) != 3 || d.List.Items[0].ID != "alpha" {
		t.Fatalf("data = %+v, %v", d, err)
	}
	info, err := mgr.Data("demo", protocol.ViewDataParams{View: "items", Pane: "info", Context: protocol.Context{Service: "plain", Selection: map[string]string{"list": "beta"}}})
	if err != nil || info.Detail.Sections[0].Title != "item beta" || info.Detail.Sections[0].Fields[1].Value != "plain" {
		t.Fatalf("detail = %+v, %v", info, err)
	}
	if got := info.Detail.Sections[0].Fields[2].Value; got != "tick@ticker,once@ticker,plain@process" {
		t.Fatalf("the plugin must be told the services of the project, with their types: %q", got)
	}
	text, _ := mgr.Data("demo", protocol.ViewDataParams{View: "items", Pane: "log", Context: protocol.Context{Selection: map[string]string{"list": "beta"}}})
	for _, ln := range text.Text.Lines {
		for _, sp := range ln {
			if strings.Contains(sp.Text, "\x1b") {
				t.Fatalf("an escape sequence reached the interface: %q", sp.Text)
			}
		}
	}

	res, err := mgr.Invoke("demo", protocol.InvokeParams{Action: "bump", Inputs: map[string]string{"level": "minor"}, Context: protocol.Context{Selection: map[string]string{"list": "alpha"}}})
	if err != nil || res.Toast == nil || res.Toast.Text != "bumped alpha (minor)" {
		t.Fatalf("invoke = %+v, %v", res, err)
	}
	if _, err := mgr.Invoke("demo", protocol.InvokeParams{Action: "bump"}); err == nil || !strings.Contains(err.Error(), "nothing selected") {
		t.Fatalf("an error of the plugin must reach the caller: %v", err)
	}
	d2, _ := mgr.Data("demo", protocol.ViewDataParams{View: "items", Pane: "list"})
	if d2.List.Items[0].Cells["version"].Text != "0.2.0" {
		t.Fatalf("the action must have changed the data: %+v", d2.List.Items[0])
	}

	secs, err := mgr.Sections("demo", protocol.SectionsParams{Service: "plain", Section: "info"})
	if err != nil || secs[0].Fields[0].Value != "hi plain" {
		t.Fatalf("sections = %+v, %v", secs, err)
	}

	// the header badge is set at init and changes with the action; the service badge follows the events of state
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && !hasBadge(mgr, "header", "bumps 1") {
		time.Sleep(20 * time.Millisecond)
	}
	if !hasBadge(mgr, "header", "bumps 1") {
		t.Fatalf("badges = %+v", mgr.Badges())
	}
	sup.Start("plain")
	for time.Now().Before(deadline) && !hasBadge(mgr, "service:plain", "running") {
		time.Sleep(20 * time.Millisecond)
	}
	if !hasBadge(mgr, "service:plain", "running") {
		t.Fatalf("the plugin must be told about the state of services: %+v", mgr.Badges())
	}
	mu.Lock()
	defer mu.Unlock()
	kinds := map[string]bool{}
	for _, u := range updates {
		kinds[u.Kind] = true
	}
	if !kinds[plugins.UpdateBadges] {
		t.Fatalf("badge changes must be announced: %v", kinds)
	}
}

func hasBadge(m *plugins.Manager, target, text string) bool {
	for _, b := range m.Badges()[target] {
		if b.Text == text {
			return true
		}
	}
	return false
}

func TestServicesDieWithTheirPluginAndComeBack(t *testing.T) {
	sup, mgr := project(t)
	sup.Start("tick")
	waitStatus(t, sup, "tick", supervisor.Running)
	waitLog(t, sup, "tick", "hi #1")

	mgr.KillSync()
	waitStatus(t, sup, "tick", supervisor.Crashed, supervisor.Failed)
	if !strings.Contains(logsOf(sup, "tick"), "the plugin demo stopped") {
		t.Fatalf("the log must say why:\n%s", logsOf(sup, "tick"))
	}
	// orbit restarts the plugin, and the service can be started again
	waitPlugin(t, mgr, plugins.StatusRunning)
	sup.Start("tick")
	waitStatus(t, sup, "tick", supervisor.Running)
}

func TestServiceTypeWithoutItsPlugin(t *testing.T) {
	isolate(t)
	if _, err := plugins.Link(plugintest.Demo(t)); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "orbit.yaml"), []byte(orbitYAML), 0o644)
	cfg, err := config.LoadConfig(config.LoadOptions{Dir: dir, Env: map[string]string{}})
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("ORBIT_NO_PLUGINS", "1") // the type is installed, but no plugin runs here
	sup := supervisor.New(cfg, supervisor.Options{StateDir: t.TempDir(), Plugins: plugins.ForProject(cfg, "1.0.0")})
	sup.Init()
	t.Cleanup(sup.Dispose)
	sup.Start("tick")
	st := waitStatus(t, sup, "tick", supervisor.Failed, supervisor.Crashed)
	if !strings.Contains(st.Error, "no plugin provides the service type") {
		t.Fatalf("the error must say what to do: %q", st.Error)
	}
}

func TestOrbitDoesNotOpenAProjectWithAnUnknownType(t *testing.T) {
	isolate(t) // nothing installed
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "orbit.yaml"), []byte(orbitYAML), 0o644)
	_, err := config.LoadConfig(config.LoadOptions{Dir: dir, Env: map[string]string{}})
	if err == nil || !strings.Contains(err.Error(), `unknown type "ticker"`) {
		t.Fatalf("want an unknown type error, got %v", err)
	}
}

func TestValidateServicesAsksThePlugin(t *testing.T) {
	isolate(t)
	if _, err := plugins.Link(plugintest.Demo(t)); err != nil {
		t.Fatal(err)
	}
	load := func(yaml string) *config.OrbitConfig {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "orbit.yaml"), []byte(yaml), 0o644)
		cfg, err := config.LoadConfig(config.LoadOptions{Dir: dir, Env: map[string]string{}})
		if err != nil {
			t.Fatal(err)
		}
		return cfg
	}
	if errs := plugins.ValidateServices(load(orbitYAML), "1.0.0"); len(errs) != 0 {
		t.Fatalf("a valid project: %v", errs)
	}
	errs := plugins.ValidateServices(load("services:\n  t: { type: ticker, with: { intervalMs: -5 } }\n  ok: { cmd: x }\n"), "1.0.0")
	if len(errs) != 1 || !strings.Contains(errs[0], "services.t.with") || !strings.Contains(errs[0], "must not be negative") {
		t.Fatalf("the plugin's complaint must reach the user: %v", errs)
	}
	if errs := plugins.ValidateServices(load("services:\n  a: { cmd: x }\n"), "1.0.0"); errs != nil {
		t.Fatalf("a project with no plugin services must not start any plugin: %v", errs)
	}
}
