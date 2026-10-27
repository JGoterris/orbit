package plugins

import (
	"os"
	"sync"
	"testing"
	"time"

	"github.com/JGoterris/orbit/pluginsdk/protocol"
)

func fakeManager(t *testing.T) (*Manager, *[]Update, *sync.Mutex) {
	t.Helper()
	fastTimings(t)
	t.Setenv("ORBIT_TEST_PLUGIN", "ok")
	pl := &Plugin{Dir: t.TempDir(), Manifest: &protocol.Manifest{
		Name: "fake", Version: "1.0.0", Run: &protocol.Command{Command: []string{os.Args[0]}},
		Contributes: protocol.Contributes{
			Views:         []protocol.View{{ID: "v", Title: "V", Panes: []protocol.Pane{{ID: "list", Widget: protocol.WidgetList}}}},
			ServiceColumn: &protocol.ColumnDef{ID: "pr"},
		},
	}}
	m := NewManager(Options{ProjectDir: t.TempDir(), Specs: []Spec{{Plugin: pl}}, Missing: []string{"absent"}})
	var mu sync.Mutex
	var updates []Update
	m.OnUpdate(func(u Update) { mu.Lock(); updates = append(updates, u); mu.Unlock() })
	m.Start()
	t.Cleanup(m.Stop)
	waitStatus(t, m.procs["fake"], StatusRunning)
	return m, &updates, &mu
}

func TestManagerCleansWhatThePluginSends(t *testing.T) {
	m, _, _ := fakeManager(t)
	d, err := m.Data("fake", protocol.ViewDataParams{View: "v", Pane: "list", Context: protocol.Context{Service: "a\x1b[31mb\x07"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := d.List.Items[0].Cells["name"].Text; got != "ab" {
		t.Fatalf("escape sequences must not reach the screen, got %q", got)
	}
}

func TestManagerRejectsWhatTheManifestDoesNotDeclare(t *testing.T) {
	m, _, _ := fakeManager(t)
	if _, err := m.Data("fake", protocol.ViewDataParams{View: "v", Pane: "nope"}); err == nil {
		t.Fatal("a pane that is not in the manifest must be refused")
	}
	if _, err := m.Data("other", protocol.ViewDataParams{View: "v", Pane: "list"}); err == nil {
		t.Fatal("an unknown plugin must be refused")
	}
	if _, err := m.Invoke("fake", protocol.InvokeParams{Action: "nope"}); err == nil {
		t.Fatal("an action that is not in the manifest must be refused")
	}
}

func TestManagerBadgesAndUpdates(t *testing.T) {
	m, updates, mu := fakeManager(t)
	if err := m.procs["fake"].Call("emit", nil, nil, time.Second); err != nil {
		t.Fatal(err)
	}
	b := m.Badges()
	if len(b["service:api"]) != 1 || b["service:api"][0].Text != "!41" || b["service:api"][0].Tone != "" {
		t.Fatalf("service badge must be cleaned: %+v", b)
	}
	if len(b["header"]) != 0 {
		t.Fatal("a plugin that did not declare headerBadges must not badge the header")
	}
	mu.Lock()
	kinds := map[string]bool{}
	for _, u := range *updates {
		kinds[u.Kind] = true
	}
	mu.Unlock()
	for _, k := range []string{UpdateToast, UpdateBadges, UpdateInvalidate} {
		if !kinds[k] {
			t.Errorf("no %q update was emitted: %v", k, kinds)
		}
	}

	m.procs["fake"].Stop()
	if len(m.Badges()["service:api"]) != 0 {
		t.Fatal("the badges of a plugin that stopped must go away")
	}
}

func TestManagerListsMissingPlugins(t *testing.T) {
	m, _, _ := fakeManager(t)
	infos := m.Plugins()
	if len(infos) != 2 || infos[0].Name != "fake" || infos[0].Status != StatusRunning {
		t.Fatalf("plugins = %+v", infos)
	}
	if last := infos[1]; !last.Missing || last.Name != "absent" {
		t.Fatalf("a required plugin that is not installed must be listed: %+v", last)
	}
}

func TestInstallCommand(t *testing.T) {
	if got := InstallCommand("kube", ""); got != "orbit plugin install <owner>/kube" {
		t.Errorf("without a source: %q", got)
	}
	if got := InstallCommand("kube", "acme/orbit-kube@v1"); got != "orbit plugin install acme/orbit-kube@v1" {
		t.Errorf("with a source: %q", got)
	}
}

func TestClean(t *testing.T) {
	for in, want := range map[string]string{
		"plain":                 "plain",
		"a\tb":                  "a  b",
		"x\ny\r\nz":             "xyz",
		"\x1b]0;title\x07ok":    "ok",
		"\x1b[31mred\x1b[0m":    "red",
		"\x1b[1;38;5;9mx\x1b[K": "x",
		"ñ ✓ 日本":                "ñ ✓ 日本",
	} {
		if got := Clean(in); got != want {
			t.Errorf("Clean(%q) = %q, want %q", in, got, want)
		}
	}
}
