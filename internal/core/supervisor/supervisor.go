// Package supervisor runs the services of a project: it starts them in dependency order, health-checks them,
// restarts them, follows their output and resources, and applies changes to orbit.yaml to what is running.
package supervisor

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/JGoterris/orbit/internal/config"
	"github.com/JGoterris/orbit/internal/core/health"
	"github.com/JGoterris/orbit/internal/core/hooks"
	"github.com/JGoterris/orbit/internal/core/logs"
	"github.com/JGoterris/orbit/internal/core/metrics"
	"github.com/JGoterris/orbit/internal/core/notify"
	"github.com/JGoterris/orbit/internal/core/resources"
	"github.com/JGoterris/orbit/internal/core/runner"
	"github.com/JGoterris/orbit/internal/core/watch"
	"github.com/JGoterris/orbit/internal/graph"
	"github.com/JGoterris/orbit/internal/platform"
	"github.com/JGoterris/orbit/internal/proc"
	"github.com/JGoterris/orbit/internal/state"
)

type Status string

const (
	Stopped   Status = "stopped"   // never started / stopped by the user
	Waiting   Status = "waiting"   // waiting for dependencies to become ready
	Starting  Status = "starting"  // launched, health check not passing yet
	Running   Status = "running"   // up, no health check configured
	Healthy   Status = "healthy"   // up and health check passing
	Unhealthy Status = "unhealthy" // up but health check failing
	Stopping  Status = "stopping"
	Exited    Status = "exited"  // exited on its own with code 0
	Crashed   Status = "crashed" // exited on its own with code != 0
	Failed    Status = "failed"  // could not be started (dependency failed, port busy, launch error, timeout)
)

// IsUp: the service is running (or about to be).
func IsUp(s Status) bool {
	return s == Starting || s == Running || s == Healthy || s == Unhealthy
}

func isTerminal(s Status) bool { return s == Stopped || s == Exited || s == Crashed || s == Failed }

// IsReadyStatus: ready to be depended on: up (and healthy if checked), or a oneshot task that finished OK.
func IsReadyStatus(s Status, oneshot bool) bool {
	return s == Running || s == Healthy || (s == Exited && oneshot)
}

// History is how many cpu / memory samples a ServiceState keeps.
const History = 60

// PendingConfig is a change in the config file nobody applied yet.
type PendingConfig struct {
	Diff *config.ConfigDiff `json:"diff,omitempty"`
	// Error: the file does not load, so there is nothing to apply until it is fixed
	Error string `json:"error,omitempty"`
}

// ServiceState is what the UI and the socket show about a service.
type ServiceState struct {
	Name        string `json:"name"`
	Status      Status `json:"status"`
	Pid         int    `json:"pid,omitempty"`
	ContainerID string `json:"containerId,omitempty"`
	StartedAt   int64  `json:"startedAt,omitempty"`
	StoppedAt   int64  `json:"stoppedAt,omitempty"`
	ExitCode    *int   `json:"exitCode,omitempty"`
	Restarts    int    `json:"restarts"`
	Error       string `json:"error,omitempty"`
	Health      string `json:"health,omitempty"`
	// Watch is "" when the service has no `watch`, else active, paused or pending
	Watch     string   `json:"watch,omitempty"`
	WaitingOn []string `json:"waitingOn,omitempty"`
	// Adopted: attached to a container that was already running: quitting orbit leaves it running
	Adopted bool      `json:"adopted,omitempty"`
	CPU     []float64 `json:"cpu"`
	Mem     []float64 `json:"mem"`
	// Resources is memory against its limit, and sustained growth; nil until there is something to say
	Resources *resources.Info `json:"resources,omitempty"`
}

// Options of New.
type Options struct {
	// StateDir overrides the per-project state directory (tests)
	StateDir string
}

// StopOptions of Stop.
type StopOptions struct {
	// NoDependents leaves the services that depend on this one running
	NoDependents bool
	// KeepAdopted only stops following an adopted container instead of stopping it
	KeepAdopted bool
}

type runtimeInfo struct {
	runner         runner.Runner
	gen            int
	waiters        []chan bool
	healthTimer    *time.Timer
	restartTimer   *time.Timer
	watcher        *watch.FileWatcher
	stopDone       chan struct{}
	userStopping   bool
	adopted        bool
	backoff        time.Duration
	healthFailures int
	// logReady: `ready_when.log` already matched in this run (always true when the service has none)
	logReady   bool
	readyMatch func(string) bool
	// env is the environment the service was last started with (env files included), also given to its hooks
	env map[string]string
	// hookPid is the pre_start / post_start hook process running right now
	hookPid int
	// postStop is closed when the post_stop of the previous run is over: a new start waits for it
	postStop chan struct{}
}

type bus[T any] struct {
	mu   sync.Mutex
	next int
	subs map[int]func(T)
}

func (b *bus[T]) on(fn func(T)) (cancel func()) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.subs == nil {
		b.subs = map[int]func(T){}
	}
	id := b.next
	b.next++
	b.subs[id] = fn
	return func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		delete(b.subs, id)
	}
}

func (b *bus[T]) emit(v T) {
	b.mu.Lock()
	fns := make([]func(T), 0, len(b.subs))
	for _, f := range b.subs {
		fns = append(fns, f)
	}
	b.mu.Unlock()
	for _, f := range fns {
		f(v)
	}
}

// Supervisor runs the services of one project in this process.
type Supervisor struct {
	// Logs holds everything the services print
	Logs *logs.Store

	mu         sync.Mutex
	cfg        *config.OrbitConfig
	deps       graph.DepMap
	dependents map[string][]string
	order      []string
	states     map[string]*ServiceState
	rt         map[string]*runtimeInfo
	pending    *PendingConfig
	disposed   bool
	stateDir   string

	reloadMu sync.Mutex

	sampler    *metrics.ProcessSampler
	resHistory *resources.History
	sampling   bool
	stopMetric chan struct{}

	configWatcher *configWatch

	changeBus  bus[string]
	configBus  bus[config.ConfigDiff]
	pendingBus bus[*PendingConfig]
}

// New makes a supervisor for cfg. Nothing runs until Init / Start.
func New(cfg *config.OrbitConfig, opts Options) *Supervisor {
	s := &Supervisor{
		Logs:       logs.NewStore(0, 0),
		cfg:        cfg,
		states:     map[string]*ServiceState{},
		rt:         map[string]*runtimeInfo{},
		sampler:    metrics.NewProcessSampler(),
		resHistory: resources.NewHistory(),
		stateDir:   opts.StateDir,
	}
	if s.stateDir == "" {
		s.stateDir = state.Dir(cfg)
	}
	s.refreshGraphLocked()
	for _, name := range cfg.Order {
		s.initRuntimeLocked(name)
	}
	return s
}

func (s *Supervisor) refreshGraphLocked() {
	s.deps = s.cfg.DepMap()
	s.dependents = graph.DependentsMap(s.deps)
	s.order = graph.TopoOrder(s.deps, nil)
}

