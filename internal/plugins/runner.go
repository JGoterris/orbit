package plugins

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/JGoterris/orbit/internal/config"
	"github.com/JGoterris/orbit/internal/core/logs"
	"github.com/JGoterris/orbit/internal/core/runner"
	"github.com/JGoterris/orbit/internal/state"
	"github.com/JGoterris/orbit/pluginsdk/protocol"
)

// readyWait is how long a service waits for its plugin to be up: the supervisor starts the plugins and the
// services together, and the services should not fail just because the plugin takes a moment to say hello.
const readyWait = 10 * time.Second

// attachWait is the same for the question "is it still running?" asked when orbit opens: shorter, because the
// answer is almost always no.
const attachWait = 5 * time.Second

func (m *Manager) typeOwner(typ string) (string, bool) {
	for _, s := range m.opts.Specs {
		for _, t := range s.Plugin.Manifest.Contributes.ServiceTypes {
			if t.Name == typ && m.procs[s.Plugin.Manifest.Name] != nil {
				return s.Plugin.Manifest.Name, true
			}
		}
	}
	return "", false
}

func (m *Manager) waitRunning(plugin string, d time.Duration) error {
	p := m.procs[plugin]
	if p == nil {
		return fmt.Errorf("plugin %s has no process", plugin)
	}
	deadline := time.Now().Add(d)
	for {
		st, reason := p.Status()
		switch st {
		case StatusRunning:
			return nil
		case StatusFailed:
			return fmt.Errorf("plugin %s failed: %s", plugin, reason)
		case StatusStopped:
			return fmt.Errorf("plugin %s is stopped", plugin)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("plugin %s did not start in %s", plugin, d)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// NewRunner is the runner of a service whose type a plugin provides. When none of the active plugins does, the
// service fails to start with the reason, instead of the project failing to open.
func (m *Manager) NewRunner(svc *config.ServiceConfig, cb runner.Callbacks) runner.Runner {
	plugin, ok := m.typeOwner(string(svc.Type))
	if !ok {
		return &missingRunner{typ: string(svc.Type)}
	}
	return &serviceRunner{m: m, plugin: plugin, svc: svc, cb: cb}
}

type serviceRunner struct {
	m      *Manager
	plugin string
	svc    *config.ServiceConfig
	cb     runner.Callbacks

	mu       sync.Mutex
	attached bool
}

func (r *serviceRunner) params() protocol.ServiceParams {
	p := protocol.ServiceParams{Name: r.svc.Name, Type: string(r.svc.Type), Cwd: r.svc.Cwd, StopTimeoutMs: r.svc.StopTimeout}
	if len(r.svc.With) > 0 {
		p.With, _ = json.Marshal(r.svc.With)
	}
	return p
}

func (r *serviceRunner) follow() {
	r.m.mu.Lock()
	r.m.services[r.svc.Name] = r
	r.m.mu.Unlock()
	r.mu.Lock()
	r.attached = true
	r.mu.Unlock()
}

func (r *serviceRunner) unfollow() {
	r.m.mu.Lock()
	if r.m.services[r.svc.Name] == r {
		delete(r.m.services, r.svc.Name)
	}
	r.m.mu.Unlock()
	r.mu.Lock()
	r.attached = false
	r.mu.Unlock()
}

func (r *serviceRunner) Start() error {
	if err := r.m.waitRunning(r.plugin, readyWait); err != nil {
		return err
	}
	// registered first: what the service prints while it starts must not be lost
	r.follow()
	timeout := time.Duration(r.svc.StartTimeout) * time.Millisecond
	if timeout <= 0 {
		timeout = ActionTimeout
	}
	if err := r.m.procs[r.plugin].Call(protocol.MethodServiceStart, r.params(), nil, timeout); err != nil {
		r.unfollow()
		return err
	}
	return nil
}

func (r *serviceRunner) Attach(*state.SavedService) (bool, error) {
	if err := r.m.waitRunning(r.plugin, attachWait); err != nil {
		return false, nil // nothing to follow: the plugin is not there, and it is what kept the service up
	}
	var res protocol.AttachResult
	if err := r.m.procs[r.plugin].Call(protocol.MethodServiceAttach, r.params(), &res, DefaultTimeout); err != nil || !res.Running {
		return false, err
	}
	r.follow()
	return true, nil
}

func (r *serviceRunner) Stop(timeoutMs int64) error {
	defer r.unfollow()
	timeout := time.Duration(timeoutMs)*time.Millisecond + 5*time.Second
	return r.m.procs[r.plugin].Call(protocol.MethodServiceStop, r.params(), nil, timeout)
}

func (r *serviceRunner) Release()            { r.unfollow() }
func (r *serviceRunner) KillSync()           {}
func (r *serviceRunner) Pid() int            { return 0 }
func (r *serviceRunner) StartTime() int64    { return 0 }
func (r *serviceRunner) ContainerID() string { return "" }

func (r *serviceRunner) output(stream, text string) {
	s := logs.Stdout
	if stream == "stderr" {
		s = logs.Stderr
	}
	r.cb.Log(s, text)
}

func (r *serviceRunner) exited(code *int, reason string) {
	r.unfollow()
	if reason != "" {
		r.cb.Log(logs.System, reason)
	}
	r.cb.Exit(code, "")
}

type missingRunner struct{ typ string }

func (r *missingRunner) Start() error {
	return fmt.Errorf("no plugin provides the service type %q: orbit plugin ls (is it installed and enabled for this project?)", r.typ)
}
func (r *missingRunner) Attach(*state.SavedService) (bool, error) { return false, nil }
func (r *missingRunner) Stop(int64) error                         { return nil }
func (r *missingRunner) Release()                                 {}
func (r *missingRunner) KillSync()                                {}
func (r *missingRunner) Pid() int                                 { return 0 }
func (r *missingRunner) StartTime() int64                         { return 0 }
func (r *missingRunner) ContainerID() string                      { return "" }

// routeService hands what a plugin says about one of its services to the runner that follows it.
func (m *Manager) routeService(msg protocol.Message) {
	switch msg.Method {
	case protocol.NotifyServiceLog:
		var l protocol.ServiceLog
		if json.Unmarshal(msg.Params, &l) != nil {
			return
		}
		if r := m.follower(l.Name); r != nil {
			r.output(l.Stream, Clean(l.Text))
		}
	case protocol.NotifyExited:
		var e protocol.ServiceExited
		if json.Unmarshal(msg.Params, &e) != nil {
			return
		}
		if r := m.follower(e.Name); r != nil {
			r.exited(e.Code, Clean(e.Reason))
		}
	}
}

func (m *Manager) follower(service string) *serviceRunner {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.services[service]
}

// failServices tells the supervisor that the services of a plugin that died are down: nothing else will.
func (m *Manager) failServices(plugin, reason string) {
	m.mu.Lock()
	var dead []*serviceRunner
	for _, r := range m.services {
		if r.plugin == plugin {
			dead = append(dead, r)
		}
	}
	m.mu.Unlock()
	for _, r := range dead {
		r.exited(nil, reason)
	}
}

// ValidateServices asks the plugins that provide the service types of cfg to check the `with:` of each service. It
// starts them for that, and stops them. Projects with no such services cost nothing.
func ValidateServices(cfg *config.OrbitConfig, version string) []string {
	var mine []*config.ServiceConfig
	for _, name := range cfg.Order {
		if svc := cfg.Services[name]; svc != nil && !config.IsCoreType(svc.Type) {
			mine = append(mine, svc)
		}
	}
	if len(mine) == 0 {
		return nil
	}
	m := ForProject(cfg, version)
	m.Start()
	defer m.Stop()
	var errs []string
	for _, svc := range mine {
		plugin, ok := m.typeOwner(string(svc.Type))
		if !ok {
			errs = append(errs, fmt.Sprintf("services.%s: no active plugin provides the type %q", svc.Name, svc.Type))
			continue
		}
		if err := m.waitRunning(plugin, readyWait); err != nil {
			errs = append(errs, fmt.Sprintf("services.%s: %v", svc.Name, err))
			continue
		}
		r := &serviceRunner{m: m, plugin: plugin, svc: svc}
		if err := m.procs[plugin].Call(protocol.MethodServiceCheck, r.params(), nil, DefaultTimeout); err != nil {
			errs = append(errs, fmt.Sprintf("services.%s.with: %v", svc.Name, err))
		}
	}
	return errs
}
