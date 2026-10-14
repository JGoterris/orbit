package ipc

import (
	"encoding/json"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/JGoterris/orbit/internal/config"
	"github.com/JGoterris/orbit/internal/core/logs"
	"github.com/JGoterris/orbit/internal/core/resources"
	"github.com/JGoterris/orbit/internal/core/supervisor"
	"github.com/JGoterris/orbit/internal/graph"
)

// historyLines is everything the daemon has in its log rings.
const historyLines = 20_000

// Remote is a supervisor whose services run elsewhere (an `orbit daemon`, or any orbit with a socket): it mirrors
// the state and the logs it receives, so the UI can read it synchronously, and forwards every command. It reports
// changes like a real Supervisor, and Close when the connection is lost.
type Remote struct {
	client *Client
	// Hello is what the daemon said about itself
	Hello Hello

	logStore *logs.Store

	mu         sync.Mutex
	cfg        *config.OrbitConfig
	deps       graph.DepMap
	dependents map[string][]string
	order      []string
	pending    *supervisor.PendingConfig
	states     map[string]supervisor.ServiceState
	lastSeq    int

	changeBus  supervisor.Bus[string]
	configBus  supervisor.Bus[config.ConfigDiff]
	pendingBus supervisor.Bus[*supervisor.PendingConfig]
	closeBus   supervisor.Bus[struct{}]
}

var _ supervisor.Like = (*Remote)(nil)

// ConnectRemote connects to a socket, checks the protocol and returns a supervisor that has not loaded its state
// yet (Init does).
func ConnectRemote(path string) (*Remote, error) {
	client, err := Connect(path, 2*time.Second)
	if err != nil {
		return nil, err
	}
	var hello Hello
	if err := client.Call(MethodHello, nil, &hello); err != nil {
		client.Close()
		return nil, err
	}
	if hello.Protocol != Protocol {
		client.Close()
		return nil, fmt.Errorf("the running orbit (v%s) speaks protocol %d, this one speaks %d: run `orbit down` and try again",
			hello.Version, hello.Protocol, Protocol)
	}
	r := &Remote{client: client, Hello: hello, logStore: logs.NewStore(0, 0), states: map[string]supervisor.ServiceState{}}
	r.cfg = hello.Config
	r.pending = hello.Pending
	r.refreshGraph()
	for _, n := range r.cfg.Order {
		r.states[n] = supervisor.ServiceState{Name: n, Status: supervisor.Stopped}
	}
	client.OnClose(func() {
		r.changeBus.Emit("")
		r.closeBus.Emit(struct{}{})
	})
	return r, nil
}

func (r *Remote) refreshGraph() {
	r.deps = r.cfg.DepMap()
	r.dependents = graph.DependentsMap(r.deps)
	r.order = graph.TopoOrder(r.deps, nil)
}

// Version is the version of the orbit that holds the services.
func (r *Remote) Version() string { return r.Hello.Version }

// Connected reports whether the connection is alive.
func (r *Remote) Connected() bool { return !r.client.Closed() }

// OnClose calls fn once when the connection is lost.
func (r *Remote) OnClose(fn func()) (cancel func()) { return r.closeBus.On(func(struct{}) { fn() }) }

// ---------------------------------------------------------------- queries

func (r *Remote) Config() *config.OrbitConfig { r.mu.Lock(); defer r.mu.Unlock(); return r.cfg }
func (r *Remote) LogStore() *logs.Store       { return r.logStore }
func (r *Remote) Deps() graph.DepMap          { r.mu.Lock(); defer r.mu.Unlock(); return r.deps }
func (r *Remote) Dependents() map[string][]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.dependents
}
func (r *Remote) Order() []string { r.mu.Lock(); defer r.mu.Unlock(); return slices.Clone(r.order) }

// StateDir is the daemon's state dir, so exports and the like land next to the logs it writes.
func (r *Remote) StateDir() string { return r.Hello.StateDir }

func (r *Remote) Names() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.cfg.Order)
}

func (r *Remote) Service(name string) *config.ServiceConfig {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.cfg.Services[name]
}

func (r *Remote) State(name string) supervisor.ServiceState {
	r.mu.Lock()
	defer r.mu.Unlock()
	if st, ok := r.states[name]; ok {
		return st
	}
	return supervisor.ServiceState{Name: name}
}