func (s *Supervisor) initRuntimeLocked(name string) {
	s.states[name] = &ServiceState{Name: name, Status: Stopped}
	rt := &runtimeInfo{backoff: time.Second, logReady: true}
	if rw := s.cfg.Services[name].ReadyWhen; rw != nil {
		rt.logReady = false
		rt.readyMatch, _ = config.CompileLogPattern(rw.Log)
	}
	s.rt[name] = rt
}

// ---------------------------------------------------------------- queries

func (s *Supervisor) StateDir() string { return s.stateDir }

func (s *Supervisor) Config() *config.OrbitConfig {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cfg
}

func (s *Supervisor) ProjectName() string { return s.Config().Name }

// Names are the services in declaration order.
func (s *Supervisor) Names() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.cfg.Order)
}

// Order is the services with dependencies first.
func (s *Supervisor) Order() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.order)
}

func (s *Supervisor) Deps() graph.DepMap {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.deps
}

func (s *Supervisor) Dependents() map[string][]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.dependents
}

// Service is the configuration of a service; nil when there is none by that name.
func (s *Supervisor) Service(name string) *config.ServiceConfig {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cfg.Services[name]
}

// State is a copy of the state of a service (the zero value for an unknown one).
func (s *Supervisor) State(name string) ServiceState {
	s.mu.Lock()
	defer s.mu.Unlock()
	if st := s.states[name]; st != nil {
		return *st
	}
	return ServiceState{Name: name}
}

func (s *Supervisor) Snapshot() []ServiceState {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]ServiceState, 0, len(s.cfg.Order))
	for _, n := range s.cfg.Order {
		out = append(out, *s.states[n])
	}
	return out
}

// HistoryOf is up to an hour of cpu / memory in 10 s buckets, oldest first; sinceMs > 0 keeps only the last stretch.
func (s *Supervisor) HistoryOf(name string, sinceMs int64) ([]resources.Bucket, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cfg.Services[name] == nil {
		return nil, fmt.Errorf("unknown service %s", name)
	}
	return s.resHistory.Buckets(name, sinceMs, time.Now().UnixMilli()), nil
}

func (s *Supervisor) isReadyLocked(name string, status Status) bool {
	svc := s.cfg.Services[name]
	return IsReadyStatus(status, svc != nil && svc.Oneshot)
}

// IsReady: ready to be depended on.
func (s *Supervisor) IsReady(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.states[name]
	return st != nil && s.isReadyLocked(name, st.Status)
}

func (s *Supervisor) IsUp(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.isUpLocked(name)
}

func (s *Supervisor) isUpLocked(name string) bool {
	st := s.states[name]
	return st != nil && (IsUp(st.Status) || st.Status == Waiting)
}

func (s *Supervisor) RunningCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, name := range s.cfg.Order {
		if s.isUpLocked(name) || s.states[name].Status == Stopping {
			n++
		}
	}
	return n
}

// OwnedRunningCount is the running services this session started itself (the ones quitting will stop).
func (s *Supervisor) OwnedRunningCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, name := range s.cfg.Order {
		if (s.isUpLocked(name) || s.states[name].Status == Stopping) && !s.rt[name].adopted {
			n++
		}
	}
	return n
}

func (s *Supervisor) IsAdopted(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	rt := s.rt[name]
	return rt != nil && rt.adopted && s.isUpLocked(name)
}

func (s *Supervisor) Pending() *PendingConfig {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pending
}

// OnChange calls fn with the name of a service whose state changed ("" for a change of the whole project).
func (s *Supervisor) OnChange(fn func(name string)) (cancel func()) { return s.changeBus.on(fn) }

// OnConfig calls fn after a reload was applied.
func (s *Supervisor) OnConfig(fn func(config.ConfigDiff)) (cancel func()) { return s.configBus.on(fn) }

// OnConfigPending calls fn when orbit.yaml changed on disk (a diff or an error), or nil when it is back in sync.
func (s *Supervisor) OnConfigPending(fn func(*PendingConfig)) (cancel func()) {
	return s.pendingBus.on(fn)
}

// ServiceView adapts the state of a service for the desktop notifier.
func (s *Supervisor) ServiceView(name string) notify.ServiceView {
	st := s.State(name)
	return notify.ServiceView{Status: string(st.Status), Error: st.Error, Health: st.Health, Resources: st.Resources}
}

// ---------------------------------------------------------------- state plumbing

// apply mutates the state of a service. Call it with s.mu held; it returns what has to run once the lock is released.
func (s *Supervisor) apply(name string, fn func(*ServiceState)) func() {
	st := s.states[name]
	if st == nil {
		return func() {}
	}
	was := s.isReadyLocked(name, st.Status)
	fn(st)
	var waiters []chan bool
	if !was && s.isReadyLocked(name, st.Status) {
		if rt := s.rt[name]; rt != nil {
			waiters, rt.waiters = rt.waiters, nil
		}
	}
	return func() {
		for _, w := range waiters {
			w <- true
		}
		s.changeBus.emit(name)
	}
}

func (s *Supervisor) update(name string, fn func(*ServiceState)) {
	s.mu.Lock()
	post := s.apply(name, fn)
	s.mu.Unlock()
	post()
}

// updateIfGen is update, but only while the run identified by gen is still the current one: an exit that raced with
// the caller bumps the generation, and must not be overwritten.
func (s *Supervisor) updateIfGen(name string, gen int, fn func(*ServiceState)) bool {
	s.mu.Lock()
	rt := s.rt[name]
	if rt == nil || rt.gen != gen {
		s.mu.Unlock()
		return false
	}
	post := s.apply(name, fn)
	s.mu.Unlock()
	post()
	return true
}

func (s *Supervisor) flushWaiters(name string, ready bool) {
	s.mu.Lock()
	var waiters []chan bool
	if rt := s.rt[name]; rt != nil {
		waiters, rt.waiters = rt.waiters, nil
	}
	s.mu.Unlock()
	for _, w := range waiters {
		w <- ready
	}
}

// waitReadyChan answers true once the service is ready, false as soon as it cannot become so.
func (s *Supervisor) waitReadyChan(name string) <-chan bool {
	ch := make(chan bool, 1)
	s.mu.Lock()
	defer s.mu.Unlock()
	st, rt := s.states[name], s.rt[name]
	switch {
	case st == nil || rt == nil:
		ch <- false
	case s.isReadyLocked(name, st.Status):
		ch <- true
	case isTerminal(st.Status):
		ch <- false
	default:
		rt.waiters = append(rt.waiters, ch)
	}
	return ch
}

func (s *Supervisor) isGen(name string, gen int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	rt := s.rt[name]
	return rt != nil && rt.gen == gen
}

func (s *Supervisor) fail(name, msg string, logIt bool) {
	if logIt {
		s.Log(name, "✖ "+msg)
	}
	s.update(name, func(st *ServiceState) { st.Status, st.Error, st.WaitingOn = Failed, msg, nil })
	s.flushWaiters(name, false)
}

// Log writes a line of orbit's own (system stream) into the service's log.
func (s *Supervisor) Log(name, text string) { s.Logs.Append(name, logs.System, text) }

