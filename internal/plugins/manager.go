package plugins

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/JGoterris/orbit/pluginsdk/protocol"
)

// Host is what the TUI and the socket server need from the plugins: implemented by Manager (the plugins run in
// this process) and by ipc.Remote (they run in an `orbit daemon`). The calls block while the plugin answers, so the
// TUI makes them off its loop.
type Host interface {
	Plugins() []Info
	Data(plugin string, p protocol.ViewDataParams) (protocol.ViewData, error)
	Invoke(plugin string, p protocol.InvokeParams) (protocol.InvokeResult, error)
	Options(plugin string, p protocol.OptionsParams) ([]protocol.Option, error)
	Sections(plugin string, p protocol.SectionsParams) ([]protocol.Section, error)
	// Badges are the ones the plugins set, by target: "service:<name>" or "header"
	Badges() map[string][]TaggedBadge
	// OnUpdate calls fn when a plugin asks for a redraw, shows a toast, changes its badges or its status
	OnUpdate(fn func(Update)) (cancel func())
}

type Info struct {
	Name        string               `json:"name"`
	Version     string               `json:"version"`
	Description string               `json:"description,omitempty"`
	Status      Status               `json:"status"`
	Error       string               `json:"error,omitempty"`
	Contributes protocol.Contributes `json:"contributes"`
	// Missing: orbit.yaml asks for the plugin and it is not installed
	Missing bool `json:"missing,omitempty"`
	// Install is the command that installs a Missing plugin
	Install string `json:"install,omitempty"`
}

// InstallCommand is what to run to install the plugin name, given where orbit.yaml says it comes from (from may be
// empty: then only the owner is unknown).
func InstallCommand(name, from string) string {
	if from == "" {
		from = "<owner>/" + name
	}
	return "orbit plugin install " + from
}

type TaggedBadge struct {
	Plugin string `json:"plugin"`
	protocol.Badge
}

const (
	UpdateInvalidate = "invalidate"
	UpdateToast      = "toast"
	UpdateBadges     = "badges"
	UpdateStatus     = "status"
)

type Update struct {
	Kind       string               `json:"kind"`
	Plugin     string               `json:"plugin"`
	Invalidate *protocol.Invalidate `json:"invalidate,omitempty"`
	Toast      *protocol.Toast      `json:"toast,omitempty"`
}

type Spec struct {
	Plugin   *Plugin
	Settings json.RawMessage
}

type Options struct {
	Version    string
	Project    string
	ProjectDir string
	Services   []protocol.ServiceRef
	Specs      []Spec
	// Missing are plugins the project asks for that are not installed
	Missing []string
	// MissingFrom is where orbit.yaml says each of them is installed from (`from:`)
	MissingFrom map[string]string
}

type Manager struct {
	opts  Options
	procs map[string]*Process
	order []string

	mu      sync.Mutex
	badges  map[string]map[string][]protocol.Badge // plugin → target → badges
	subs    map[int]func(Update)
	nextSub int
	// services are the runners that follow a service the plugins run, by service name
	services map[string]*serviceRunner
}

var _ Host = (*Manager)(nil)

// NewManager prepares the plugins; Start launches them.
func NewManager(o Options) *Manager {
	m := &Manager{opts: o, procs: map[string]*Process{}, badges: map[string]map[string][]protocol.Badge{}, subs: map[int]func(Update){}, services: map[string]*serviceRunner{}}
	for _, s := range o.Specs {
		name := s.Plugin.Manifest.Name
		m.order = append(m.order, name)
		if s.Plugin.Manifest.Run == nil {
			continue
		}
		init := protocol.InitializeParams{
			API: protocol.API, OrbitVersion: o.Version, Project: o.Project, ProjectDir: o.ProjectDir,
			ConfigDir: ConfigDir(name), StateDir: StateDir(name), Settings: s.Settings, Services: o.Services,
		}
		m.procs[name] = NewProcess(s.Plugin, init,
			func(msg protocol.Message) { m.fromPlugin(name, msg) },
			func(st Status, reason string) { m.statusChanged(name, st, reason) })
	}
	return m
}

func (m *Manager) Start() {
	for _, name := range m.order {
		if p := m.procs[name]; p != nil {
			p.Start()
		}
	}
}