func (r *Remote) Snapshot() []supervisor.ServiceState {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]supervisor.ServiceState, 0, len(r.cfg.Order))
	for _, n := range r.cfg.Order {
		out = append(out, r.states[n])
	}
	return out
}

func (r *Remote) HistoryOf(name string, sinceMs int64) ([]resources.Bucket, error) {
	p := map[string]any{"service": name}
	if sinceMs > 0 {
		p["since"] = sinceMs
	}
	var out []resources.Bucket
	return out, r.client.Call(MethodHistory, p, &out)
}

func (r *Remote) IsReady(name string) bool {
	svc := r.Service(name)
	return svc != nil && supervisor.IsReadyStatus(r.State(name).Status, svc.Oneshot)
}

func (r *Remote) IsUp(name string) bool {
	st := r.State(name).Status
	return supervisor.IsUp(st) || st == supervisor.Waiting
}

func (r *Remote) IsAdopted(name string) bool { return r.State(name).Adopted && r.IsUp(name) }

func (r *Remote) RunningCount() int {
	n := 0
	for _, name := range r.Names() {
		if r.IsUp(name) || r.State(name).Status == supervisor.Stopping {
			n++
		}
	}
	return n
}

func (r *Remote) OwnedRunningCount() int {
	n := 0
	for _, name := range r.Names() {
		st := r.State(name)
		if (r.IsUp(name) || st.Status == supervisor.Stopping) && !st.Adopted {
			n++
		}
	}
	return n
}

func (r *Remote) Pending() *supervisor.PendingConfig {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.pending
}

func (r *Remote) OnChange(fn func(string)) func()            { return r.changeBus.On(fn) }
func (r *Remote) OnConfig(fn func(config.ConfigDiff)) func() { return r.configBus.On(fn) }
func (r *Remote) OnConfigPending(fn func(*supervisor.PendingConfig)) func() {
	return r.pendingBus.On(fn)
}

// ---------------------------------------------------------------- lifecycle

// Init subscribes and loads the current state and the log history.
func (r *Remote) Init() error {
	var mu sync.Mutex
	var buffered []Message
	loading := true
	r.client.OnNotification(func(m Message) {
		mu.Lock()
		if loading {
			buffered = append(buffered, m)
			mu.Unlock()
			return
		}
		mu.Unlock()
		r.apply(m)
	})
	// the snapshot comes with the subscription, so every notification after it is newer than the snapshot
	var sub struct {
		Snapshot []supervisor.ServiceState `json:"snapshot"`
	}
	if err := r.client.Call(MethodSubscribe, map[string]any{"states": true, "logs": true}, &sub); err != nil {
		return err
	}
	r.mu.Lock()
	for _, st := range sub.Snapshot {
		r.states[st.Name] = st
	}
	r.mu.Unlock()
	var history []logs.Line
	if err := r.client.Call(MethodLogs, map[string]any{"limit": historyLines}, &history); err != nil {
		return err
	}
	for _, l := range history {
		r.ingest(l)
	}
	mu.Lock()
	loading = false
	pending := buffered
	buffered = nil
	mu.Unlock()
	for _, m := range pending {
		r.apply(m)
	}
	r.changeBus.Emit("")
	return nil
}

func (r *Remote) ingest(l logs.Line) {
	r.mu.Lock()
	if l.Seq <= r.lastSeq { // already in the history
		r.mu.Unlock()
		return
	}
	r.lastSeq = l.Seq
	r.mu.Unlock()
	r.logStore.Ingest(l)
}

