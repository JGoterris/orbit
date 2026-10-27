//go:build !windows

package ipc

import (
	"strings"
	"sync"
	"testing"

	"github.com/JGoterris/orbit/internal/core/supervisor"
	"github.com/JGoterris/orbit/internal/plugins"
	"github.com/JGoterris/orbit/internal/plugins/plugintest"
	"github.com/JGoterris/orbit/pluginsdk/protocol"
)

// pluginRemote opens a project with the demo plugin in an in-process "daemon" and connects a client to it, the way
// the TUI does.
func pluginRemote(t *testing.T) (*Remote, *plugins.Manager) {
	t.Helper()
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if _, err := plugins.Link(plugintest.Demo(t)); err != nil {
		t.Fatal(err)
	}
	cfg := cfgOf("withplugin", svc("plain", "sleep 30"))
	cfg.Root = t.TempDir()
	mgr := plugins.ForProject(cfg, "test")
	sup := supervisor.New(cfg, supervisor.Options{StateDir: t.TempDir(), Plugins: mgr})
	if err := sup.Init(); err != nil {
		t.Fatal(err)
	}
	srv := NewServer(sup, ServerOptions{Version: "test"})
	if started, err := srv.Start(); err != nil || !started {
		t.Fatalf("server: %v %v", started, err)
	}
	r, err := ConnectRemote(srv.Path())
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Init(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		r.client.Close()
		srv.Close()
		sup.Dispose()
	})
	return r, mgr
}

func pluginStatus(r *Remote) plugins.Status {
	for _, i := range r.Plugins().Plugins() {
		if i.Name == "demo" {
			return i.Status
		}
	}
	return ""
}

func TestPluginsOverTheSocket(t *testing.T) {
	r, _ := pluginRemote(t)
	until(t, "the plugin to be running for the client", func() bool { return pluginStatus(r) == plugins.StatusRunning })

	infos := r.Plugins().Plugins()
	if len(infos) != 1 || infos[0].Contributes.Views[0].ID != "items" || infos[0].Version != "0.1.0" {
		t.Fatalf("the client must see what the plugin contributes: %+v", infos)
	}

	d, err := r.Plugins().Data("demo", protocol.ViewDataParams{View: "items", Pane: "list"})
	if err != nil || d.List == nil || len(d.List.Items) != 3 {
		t.Fatalf("data = %+v, %v", d, err)
	}
	if _, err := r.Plugins().Data("demo", protocol.ViewDataParams{View: "items", Pane: "nope"}); err == nil {
		t.Fatal("a pane that is not in the manifest must be refused by the daemon")
	}
	if _, err := r.Plugins().Data("ghost", protocol.ViewDataParams{View: "items", Pane: "list"}); err == nil {
		t.Fatal("an unknown plugin must be refused")
	}

	secs, err := r.Plugins().Sections("demo", protocol.SectionsParams{Service: "plain", Section: "info"})
	if err != nil || secs[0].Fields[0].Value != "hi plain" {
		t.Fatalf("sections = %+v, %v", secs, err)
	}
}

func TestActionsAndBadgesOverTheSocket(t *testing.T) {
	r, _ := pluginRemote(t)
	until(t, "the plugin", func() bool { return pluginStatus(r) == plugins.StatusRunning })
	var mu sync.Mutex
	var kinds []string
	r.Plugins().OnUpdate(func(u plugins.Update) { mu.Lock(); kinds = append(kinds, u.Kind); mu.Unlock() })

	res, err := r.Plugins().Invoke("demo", protocol.InvokeParams{
		Action: "bump", Inputs: map[string]string{"level": "major"},
		Context: protocol.Context{Selection: map[string]string{"list": "gamma"}},
	})
	if err != nil || res.Toast == nil || res.Toast.Text != "bumped gamma (major)" {
		t.Fatalf("invoke = %+v, %v", res, err)
	}
	if _, err := r.Plugins().Invoke("demo", protocol.InvokeParams{Action: "bump"}); err == nil || !strings.Contains(err.Error(), "nothing selected") {
		t.Fatalf("the error of the plugin must reach the client: %v", err)
	}

	until(t, "the badge the plugin set to reach the client", func() bool {
		for _, b := range r.Plugins().Badges()["header"] {
			if b.Text == "bumps 1" && b.Plugin == "demo" && b.Action == "hello" {
				return true
			}
		}
		return false
	})
	mu.Lock()
	defer mu.Unlock()
	found := false
	for _, k := range kinds {
		found = found || k == plugins.UpdateBadges
	}
	if !found {
		t.Fatalf("the client must be told about the badges: %v", kinds)
	}
}

func TestStatusOfAPluginThatDiesReachesTheClient(t *testing.T) {
	r, mgr := pluginRemote(t)
	until(t, "the plugin", func() bool { return pluginStatus(r) == plugins.StatusRunning })
	seen := map[plugins.Status]bool{}
	var mu sync.Mutex
	r.Plugins().OnUpdate(func(u plugins.Update) {
		if u.Kind == plugins.UpdateStatus {
			mu.Lock()
			seen[pluginStatus(r)] = true
			mu.Unlock()
		}
	})
	mgr.KillSync()
	until(t, "the client to see the plugin down", func() bool { mu.Lock(); defer mu.Unlock(); return seen[plugins.StatusStarting] })
	until(t, "the plugin to come back for the client", func() bool { return pluginStatus(r) == plugins.StatusRunning })
	if _, err := r.Plugins().Data("demo", protocol.ViewDataParams{View: "items", Pane: "list"}); err != nil {
		t.Fatalf("after the restart it must answer again: %v", err)
	}
}