func (m *Manager) Stop() {
	var wg sync.WaitGroup
	for _, p := range m.procs {
		wg.Add(1)
		go func() { defer wg.Done(); p.Stop() }()
	}
	wg.Wait()
}

func (m *Manager) plugin(name string) *Plugin {
	for _, s := range m.opts.Specs {
		if s.Plugin.Manifest.Name == name {
			return s.Plugin
		}
	}
	return nil
}

func (m *Manager) Plugins() []Info {
	var out []Info
	for _, name := range m.order {
		pl := m.plugin(name)
		info := Info{Name: name, Version: pl.Manifest.Version, Description: pl.Manifest.Description, Status: StatusStopped, Contributes: pl.Manifest.Contributes}
		if p := m.procs[name]; p != nil {
			info.Status, info.Error = p.Status()
		} else {
			info.Status = StatusRunning // nothing to run: it only has a CLI
		}
		out = append(out, info)
	}
	for _, name := range m.opts.Missing {
		install := InstallCommand(name, m.opts.MissingFrom[name])
		out = append(out, Info{Name: name, Status: StatusFailed, Error: "not installed: " + install, Install: install, Missing: true})
	}
	return out
}

func (m *Manager) running(name string) (*Process, *Plugin, error) {
	pl := m.plugin(name)
	if pl == nil {
		return nil, nil, fmt.Errorf("no plugin %q in this project", name)
	}
	p := m.procs[name]
	if p == nil {
		return nil, nil, fmt.Errorf("plugin %q has no process", name)
	}
	return p, pl, nil
}

func (m *Manager) Data(name string, req protocol.ViewDataParams) (protocol.ViewData, error) {
	var out protocol.ViewData
	p, pl, err := m.running(name)
	if err != nil {
		return out, err
	}
	if !hasPane(pl.Manifest, req.View, req.Pane) {
		return out, fmt.Errorf("plugin %q has no pane %s/%s", name, req.View, req.Pane)
	}
	if err := p.Call(protocol.MethodViewData, req, &out, DefaultTimeout); err != nil {
		return out, err
	}
	CleanViewData(&out)
	return out, nil
}

func hasPane(m *protocol.Manifest, view, pane string) bool {
	for _, v := range m.Contributes.Views {
		if v.ID == view {
			return slices.ContainsFunc(v.Panes, func(p protocol.Pane) bool { return p.ID == pane })
		}
	}
	return false
}

func (m *Manager) Invoke(name string, req protocol.InvokeParams) (protocol.InvokeResult, error) {
	var out protocol.InvokeResult
	p, pl, err := m.running(name)
	if err != nil {
		return out, err
	}
	if !slices.ContainsFunc(pl.Manifest.Contributes.Actions, func(a protocol.Action) bool { return a.ID == req.Action }) {
		return out, fmt.Errorf("plugin %q has no action %q", name, req.Action)
	}
	// an action may take a while (a merge, a rerun): it gets longer than a data request
	if err := p.Call(protocol.MethodActionInvoke, req, &out, ActionTimeout); err != nil {
		return out, err
	}
	CleanToast(out.Toast)
	return out, nil
}

func (m *Manager) Options(name string, req protocol.OptionsParams) ([]protocol.Option, error) {
	p, pl, err := m.running(name)
	if err != nil {
		return nil, err
	}
	if !slices.ContainsFunc(pl.Manifest.Contributes.Actions, func(a protocol.Action) bool { return a.ID == req.Action }) {
		return nil, fmt.Errorf("plugin %q has no action %q", name, req.Action)
	}
	var out []protocol.Option
	if err := p.Call(protocol.MethodActionOptions, req, &out, DefaultTimeout); err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Value, out[i].Label = Clean(out[i].Value), Clean(out[i].Label)
	}
	return out, nil
}

func (m *Manager) Sections(name string, req protocol.SectionsParams) ([]protocol.Section, error) {
	p, pl, err := m.running(name)
	if err != nil {
		return nil, err
	}
	if !slices.ContainsFunc(pl.Manifest.Contributes.ServiceSections, func(s protocol.SectionDef) bool { return s.ID == req.Section }) {
		return nil, fmt.Errorf("plugin %q has no service section %q", name, req.Section)
	}
	var out []protocol.Section
	if err := p.Call(protocol.MethodSections, req, &out, DefaultTimeout); err != nil {
		return nil, err
	}
	return CleanSections(out), nil
}