// ClearLogs empties the logs of a service (or of all of them when name is "").
func (s *Supervisor) ClearLogs(name string) {
	s.Logs.Clear(name)
	s.changeBus.emit(name)
}

// ---------------------------------------------------------------- lifecycle

// Init re-attaches to what is already running: containers, and processes a previous orbit session left behind
// (recorded in state.json). It starts the metrics loop and the watchers.
func (s *Supervisor) Init() {
	s.mu.Lock()
	s.stopMetric = make(chan struct{})
	stop := s.stopMetric
	s.mu.Unlock()
	go func() {
		t := time.NewTicker(2 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				s.sampleMetrics()
			}
		}
	}()

	saved := state.Read(s.stateDir).Services
	var wg sync.WaitGroup
	for _, name := range s.Names() {
		wg.Add(1)
		go func() {
			defer wg.Done()
			svc := s.Service(name)
			if svc == nil {
				return
			}
			entry, hasEntry := saved[name]
			if svc.Type == config.TypeProcess && !hasEntry {
				return
			}
			r := s.makeRunner(name, nil)
			var savedPtr *state.SavedService
			if hasEntry {
				savedPtr = &entry
			}
			attached, err := r.Attach(savedPtr)
			if err != nil || !attached {
				return
			}
			s.mu.Lock()
			rt := s.rt[name]
			if rt == nil || rt.runner != nil || s.disposed {
				s.mu.Unlock()
				return
			}
			rt.runner = r
			// a container orbit did not start itself stays up when orbit quits; anything recorded in state.json is ours
			rt.adopted = svc.Type != config.TypeProcess && !hasEntry
			rt.logReady = true // it printed its line before we got here
			rt.gen++
			gen := rt.gen
			adopted := rt.adopted
			s.mu.Unlock()

			var msg string
			switch {
			case svc.Type == config.TypeExternal:
				msg = fmt.Sprintf("monitoring external service (%s)", svc.Health.Describe())
			case svc.Type == config.TypeProcess:
				msg = fmt.Sprintf("re-attached to running process (pid %d)", r.Pid())
			case adopted:
				msg = "attached to already running container (left running when orbit quits)"
			default:
				msg = "re-attached to running container"
			}
			s.Log(name, msg)
			status := Running
			if svc.Health != nil || (svc.Oneshot && svc.Type == config.TypeProcess) {
				status = Starting
			}
			startedAt := entry.StartedAt
			if startedAt == 0 {
				startedAt = time.Now().UnixMilli()
			}
			s.update(name, func(st *ServiceState) {
				st.Status, st.Pid, st.ContainerID, st.StartedAt, st.Adopted = status, r.Pid(), r.ContainerID(), startedAt, adopted
			})
			if !svc.Oneshot {
				s.scheduleHealth(name, gen, 0)
			}
		}()
	}
	wg.Wait()
	s.persist()
	s.startWatchers()
	s.watchConfigFiles()
}

// Start starts a service (and its dependencies first). It returns true once the service is ready.
func (s *Supervisor) Start(name string) bool {
	svc := s.Service(name)
	if svc == nil {
		return false
	}
	if svc.Type == config.TypeExternal {
		return s.checkExternal(name)
	}

	s.mu.Lock()
	st, rt := s.states[name], s.rt[name]
	if s.isReadyLocked(name, st.Status) {
		s.mu.Unlock()
		return true
	}
	switch st.Status {
	case Waiting, Starting, Unhealthy:
		s.mu.Unlock()
		return <-s.waitReadyChan(name)
	}
	var stopDone chan struct{}
	if st.Status == Stopping {
		stopDone = rt.stopDone
	}
	s.mu.Unlock()
	if stopDone != nil {
		<-stopDone
	}

	s.mu.Lock()
	if rt.restartTimer != nil {
		rt.restartTimer.Stop()
		rt.restartTimer = nil
	}
	rt.gen++
	gen := rt.gen
	postStop := rt.postStop
	s.mu.Unlock()
	// the previous run's post_stop must be over before this one begins
	if postStop != nil {
		<-postStop
	}
	if !s.isGen(name, gen) {
		return false
	}

	if len(svc.DependsOn) > 0 {
		deps := svc.DependsOn
		s.update(name, func(st *ServiceState) { st.Status, st.WaitingOn, st.Error = Waiting, deps, "" })
		results := make([]bool, len(deps))
		var wg sync.WaitGroup
		for i, d := range deps {
			wg.Add(1)
			go func() { defer wg.Done(); results[i] = s.Start(d) }()
		}
		wg.Wait()
		if !s.isGen(name, gen) {
			return false
		}
		var failed []string
		for i, d := range deps {
			if !results[i] {
				failed = append(failed, d)
			}
		}
		if len(failed) > 0 {
			s.fail(name, "dependency not ready: "+strings.Join(failed, ", "), true)
			return false
		}
	}

	s.update(name, func(st *ServiceState) { st.Status, st.WaitingOn, st.Error, st.ExitCode = Starting, nil, "", nil })
	s.mu.Lock()
	rt.healthFailures = 0
	s.mu.Unlock()

	if svc.Port != 0 && svc.Type != config.TypeCompose && proc.IsPortOpen(svc.Port, "", 0) {
		if !s.isGen(name, gen) {
			return false
		}
		msg := fmt.Sprintf("port %d is already in use", svc.Port)
		if who := platform.WhoListens(svc.Port); who != "" {
			msg += " by " + who
		}
		s.fail(name, msg, true)
		return false
	}

	// env files are re-read on every start, so editing a .env only needs a restart
	fromFiles, err := config.ReadEnvFiles(svc.EnvFiles)
	if err != nil {
		s.fail(name, err.Error(), true)
		return false
	}
	env := map[string]string{}
	for k, v := range fromFiles {
		env[k] = v
	}
	for k, v := range svc.Env {
		env[k] = v
	}
	if len(svc.EnvFiles) > 0 {
		root := s.Config().Root
		files := make([]string, len(svc.EnvFiles))
		for i, f := range svc.EnvFiles {
			files[i] = f.Path
			if r, err := filepath.Rel(root, f.Path); err == nil && r != "" {
				files[i] = r
			}
		}
		s.Log(name, fmt.Sprintf("env: %d vars from %s", len(fromFiles), strings.Join(files, ", ")))
	}
	s.mu.Lock()
	rt.env = env
	s.mu.Unlock()

	if svc.Hooks != nil && len(svc.Hooks.PreStart) > 0 {
		res := s.runHook(name, hooks.PreStart, svc.Hooks.PreStart, nil)
		if !s.isGen(name, gen) {
			return false
		}
		if !res.OK {
			s.fail(name, res.Error, false) // the hook runner already logged it
			return false
		}
	}

	r := s.makeRunner(name, env)
	s.mu.Lock()
	rt.runner = r
	rt.logReady = svc.ReadyWhen == nil
	rt.adopted = false
	rt.userStopping = false
	s.mu.Unlock()
	if svc.Type == config.TypeProcess {
		s.Log(name, "$ "+svc.Cmd)
	} else {
		s.Log(name, fmt.Sprintf("starting %s service", svc.Type))
	}
	if err := r.Start(); err != nil {
		s.mu.Lock()
		if rt.runner == r {
			rt.runner = nil
		}
		current := rt.gen == gen
		s.mu.Unlock()
		if current {
			s.fail(name, err.Error(), true)
		}
		return false
	}
	if !s.isGen(name, gen) {
		return false
	}

	s.mu.Lock()
	logReady := rt.logReady
	s.mu.Unlock()
	status := Running
	// a oneshot task is "starting" until it exits; its exit code is its health check
	if svc.Health != nil || svc.Oneshot || !logReady {
		status = Starting
	}
	pid, cid := r.Pid(), r.ContainerID()
	if !s.updateIfGen(name, gen, func(st *ServiceState) {
		st.Status, st.Pid, st.ContainerID = status, pid, cid
		st.StartedAt, st.StoppedAt, st.Adopted = time.Now().UnixMilli(), 0, false
	}) {
		return false
	}
	s.persist()
	if !svc.Oneshot {
		s.scheduleHealth(name, gen, 300*time.Millisecond)
	}

	timeout := time.Duration(svc.StartTimeout) * time.Millisecond
	select {
	case ready := <-s.waitReadyChan(name):
		if ready && s.isGen(name, gen) && svc.Hooks != nil && len(svc.Hooks.PostStart) > 0 {
			go s.runPostStart(name, gen)
		}
		return ready
	case <-time.After(timeout):
		if !s.isGen(name, gen) || s.IsReady(name) {
			return s.IsReady(name)
		}
		msg := fmt.Sprintf("not ready after %ds", (svc.StartTimeout+500)/1000)
		s.update(name, func(st *ServiceState) { st.Status, st.Error = Unhealthy, msg })
		s.Log(name, fmt.Sprintf("not ready after %ds (%s)", (svc.StartTimeout+500)/1000, health.DescribeReadiness(svc)))
		s.flushWaiters(name, false)
		return false
	}
}