func (r *Remote) apply(m Message) {
	switch m.Method {
	case NotifyState:
		var p struct {
			Name  string                  `json:"name"`
			State supervisor.ServiceState `json:"state"`
		}
		if json.Unmarshal(m.Params, &p) != nil {
			return
		}
		r.mu.Lock()
		r.states[p.Name] = p.State
		r.mu.Unlock()
		r.changeBus.Emit(p.Name)
	case NotifyConfig:
		var p struct {
			Config *config.OrbitConfig `json:"config"`
		}
		if json.Unmarshal(m.Params, &p) != nil || p.Config == nil {
			return
		}
		r.setConfig(p.Config)
	case NotifyConfigPending:
		var p struct {
			Pending *supervisor.PendingConfig `json:"pending"`
		}
		if json.Unmarshal(m.Params, &p) != nil {
			return
		}
		r.mu.Lock()
		r.pending = p.Pending
		r.mu.Unlock()
		r.pendingBus.Emit(p.Pending)
		r.changeBus.Emit("")
	case NotifyLog:
		var l logs.Line
		if json.Unmarshal(m.Params, &l) == nil {
			r.ingest(l)
		}
	case NotifyCleared:
		var p struct {
			Service string `json:"service"`
		}
		json.Unmarshal(m.Params, &p)
		r.logStore.Clear(p.Service)
		r.changeBus.Emit(p.Service)
	}
}

// setConfig: the daemon applied a new config: services come and go, the graph is recomputed.
func (r *Remote) setConfig(cfg *config.OrbitConfig) {
	r.mu.Lock()
	prev := r.cfg
	r.cfg = cfg
	r.refreshGraph()
	for name := range prev.Services {
		if cfg.Services[name] == nil {
			delete(r.states, name)
		}
	}
	for _, name := range cfg.Order {
		if _, ok := r.states[name]; !ok {
			r.states[name] = supervisor.ServiceState{Name: name, Status: supervisor.Stopped}
		}
	}
	r.pending = nil
	r.mu.Unlock()
	r.configBus.Emit(config.ConfigDiff{})
	r.changeBus.Emit("")
}

func (r *Remote) Reload(next *config.OrbitConfig) (config.ConfigDiff, error) {
	var p any = map[string]any{}
	if next != nil {
		p = map[string]any{"config": next}
	}
	var res struct {
		Diff config.ConfigDiff `json:"diff"`
	}
	err := r.client.Call(MethodReload, p, &res)
	return res.Diff, err
}

// ---------------------------------------------------------------- commands

func (r *Remote) ok(method string, names []string) bool {
	var res struct {
		OK bool `json:"ok"`
	}
	if err := r.client.Call(method, map[string]any{"services": names}, &res); err != nil {
		return false
	}
	return res.OK
}

func (r *Remote) Start(name string) bool { return r.ok(MethodStart, []string{name}) }

// Stop stops a service (and its dependents first: the daemon decides).
func (r *Remote) Stop(name string, _ supervisor.StopOptions) { r.ok(MethodStop, []string{name}) }
func (r *Remote) Restart(name string) bool                   { return r.ok(MethodRestart, []string{name}) }
func (r *Remote) Toggle(name string)                         { r.ok(MethodToggle, []string{name}) }

func (r *Remote) StartMany(names []string) {
	if len(names) > 0 {
		r.ok(MethodStart, names)
	}
}

func (r *Remote) StartAll() { r.client.Call(MethodStartAll, nil, nil) }
func (r *Remote) StopAll()  { r.client.Call(MethodStopAll, nil, nil) }

// ToggleWatch asks the daemon to pause / resume watching; the state change that follows updates the mirror.
func (r *Remote) ToggleWatch(name string) string {
	w := r.State(name).Watch
	if w == "" {
		return ""
	}
	go r.client.Call(MethodToggleWatch, map[string]any{"service": name}, nil)
	if w == "paused" {
		return "active"
	}
	return "paused"
}

func (r *Remote) ClearLogs(name string) {
	r.logStore.Clear(name)
	r.changeBus.Emit(name)
	p := map[string]any{}
	if name != "" {
		p["service"] = name
	}
	go r.client.Call(MethodClearLogs, p, nil)
}

// Dispose asks the daemon to stop everything and quit, and waits for it to hang up.
func (r *Remote) Dispose() {
	if r.client.Closed() {
		return
	}
	if err := r.client.Call(MethodShutdown, map[string]any{"how": "stop"}, nil); err != nil {
		return
	}
	select {
	case <-r.client.Done():
	case <-time.After(30 * time.Second):
	}
}

// Detach disconnects; the services keep running under the daemon.
func (r *Remote) Detach() { r.client.Close() }

// KillAllSync only disconnects: a remote supervisor must not touch the daemon's processes.
func (r *Remote) KillAllSync() { r.client.Close() }