func (m *Manager) Badges() map[string][]TaggedBadge {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[string][]TaggedBadge{}
	for _, name := range m.order {
		for target, list := range m.badges[name] {
			for _, b := range list {
				out[target] = append(out[target], TaggedBadge{Plugin: name, Badge: b})
			}
		}
	}
	return out
}

// OnUpdate subscribes to what the plugins do on their own. fn runs on the reader of a plugin: it must not block.
func (m *Manager) OnUpdate(fn func(Update)) (cancel func()) {
	m.mu.Lock()
	id := m.nextSub
	m.nextSub++
	m.subs[id] = fn
	m.mu.Unlock()
	return func() {
		m.mu.Lock()
		delete(m.subs, id)
		m.mu.Unlock()
	}
}

func (m *Manager) emit(u Update) {
	m.mu.Lock()
	fns := make([]func(Update), 0, len(m.subs))
	for _, f := range m.subs {
		fns = append(fns, f)
	}
	m.mu.Unlock()
	for _, f := range fns {
		f(u)
	}
}

func (m *Manager) Notify(ev protocol.Event) {
	for name, p := range m.procs {
		if slices.Contains(m.plugin(name).Manifest.Events, ev.Kind) {
			p.Notify(protocol.NotifyEvent, ev)
		}
	}
}

func (m *Manager) Wants(kind string) bool {
	for _, s := range m.opts.Specs {
		if m.procs[s.Plugin.Manifest.Name] != nil && slices.Contains(s.Plugin.Manifest.Events, kind) {
			return true
		}
	}
	return false
}

// KillSync kills every plugin without waiting: the last resort when orbit exits.
func (m *Manager) KillSync() {
	for _, p := range m.procs {
		p.KillSync()
	}
}

func (m *Manager) statusChanged(name string, st Status, reason string) {
	if st == StatusFailed || st == StatusStopped || (st == StatusStarting && reason != "") {
		// the process died: the services it ran died with it
		m.failServices(name, "the plugin "+name+" stopped: "+reason)
	}
	if st != StatusRunning && st != StatusStarting {
		// a dead plugin cannot take its badges back
		m.mu.Lock()
		had := len(m.badges[name]) > 0
		delete(m.badges, name)
		m.mu.Unlock()
		if had {
			m.emit(Update{Kind: UpdateBadges, Plugin: name})
		}
	}
	m.emit(Update{Kind: UpdateStatus, Plugin: name})
}

// fromPlugin handles what a plugin sends on its own.
func (m *Manager) fromPlugin(name string, msg protocol.Message) {
	pl := m.plugin(name)
	switch msg.Method {
	case protocol.NotifyInvalidate:
		var inv protocol.Invalidate
		if json.Unmarshal(msg.Params, &inv) == nil {
			m.emit(Update{Kind: UpdateInvalidate, Plugin: name, Invalidate: &inv})
		}
	case protocol.NotifyToast:
		var t protocol.Toast
		if json.Unmarshal(msg.Params, &t) == nil && strings.TrimSpace(t.Text) != "" {
			CleanToast(&t)
			m.emit(Update{Kind: UpdateToast, Plugin: name, Toast: &t})
		}
	case protocol.NotifyBadges:
		var set protocol.BadgesSet
		if json.Unmarshal(msg.Params, &set) != nil || !m.validTarget(pl, set.Target) {
			return
		}
		items := cleanBadges(set.Items)
		m.mu.Lock()
		if m.badges[name] == nil {
			m.badges[name] = map[string][]protocol.Badge{}
		}
		if len(items) == 0 {
			delete(m.badges[name], set.Target)
		} else {
			m.badges[name][set.Target] = items
		}
		m.mu.Unlock()
		m.emit(Update{Kind: UpdateBadges, Plugin: name})
	case protocol.NotifyServiceLog, protocol.NotifyExited:
		m.routeService(msg)
	}
}

// validTarget: a plugin may only badge the places its manifest says it uses.
func (m *Manager) validTarget(pl *Plugin, target string) bool {
	c := pl.Manifest.Contributes
	switch {
	case target == "header":
		return c.HeaderBadges
	case strings.HasPrefix(target, "service:"):
		return c.ServiceColumn != nil
	}
	return false
}