// checkExternal is Start for an external service: check it now. It returns true once it answers (false after
// `start_timeout`).
func (s *Supervisor) checkExternal(name string) bool {
	svc := s.Service(name)
	s.mu.Lock()
	rt := s.rt[name]
	needInit := rt.runner == nil
	if needInit {
		// not being monitored yet (Init did not run)
		rt.runner = s.newRunnerLocked(name, nil)
		rt.adopted = true
		rt.gen++
	}
	gen := rt.gen
	s.mu.Unlock()
	if needInit {
		s.update(name, func(st *ServiceState) {
			st.Status, st.StartedAt, st.Adopted, st.Error = Starting, time.Now().UnixMilli(), true, ""
		})
	}
	s.scheduleHealth(name, gen, 0)
	timeout := time.Duration(svc.StartTimeout) * time.Millisecond
	select {
	case ready := <-s.waitReadyChan(name):
		return ready
	case <-time.After(timeout):
		if s.IsReady(name) {
			return true
		}
		detail := s.State(name).Health
		if detail != "" {
			detail = ": " + detail
		}
		s.Log(name, fmt.Sprintf("not reachable after %ds (%s%s)", (svc.StartTimeout+500)/1000, svc.Health.Describe(), detail))
		s.flushWaiters(name, false)
		return false
	}
}

// Stop stops a service, stopping everything that depends on it first.
func (s *Supervisor) Stop(name string, opts StopOptions) {
	svc := s.Service(name)
	// orbit does not run an external service, so there is nothing to stop (and its dependents keep running)
	if svc == nil || svc.Type == config.TypeExternal {
		return
	}
	if !opts.NoDependents {
		var wg sync.WaitGroup
		for _, d := range s.Dependents()[name] {
			if s.IsUp(d) {
				wg.Add(1)
				go func() { defer wg.Done(); s.Stop(d, StopOptions{KeepAdopted: opts.KeepAdopted}) }()
			}
		}
		wg.Wait()
	}

	s.mu.Lock()
	rt, st := s.rt[name], s.states[name]
	if rt == nil {
		s.mu.Unlock()
		return
	}
	if opts.KeepAdopted && rt.adopted && rt.runner != nil {
		// only stop following it
		rt.gen++
		if rt.healthTimer != nil {
			rt.healthTimer.Stop()
		}
		r := rt.runner
		rt.runner = nil
		s.mu.Unlock()
		r.KillSync()
		return
	}
	if rt.stopDone != nil {
		done := rt.stopDone
		s.mu.Unlock()
		<-done
		return
	}
	if rt.restartTimer != nil {
		rt.restartTimer.Stop()
		rt.restartTimer = nil
	}
	rt.gen++
	hookPid := rt.hookPid
	r := rt.runner
	if r == nil || isTerminal(st.Status) {
		rt.runner = nil
		wasStopped := st.Status == Stopped
		var post func()
		if !wasStopped {
			post = s.apply(name, func(st *ServiceState) { st.Status, st.WaitingOn = Stopped, nil })
		}
		s.mu.Unlock()
		if hookPid != 0 {
			platform.KillTree(hookPid, syscall.SIGKILL)
		}
		if post != nil {
			post()
		}
		s.flushWaiters(name, false)
		return
	}
	rt.userStopping = true
	done := make(chan struct{})
	rt.stopDone = done
	post := s.apply(name, func(st *ServiceState) { st.Status = Stopping })
	s.mu.Unlock()
	// a pre_start / post_start still running is cut short
	if hookPid != 0 {
		platform.KillTree(hookPid, syscall.SIGKILL)
	}
	post()
	s.Log(name, "stopping…")

	defer func() {
		s.mu.Lock()
		rt.stopDone = nil
		s.mu.Unlock()
		close(done)
	}()
	if err := r.Stop(svc.StopTimeout); err != nil {
		s.Log(name, "stop failed: "+err.Error())
	}
	s.mu.Lock()
	if rt.healthTimer != nil {
		rt.healthTimer.Stop()
	}
	if rt.runner == r {
		rt.runner = nil
	}
	cur := s.cfg.Services[name]
	s.mu.Unlock()
	if cur != nil && cur.Hooks != nil && len(cur.Hooks.PostStop) > 0 {
		s.runHook(name, hooks.PostStop, cur.Hooks.PostStop, nil)
	}
	s.update(name, func(st *ServiceState) {
		st.Status, st.Pid, st.StoppedAt, st.Resources = Stopped, 0, time.Now().UnixMilli(), nil
	})
	s.persist()
	s.Log(name, "stopped")
	s.flushWaiters(name, false)
}

func (s *Supervisor) Restart(name string) bool {
	s.Stop(name, StopOptions{NoDependents: true})
	return s.Start(name)
}

// Toggle stops a service if it is up and starts it otherwise.
func (s *Supervisor) Toggle(name string) {
	if s.IsUp(name) {
		s.Stop(name, StopOptions{})
	} else {
		s.Start(name)
	}
}

// StartMany starts the given services (dependencies first, in parallel where they allow it).
func (s *Supervisor) StartMany(names []string) {
	var wg sync.WaitGroup
	for _, n := range graph.TopoOrder(s.Deps(), names) {
		if !slices.Contains(names, n) {
			continue
		}
		wg.Add(1)
		go func() { defer wg.Done(); s.Start(n) }()
	}
	wg.Wait()
}

// StartAll starts every service that has `autostart`.
func (s *Supervisor) StartAll() {
	var names []string
	for _, n := range s.Names() {
		if svc := s.Service(n); svc != nil && svc.Autostart {
			names = append(names, n)
		}
	}
	s.StartMany(names)
}

func (s *Supervisor) StopAll() {
	var wg sync.WaitGroup
	for _, n := range s.Names() {
		wg.Add(1)
		go func() { defer wg.Done(); s.Stop(n, StopOptions{}) }()
	}
	wg.Wait()
}

func (s *Supervisor) closeMetrics() {
	if s.stopMetric != nil {
		close(s.stopMetric)
		s.stopMetric = nil
	}
}

// Dispose stops everything this session owns (adopted containers are only let go) and releases timers.
func (s *Supervisor) Dispose() {
	s.mu.Lock()
	s.disposed = true
	s.closeMetrics()
	s.mu.Unlock()
	s.closeWatchers()
	s.closeConfigWatchers()
	var wg sync.WaitGroup
	for _, n := range s.Names() {
		wg.Add(1)
		go func() { defer wg.Done(); s.Stop(n, StopOptions{KeepAdopted: true}) }()
	}
	wg.Wait()
	s.mu.Lock()
	for _, rt := range s.rt {
		if rt.healthTimer != nil {
			rt.healthTimer.Stop()
		}
		if rt.restartTimer != nil {
			rt.restartTimer.Stop()
		}
	}
	s.mu.Unlock()
	s.persist()
}

// Detach quits without stopping anything: it records what is running (state.json) and stops following it.
// Processes keep running in their own group; the next Init picks them up again.
func (s *Supervisor) Detach() {
	s.mu.Lock()
	s.disposed = true
	s.closeMetrics()
	s.mu.Unlock()
	s.closeWatchers()
	s.closeConfigWatchers()
	s.persist()
	s.mu.Lock()
	var runners []runner.Runner
	var waiters []chan bool
	for _, rt := range s.rt {
		if rt.healthTimer != nil {
			rt.healthTimer.Stop()
		}
		if rt.restartTimer != nil {
			rt.restartTimer.Stop()
		}
		rt.gen++
		if rt.runner != nil {
			runners = append(runners, rt.runner)
			rt.runner = nil
		}
		waiters = append(waiters, rt.waiters...)
		rt.waiters = nil
	}
	s.mu.Unlock()
	for _, r := range runners {
		r.Release()
	}
	for _, w := range waiters {
		w <- false
	}
}

// KillAllSync is the last resort on process exit: SIGKILL every process group we own. It does not wait.
func (s *Supervisor) KillAllSync() {
	s.mu.Lock()
	var runners []runner.Runner
	var hooksPids []int
	for _, rt := range s.rt {
		if rt.runner != nil {
			runners = append(runners, rt.runner)
		}
		if rt.hookPid != 0 {
			hooksPids = append(hooksPids, rt.hookPid)
		}
	}
	s.mu.Unlock()
	for _, r := range runners {
		r.KillSync()
	}
	for _, p := range hooksPids {
		platform.KillTree(p, syscall.SIGKILL)
	}
}

// ToggleWatch pauses / resumes file watching of a service. It returns "" if the service has no `watch`.
func (s *Supervisor) ToggleWatch(name string) string {
	s.mu.Lock()
	rt := s.rt[name]
	var w *watch.FileWatcher
	if rt != nil {
		w = rt.watcher
	}
	s.mu.Unlock()
	if w == nil {
		return ""
	}
	paused := !w.Paused()
	w.SetPaused(paused)
	status, msg := "active", "watch resumed"
	if paused {
		status, msg = "paused", "watch paused"
	}
	s.Log(name, msg)
	s.update(name, func(st *ServiceState) { st.Watch = status })
	return status
}

// ---------------------------------------------------------------- internals

func (s *Supervisor) startWatchers() {
	for _, name := range s.Names() {
		s.mu.Lock()
		svc, rt := s.cfg.Services[name], s.rt[name]
		if svc == nil || svc.Watch == nil || rt == nil || rt.watcher != nil || s.disposed {
			s.mu.Unlock()
			continue
		}
		w := watch.New(svc.Cwd, *svc.Watch, watch.Hooks{
			OnTrigger: func(files []string) { s.onWatchTrigger(name, files) },
			OnError:   func(msg string) { s.Log(name, msg) },
			OnPending: func(pending bool) {
				s.mu.Lock()
				cur := s.rt[name]
				paused := cur != nil && cur.watcher != nil && cur.watcher.Paused()
				s.mu.Unlock()
				if paused {
					return
				}
				status := "active"
				if pending {
					status = "pending"
				}
				s.update(name, func(st *ServiceState) { st.Watch = status })
			},
		})
		rt.watcher = w
		s.mu.Unlock()
		w.Start()
		s.update(name, func(st *ServiceState) { st.Watch = "active" })
	}
}

func (s *Supervisor) closeWatchers() {
	s.mu.Lock()
	var ws []*watch.FileWatcher
	for _, rt := range s.rt {
		if rt.watcher != nil {
			ws = append(ws, rt.watcher)
			rt.watcher = nil
		}
	}
	s.mu.Unlock()
	for _, w := range ws {
		w.Close()
	}
}

func (s *Supervisor) onWatchTrigger(name string, files []string) {
	st := s.State(name)
	s.mu.Lock()
	disposed := s.disposed
	s.mu.Unlock()
	// a change never starts a service the user stopped, nor interrupts one still waiting for its dependencies
	if st.Status == Stopped || st.Status == Stopping || st.Status == Waiting || disposed {
		return
	}
	shown := strings.Join(files[:min(2, len(files))], ", ")
	if len(files) > 2 {
		shown += fmt.Sprintf(" (+%d more)", len(files)-2)
	}
	s.Log(name, fmt.Sprintf("↻ %s changed — restarting", shown))
	s.mu.Lock()
	if rt := s.rt[name]; rt != nil {
		rt.backoff = time.Second
	}
	s.mu.Unlock()
	go s.Restart(name)
}

// persist writes what this session owns and has running, for a later session to re-attach to.
func (s *Supervisor) persist() {
	type entry struct {
		name   string
		r      runner.Runner
		typ    config.ServiceType
		startd int64
	}
	s.mu.Lock()
	var entries []entry
	for _, name := range s.cfg.Order {
		rt := s.rt[name]
		if rt == nil || rt.runner == nil || rt.adopted {
			continue
		}
		entries = append(entries, entry{name, rt.runner, s.cfg.Services[name].Type, s.states[name].StartedAt})
	}
	dir := s.stateDir
	s.mu.Unlock()
	services := map[string]state.SavedService{}
	for _, e := range entries {
		pid := e.r.Pid()
		if e.typ == config.TypeProcess && pid == 0 {
			continue
		}
		started := e.startd
		if started == 0 {
			started = time.Now().UnixMilli()
		}
		services[e.name] = state.SavedService{Pid: pid, StartTime: e.r.StartTime(), StartedAt: started}
	}
	state.Write(dir, state.Saved{Services: services})
}

func (s *Supervisor) runHook(name string, phase hooks.Phase, list []config.Hook, exitCode *int) hooks.Result {
	s.mu.Lock()
	svc := s.cfg.Services[name]
	rt := s.rt[name]
	env := rt.env
	s.mu.Unlock()
	return hooks.Run(phase, list, hooks.Context{
		Service: name, Cwd: svc.Cwd, Shell: svc.Shell, Env: env, ExitCode: exitCode,
		Log: func(stream logs.Stream, text string) { s.Logs.Append(name, stream, text) },
		Track: func(pid int) {
			s.mu.Lock()
			rt.hookPid = pid
			s.mu.Unlock()
		},
	})
}

// runPostStart never blocks readiness: a failure is reported but the service stays up.
func (s *Supervisor) runPostStart(name string, gen int) {
	svc := s.Service(name)
	res := s.runHook(name, hooks.PostStart, svc.Hooks.PostStart, nil)
	if !res.OK && s.isGen(name, gen) {
		s.update(name, func(st *ServiceState) { st.Error = res.Error })
	}
}

func (s *Supervisor) makeRunner(name string, env map[string]string) runner.Runner {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.newRunnerLocked(name, env)
}

func (s *Supervisor) newRunnerLocked(name string, env map[string]string) runner.Runner {
	svc := s.cfg.Services[name]
	if env != nil {
		c := *svc
		c.Env = env
		svc = &c
	}
	var r runner.Runner
	r = runner.New(svc, s.cfg.Name, runner.Callbacks{
		Log: func(stream logs.Stream, text string) {
			s.Logs.Append(name, stream, text)
			if stream == logs.System {
				return
			}
			s.mu.Lock()
			rt := s.rt[name]
			match := rt != nil && !rt.logReady && rt.runner == r && rt.readyMatch != nil
			var fn func(string) bool
			if match {
				fn = rt.readyMatch
			}
			s.mu.Unlock()
			if fn != nil && fn(logs.CleanLine(text)) {
				s.onLogReady(name)
			}
		},
		Exit: func(code *int, signal string) { s.onExit(name, r, code, signal) },
	}, state.Files(s.stateDir, name))
	return r
}

func (s *Supervisor) onExit(name string, r runner.Runner, code *int, signal string) {
	s.mu.Lock()
	rt := s.rt[name]
	if rt == nil || rt.runner != r || rt.userStopping {
		s.mu.Unlock()
		return
	}
	rt.runner = nil
	rt.gen++
	if rt.healthTimer != nil {
		rt.healthTimer.Stop()
	}
	s.mu.Unlock()
	s.persist()

	st := s.State(name)
	svc := s.Service(name)
	if svc == nil {
		return
	}
	ok := code != nil && *code == 0
	var uptime time.Duration
	if st.StartedAt != 0 {
		uptime = time.Since(time.UnixMilli(st.StartedAt))
	}
	how := "code null"
	switch {
	case signal != "":
		how = "signal " + signal
	case code != nil:
		how = fmt.Sprintf("code %d", *code)
	}
	switch {
	case svc.Oneshot && ok:
		s.Log(name, fmt.Sprintf("✓ done in %.1fs", uptime.Seconds()))
	case uptime > 0:
		s.Log(name, fmt.Sprintf("exited with %s after %ds", how, int(uptime.Seconds()+0.5)))
	default:
		s.Log(name, "exited with "+how)
	}
	status := Crashed
	errMsg := "exited with " + strings.TrimPrefix(how, "signal ")
	if ok {
		status, errMsg = Exited, ""
	}
	s.update(name, func(st *ServiceState) {
		st.Status, st.ExitCode, st.Pid, st.StoppedAt, st.Resources, st.Error = status, code, 0, time.Now().UnixMilli(), nil, errMsg
	})
	s.flushWaiters(name, false)

	// post_stop also runs when the service went down on its own; a restart waits for it (see Start)
	if svc.Hooks != nil && len(svc.Hooks.PostStop) > 0 {
		done := make(chan struct{})
		s.mu.Lock()
		rt.postStop = done
		s.mu.Unlock()
		go func() {
			s.runHook(name, hooks.PostStop, svc.Hooks.PostStop, code)
			s.mu.Lock()
			if rt.postStop == done {
				rt.postStop = nil
			}
			s.mu.Unlock()
			close(done)
		}()
	}

	// a oneshot that succeeded is finished, whatever the restart policy says
	restart := (svc.Restart == config.RestartAlways && !(svc.Oneshot && ok)) || (svc.Restart == config.RestartOnFailure && !ok)
	if restart {
		s.mu.Lock()
		if uptime > 30*time.Second {
			rt.backoff = time.Second
		}
		delay := rt.backoff
		rt.backoff = min(rt.backoff*2, 30*time.Second)
		s.mu.Unlock()
		s.Log(name, fmt.Sprintf("restarting in %gs (restart: %s)", delay.Seconds(), svc.Restart))
		timer := time.AfterFunc(delay, func() {
			s.update(name, func(st *ServiceState) { st.Restarts++ })
			s.Start(name)
		})
		s.mu.Lock()
		rt.restartTimer = timer
		s.mu.Unlock()
	}
}

// onLogReady: the service printed its `ready_when.log` line: ready now, or as soon as its health check passes.
func (s *Supervisor) onLogReady(name string) {
	s.mu.Lock()
	rt := s.rt[name]
	svc := s.cfg.Services[name]
	if rt == nil || svc == nil || rt.logReady {
		s.mu.Unlock()
		return
	}
	rt.logReady = true
	gen := rt.gen
	status := s.states[name].Status
	s.mu.Unlock()
	s.Log(name, fmt.Sprintf("ready (log matched %q)", svc.ReadyWhen.Log))
	if svc.Health != nil {
		s.scheduleHealth(name, gen, 0)
	} else if status == Starting || status == Unhealthy {
		s.update(name, func(st *ServiceState) { st.Status, st.Error = Running, "" })
	}
}

func (s *Supervisor) scheduleHealth(name string, gen int, delay time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	svc, rt := s.cfg.Services[name], s.rt[name]
	if svc == nil || svc.Health == nil || rt == nil {
		return
	}
	if rt.healthTimer != nil {
		rt.healthTimer.Stop()
	}
	rt.healthTimer = time.AfterFunc(delay, func() { s.healthTick(name, gen) })
}

func (s *Supervisor) healthTick(name string, gen int) {
	s.mu.Lock()
	svc, rt := s.cfg.Services[name], s.rt[name]
	if svc == nil || rt == nil || rt.gen != gen || rt.runner == nil || svc.Health == nil {
		s.mu.Unlock()
		return
	}
	r := rt.runner
	s.mu.Unlock()

	res := health.Check(svc.Health, svc, r.ContainerID())

	s.mu.Lock()
	rt = s.rt[name]
	if rt == nil || rt.gen != gen || rt.runner == nil {
		s.mu.Unlock()
		return
	}
	st := *s.states[name]
	logReady := rt.logReady
	var failures int
	if res.OK {
		if logReady {
			rt.healthFailures = 0
		}
	} else {
		rt.healthFailures++
		failures = rt.healthFailures
	}
	s.mu.Unlock()

	switch {
	case res.OK && !logReady:
		// up, but still waiting for its log line
		if st.Health != res.Detail {
			s.update(name, func(st *ServiceState) { st.Health = res.Detail })
		}
	case res.OK:
		if st.Status != Healthy {
			s.Log(name, fmt.Sprintf("healthy (%s: %s)", svc.Health.Describe(), res.Detail))
			s.update(name, func(st *ServiceState) { st.Status, st.Health, st.Error = Healthy, res.Detail, "" })
		} else if st.Health != res.Detail {
			s.update(name, func(st *ServiceState) { st.Health = res.Detail })
		}
	case svc.Type == config.TypeExternal && st.Status == Starting:
		// nothing else will ever move an external service out of "starting"
		s.Log(name, "unreachable: "+res.Detail)
		s.update(name, func(st *ServiceState) { st.Status, st.Health = Unhealthy, res.Detail })
	case st.Status == Healthy && failures >= 3:
		// a service that was healthy needs a few consecutive failures to be marked unhealthy
		s.Log(name, "health check failing: "+res.Detail)
		s.update(name, func(st *ServiceState) { st.Status, st.Health = Unhealthy, res.Detail })
	case st.Health != res.Detail:
		s.update(name, func(st *ServiceState) { st.Health = res.Detail })
	}

	// poll faster while starting
	next := time.Duration(svc.Health.Interval) * time.Millisecond
	if s.State(name).Status == Starting {
		next = min(next, time.Second)
	}
	s.scheduleHealth(name, gen, next)
}

// assess is the `resources` of a service after a sample; the previous object when nothing changed (no needless
// re-render / broadcast).
func (s *Supervisor) assess(name string, mem float64, containerLimit int64) *resources.Info {
	s.mu.Lock()
	st := s.states[name]
	svc := s.cfg.Services[name]
	if st == nil || svc == nil {
		s.mu.Unlock()
		return nil
	}
	prev := st.Resources
	started := st.StartedAt
	var buckets []resources.Bucket
	if svc.LeakDetection {
		all := s.resHistory.Buckets(name, 0, 0)
		since := started - 10_000
		for _, b := range all {
			if b.T >= since {
				buckets = append(buckets, b)
			}
		}
	}
	s.mu.Unlock()

	memLimit := svc.MemLimit
	if memLimit == 0 {
		memLimit = containerLimit
	}
	var prevLevel resources.MemLevel
	if prev != nil {
		prevLevel = prev.Level
	}
	level := resources.Level(mem, memLimit, prevLevel)
	var leak *resources.Leak
	if svc.LeakDetection {
		leak = resources.DetectLeak(buckets, memLimit)
	}
	// the estimate moves every sample; only a leak that appears, disappears or changes a lot is news
	keep := leak
	if prev != nil && prev.Leak != nil && leak != nil && absf(leak.PerMin-prev.Leak.PerMin) < prev.Leak.PerMin*0.25 {
		keep = prev.Leak
	}
	if memLimit == 0 && level == resources.LevelNone && keep == nil {
		return nil
	}
	if prev != nil && prev.MemLimit == memLimit && prev.Level == level && prev.Leak == keep {
		return prev
	}
	return &resources.Info{MemLimit: memLimit, Level: level, Leak: keep}
}

func absf(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}

func (s *Supervisor) sampleMetrics() {
	s.mu.Lock()
	if s.sampling {
		s.mu.Unlock()
		return
	}
	s.sampling = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.sampling = false
		s.mu.Unlock()
	}()

	type target struct {
		name string
		pid  int
		cid  string
	}
	var procs, containers []target
	s.mu.Lock()
	for _, name := range s.cfg.Order {
		st := s.states[name]
		if st == nil || !IsUp(st.Status) {
			continue
		}
		switch {
		case st.Pid != 0:
			procs = append(procs, target{name: name, pid: st.Pid})
		case st.ContainerID != "":
			containers = append(containers, target{name: name, cid: st.ContainerID})
		}
	}
	s.mu.Unlock()

	push := func(name string, cpu, mem float64, containerLimit int64) {
		s.mu.Lock()
		s.resHistory.Push(name, cpu, mem, time.Now().UnixMilli())
		s.mu.Unlock()
		info := s.assess(name, mem, containerLimit)
		s.update(name, func(st *ServiceState) {
			st.CPU = appendCapped(st.CPU, cpu)
			st.Mem = appendCapped(st.Mem, mem)
			st.Resources = info
		})
	}
	roots := make([]int, len(procs))
	for i, t := range procs {
		roots[i] = t.pid
	}
	samples := s.sampler.Sample(roots)
	for _, t := range procs {
		if smp, ok := samples[t.pid]; ok {
			push(t.name, smp.Cpu, smp.Mem, 0)
		}
	}
	if len(containers) > 0 {
		ids := make([]string, len(containers))
		for i, t := range containers {
			ids[i] = t.cid
		}
		cs := metrics.SampleContainers(ids)
		for _, t := range containers {
			if smp, ok := cs[t.cid]; ok && IsUp(s.State(t.name).Status) {
				push(t.name, smp.Cpu, smp.Mem, smp.Limit)
			}
		}
	}
}

func appendCapped(list []float64, v float64) []float64 {
	out := make([]float64, 0, min(len(list)+1, History))
	if len(list) >= History {
		list = list[len(list)-History+1:]
	}
	out = append(out, list...)
	return append(out, v)
}

// ---------------------------------------------------------------- config reload

type configWatch struct {
	stop  chan struct{}
	timer *time.Timer
	mu    sync.Mutex
}

// Reload applies a new config: it restarts the running services whose definition changed, stops the removed ones
// and starts the added ones that have `autostart`. Without next it re-reads the config file.
func (s *Supervisor) Reload(next *config.OrbitConfig) (config.ConfigDiff, error) {
	// one reload at a time: a second one diffs against the config the first one left
	s.reloadMu.Lock()
	defer s.reloadMu.Unlock()
	return s.applyConfig(next)
}

func (s *Supervisor) applyConfig(given *config.OrbitConfig) (config.ConfigDiff, error) {
	prev := s.Config()
	loaded := given
	if loaded == nil {
		var err error
		loaded, err = config.LoadConfig(config.LoadOptions{File: prev.File, Dir: prev.Root, AllowEmpty: true})
		if err != nil {
			return config.ConfigDiff{}, err
		}
	}
	// the name is part of the state dir and the socket: it stays what this session started with
	n := *loaded
	n.Name = prev.Name
	next := &n
	diff := config.DiffConfig(prev, next)
	if loaded.Name != prev.Name {
		diff.NameChanged = &config.NameChange{From: prev.Name, To: loaded.Name}
	}
	s.mu.Lock()
	s.pending = nil
	s.mu.Unlock()
	if diff.IsEmpty() {
		s.configBus.emit(diff)
		return diff, nil
	}

	var restart []string
	for _, name := range diff.RestartNames() {
		if s.IsUp(name) {
			restart = append(restart, name)
		}
	}
	// with the old config: the stop_timeout and post_stop hooks of the version that was running
	var wg sync.WaitGroup
	for _, name := range append(slices.Clone(diff.Removed), restart...) {
		wg.Add(1)
		go func() { defer wg.Done(); s.Stop(name, StopOptions{NoDependents: true}) }()
	}
	wg.Wait()
	for _, name := range restart {
		s.Log(name, "↻ orbit.yaml changed — restarting")
	}

	resetWatch := map[string]bool{}
	for _, c := range diff.Changed {
		if slices.Contains(c.Fields, "watch") || slices.Contains(c.Fields, "cwd") {
			resetWatch[c.Name] = true
		}
	}
	for _, name := range diff.Removed {
		resetWatch[name] = true
	}
	var closing []*watch.FileWatcher
	s.mu.Lock()
	for name := range resetWatch {
		if rt := s.rt[name]; rt != nil && rt.watcher != nil {
			closing = append(closing, rt.watcher)
			rt.watcher = nil
		}
	}
	for _, name := range diff.Removed {
		delete(s.states, name)
		delete(s.rt, name)
	}
	s.cfg = next
	s.refreshGraphLocked()
	for _, name := range diff.Added {
		s.initRuntimeLocked(name)
	}
	type reschedule struct {
		name string
		gen  int
	}
	var resched []reschedule
	var clearWatch []string
	for _, c := range diff.Changed {
		rt := s.rt[c.Name]
		svc := next.Services[c.Name]
		if rt == nil || svc == nil {
			continue
		}
		if slices.Contains(c.Fields, "readyWhen") {
			rt.readyMatch = nil
			if svc.ReadyWhen != nil {
				rt.readyMatch, _ = config.CompileLogPattern(svc.ReadyWhen.Log)
			}
			if !s.isUpLocked(c.Name) {
				rt.logReady = svc.ReadyWhen == nil
			}
		}
		if slices.Contains(c.Fields, "health") && s.isUpLocked(c.Name) && !c.Restart {
			resched = append(resched, reschedule{c.Name, rt.gen})
		}
		if svc.Watch == nil {
			clearWatch = append(clearWatch, c.Name)
		}
	}
	s.mu.Unlock()
	for _, w := range closing {
		w.Close()
	}
	for _, r := range resched {
		s.scheduleHealth(r.name, r.gen, 0)
	}
	for _, name := range clearWatch {
		s.update(name, func(st *ServiceState) { st.Watch = "" })
	}
	s.startWatchers()
	s.closeConfigWatchers()
	s.watchConfigFiles()
	s.persist()
	s.configBus.emit(diff)
	s.changeBus.emit("")

	toStart := slices.Clone(restart)
	for _, name := range diff.Added {
		if next.Services[name].Autostart {
			toStart = append(toStart, name)
		}
	}
	if len(toStart) > 0 {
		go s.StartMany(toStart)
	}
	return diff, nil
}

// configFiles are the files whose edit may change the config: the yaml, the .env that interpolates it, and the
// compose files with theirs.
func (s *Supervisor) configFiles() []string {
	cfg := s.Config()
	seen := map[string]bool{}
	var files []string
	add := func(f string) {
		if !seen[f] {
			seen[f] = true
			files = append(files, f)
		}
	}
	if cfg.File != "" {
		add(cfg.File)
		add(filepath.Join(cfg.Root, ".env"))
	}
	for _, name := range cfg.Order {
		if f := cfg.Services[name].ComposeFile; f != "" {
			add(f)
			add(filepath.Join(filepath.Dir(f), ".env"))
		}
	}
	return files
}

// watchConfigFiles watches the config files; a change is diffed against the running config and offered, never
// applied by itself.
func (s *Supervisor) watchConfigFiles() {
	s.mu.Lock()
	if s.disposed || s.configWatcher != nil || s.cfg.File == "" {
		s.mu.Unlock()
		return
	}
	cw := &configWatch{stop: make(chan struct{})}
	s.configWatcher = cw
	s.mu.Unlock()

	check := func() {
		cw.mu.Lock()
		defer cw.mu.Unlock()
		if cw.timer != nil {
			cw.timer.Stop()
		}
		cw.timer = time.AfterFunc(300*time.Millisecond, s.checkConfig)
	}
	stamp := func() string {
		var b strings.Builder
		for _, f := range s.configFiles() {
			if st, err := os.Stat(f); err == nil {
				fmt.Fprintf(&b, "%d:%d|", st.ModTime().UnixNano(), st.Size())
			} else {
				b.WriteString("|")
			}
		}
		return b.String()
	}
	// editors replace files instead of writing them, which kills a watcher on the file itself: watch the directories,
	// and back them up with a cheap stat poll (the OS watchers can miss events on network or bind mounts)
	watchDirs(s.configFiles(), cw.stop, check)
	last := stamp()
	go func() {
		t := time.NewTicker(time.Second)
		defer t.Stop()
		for {
			select {
			case <-cw.stop:
				return
			case <-t.C:
				if now := stamp(); now != last {
					last = now
					check()
				}
			}
		}
	}()
}

func (s *Supervisor) closeConfigWatchers() {
	s.mu.Lock()
	cw := s.configWatcher
	s.configWatcher = nil
	s.mu.Unlock()
	if cw == nil {
		return
	}
	cw.mu.Lock()
	if cw.timer != nil {
		cw.timer.Stop()
	}
	cw.mu.Unlock()
	close(cw.stop)
}

func (s *Supervisor) checkConfig() {
	s.mu.Lock()
	if s.disposed {
		s.mu.Unlock()
		return
	}
	cur := s.cfg
	s.mu.Unlock()

	var pending *PendingConfig
	next, err := config.LoadConfig(config.LoadOptions{File: cur.File, Dir: cur.Root, AllowEmpty: true})
	if err != nil {
		pending = &PendingConfig{Error: err.Error()}
	} else {
		n := *next
		n.Name = cur.Name
		diff := config.DiffConfig(cur, &n)
		if next.Name != cur.Name {
			diff.NameChanged = &config.NameChange{From: cur.Name, To: next.Name}
		}
		if !diff.IsEmpty() {
			pending = &PendingConfig{Diff: &diff}
		}
	}
	a, _ := json.Marshal(pending)
	s.mu.Lock()
	b, _ := json.Marshal(s.pending)
	if string(a) == string(b) {
		s.mu.Unlock()
		return
	}
	s.pending = pending
	s.mu.Unlock()
	s.pendingBus.emit(pending)
	s.changeBus.emit("")
}
