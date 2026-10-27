package plugins

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/JGoterris/orbit/internal/platform"
	"github.com/JGoterris/orbit/pluginsdk/protocol"
)

type Status string

const (
	StatusStarting Status = "starting"
	StatusRunning  Status = "running"
	// StatusFailed: it kept dying (or could not start) and orbit gave up; see its log
	StatusFailed  Status = "failed"
	StatusStopped Status = "stopped"
)

const (
	maxRetries     = 3
	stableAfter    = 30 * time.Second
	maxLogSize     = 1 << 20
	outboxSize     = 512
	DefaultTimeout = 10 * time.Second
	// ActionTimeout is for the actions of a plugin: a merge or a rerun takes longer than a data request
	ActionTimeout = 60 * time.Second
)

// Timings; variables so the tests do not wait for seconds.
var (
	initTimeout  = 10 * time.Second
	shutdownWait = 1500 * time.Millisecond
	retryStep    = 500 * time.Millisecond
)

var ErrNotRunning = errors.New("plugin is not running")

// Process is the process of one plugin: it starts it, talks JSON-RPC over its stdio, restarts it when it dies and
// stops it. Messages the plugin sends on its own go to onMessage; the owner must not block there.
type Process struct {
	plugin    *Plugin
	init      protocol.InitializeParams
	onMessage func(protocol.Message)
	onStatus  func(Status, string)

	mu      sync.Mutex
	status  Status
	lastErr string
	stdin   io.WriteCloser
	pid     int
	done    chan struct{} // closed when the current process is gone
	pending map[int64]chan protocol.Message
	nextID  int64
	stop    chan struct{}
	stopped bool

	wmu sync.Mutex // serializes writes to stdin
	log *os.File

	// outbox holds the notifications for the plugin: they are written by their own goroutine, so a plugin that
	// does not read its stdin cannot block the one that notifies (the log store calls us under its lock)
	outbox  chan []byte
	dropped atomic.Int64
}

func NewProcess(p *Plugin, init protocol.InitializeParams, onMessage func(protocol.Message), onStatus func(Status, string)) *Process {
	return &Process{
		plugin: p, init: init, onMessage: onMessage, onStatus: onStatus,
		status: StatusStopped, pending: map[int64]chan protocol.Message{}, stop: make(chan struct{}),
		outbox: make(chan []byte, outboxSize),
	}
}

func (p *Process) Status() (Status, string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.status, p.lastErr
}

func (p *Process) Start() {
	p.mu.Lock()
	p.status = StatusStarting
	p.mu.Unlock()
	go p.pump()
	go p.supervise()
}

func (p *Process) pump() {
	for {
		select {
		case line := <-p.outbox:
			_ = p.write(line)
		case <-p.stop:
			return
		}
	}
}

func (p *Process) setStatus(s Status, err string) {
	p.mu.Lock()
	if p.status == s && p.lastErr == err {
		p.mu.Unlock()
		return
	}
	p.status, p.lastErr = s, err
	p.mu.Unlock()
	if p.onStatus != nil {
		p.onStatus(s, err)
	}
}

func (p *Process) isStopping() bool {
	select {
	case <-p.stop:
		return true
	default:
		return false
	}
}

func (p *Process) supervise() {
	p.openLog()
	attempt := 0
	for {
		if p.isStopping() {
			p.setStatus(StatusStopped, "")
			return
		}
		began := time.Now()
		fatal, err := p.runOnce()
		if p.isStopping() {
			p.setStatus(StatusStopped, "")
			return
		}
		if time.Since(began) > stableAfter {
			attempt = 0
		}
		msg := "plugin exited"
		if err != nil {
			msg = err.Error()
		}
		p.logf("%s", msg)
		if fatal || attempt >= maxRetries {
			p.setStatus(StatusFailed, msg)
			return
		}
		p.setStatus(StatusStarting, msg)
		attempt++
		select {
		case <-p.stop:
			p.setStatus(StatusStopped, "")
			return
		case <-time.After(time.Duration(attempt) * retryStep):
		}
	}
}

func (p *Process) openLog() {
	file := LogFile(p.plugin.Manifest.Name)
	if os.MkdirAll(filepath.Dir(file), 0o755) != nil {
		return
	}
	if st, err := os.Stat(file); err == nil && st.Size() > maxLogSize {
		os.Remove(file)
	}
	p.log, _ = os.OpenFile(file, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
}

func (p *Process) logf(format string, args ...any) {
	if p.log == nil {
		return
	}
	fmt.Fprintf(p.log, "%s orbit: %s\n", time.Now().Format("15:04:05"), fmt.Sprintf(format, args...))
}

// runOnce starts the process and blocks until it is gone. fatal means that starting again cannot help.
func (p *Process) runOnce() (fatal bool, err error) {
	pl := p.plugin
	argv := Argv(pl.Manifest.Run, pl.Dir)
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = p.init.ProjectDir
	if st, e := os.Stat(cmd.Dir); e != nil || !st.IsDir() {
		cmd.Dir = pl.Dir
	}
	cmd.Env = append(os.Environ(), Env(pl, p.init.ProjectDir)...)
	platform.SetDetached(cmd)
	stdin, _ := cmd.StdinPipe()
	stdout, _ := cmd.StdoutPipe()
	if p.log != nil {
		cmd.Stderr = p.log
	}
	if err := cmd.Start(); err != nil {
		return true, fmt.Errorf("cannot start %s: %w", argv[0], err)
	}
	done := make(chan struct{})
	p.mu.Lock()
	p.stdin, p.pid, p.done = stdin, cmd.Process.Pid, done
	p.mu.Unlock()

	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		protocol.Read(stdout, p.dispatch, func(line []byte) { p.logf("stdout: %s", line) })
	}()

	// the process may have been asked to stop between supervise's check and here
	if p.isStopping() {
		platform.KillTree(cmd.Process.Pid, syscall.SIGKILL)
	}

	initDone := make(chan error, 1)
	go func() {
		if _, e := p.call(protocol.MethodInitialize, p.init, initTimeout); e != nil {
			platform.KillTree(cmd.Process.Pid, syscall.SIGKILL)
			initDone <- e
			return
		}
		p.setStatus(StatusRunning, "")
		initDone <- nil
	}()

	<-readDone
	waitErr := cmd.Wait()
	platform.KillTree(cmd.Process.Pid, syscall.SIGKILL)
	p.mu.Lock()
	p.stdin, p.pid = nil, 0
	pending := p.pending
	p.pending = map[int64]chan protocol.Message{}
	p.mu.Unlock()
	close(done)
	for id, ch := range pending {
		ch <- protocol.Message{ID: &id, Error: &protocol.Error{Code: protocol.ErrInternal, Message: "plugin exited"}}
	}
	// every pending request was answered above, so the initialize call is over
	if initErr := <-initDone; initErr != nil && !p.isStopping() {
		return false, fmt.Errorf("initialize: %w", initErr)
	}
	if waitErr != nil {
		return false, fmt.Errorf("plugin exited: %w", waitErr)
	}
	return false, errors.New("plugin exited")
}

// Env is the environment the plugin p gets, in its long-lived process and in its commands: the way back to orbit
// (ORBIT_BIN, which has the same CLI a script uses) and where its files are.
func Env(p *Plugin, projectDir string) []string {
	exe, _ := os.Executable()
	name := p.Manifest.Name
	return []string{
		"ORBIT_BIN=" + exe,
		"ORBIT_PLUGIN=" + name,
		"ORBIT_PLUGIN_DIR=" + p.Dir,
		"ORBIT_PLUGIN_CONFIG=" + ConfigDir(name),
		"ORBIT_PLUGIN_STATE=" + StateDir(name),
		"ORBIT_PROJECT_DIR=" + projectDir,
	}
}

// dispatch handles what the plugin sends: answers to our requests, notifications, and requests orbit does not have.
func (p *Process) dispatch(m protocol.Message) {
	switch {
	case m.IsResponse():
		p.mu.Lock()
		ch := p.pending[*m.ID]
		delete(p.pending, *m.ID)
		p.mu.Unlock()
		if ch != nil {
			ch <- m
		}
	case m.IsRequest():
		p.write(protocol.Failure(*m.ID, protocol.ErrMethod, "orbit does not handle "+m.Method+": use the orbit CLI"))
	default:
		if p.onMessage != nil {
			p.onMessage(m)
		}
	}
}

func (p *Process) write(line []byte) error {
	p.mu.Lock()
	w := p.stdin
	p.mu.Unlock()
	if w == nil {
		return ErrNotRunning
	}
	p.wmu.Lock()
	defer p.wmu.Unlock()
	_, err := w.Write(line)
	return err
}

// call sends a request and waits for its answer, whatever the status of the process. The timeout covers the
// write too: a plugin that does not read its stdin must not hold the caller (nor Stop) forever.
func (p *Process) call(method string, params any, timeout time.Duration) (json.RawMessage, error) {
	ch := make(chan protocol.Message, 1)
	p.mu.Lock()
	p.nextID++
	id := p.nextID
	p.pending[id] = ch
	p.mu.Unlock()
	forget := func() {
		p.mu.Lock()
		delete(p.pending, id)
		p.mu.Unlock()
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	written := make(chan error, 1)
	go func() { written <- p.write(protocol.Request(id, method, params)) }()
	select {
	case err := <-written:
		if err != nil {
			forget()
			return nil, err
		}
	case <-timer.C:
		forget()
		return nil, fmt.Errorf("%s did not answer in %s", method, timeout)
	}
	select {
	case m := <-ch:
		if m.Error != nil {
			return nil, m.Error
		}
		return m.Result, nil
	case <-timer.C:
		forget()
		return nil, fmt.Errorf("%s did not answer in %s", method, timeout)
	}
}

// Call asks the plugin something and decodes the answer into result (nil to ignore it). It fails at once while
// the plugin is not running.
func (p *Process) Call(method string, params, result any, timeout time.Duration) error {
	if s, _ := p.Status(); s != StatusRunning {
		return fmt.Errorf("plugin %s is %s: %w", p.plugin.Manifest.Name, s, ErrNotRunning)
	}
	raw, err := p.call(method, params, timeout)
	if err != nil {
		return err
	}
	if result != nil && len(raw) > 0 {
		return json.Unmarshal(raw, result)
	}
	return nil
}

// Notify queues a notification and returns at once. It is dropped when the plugin is not running or does not keep
// up (a full queue): a plugin is told what happens, not made a promise to be told everything.
func (p *Process) Notify(method string, params any) {
	if s, _ := p.Status(); s != StatusRunning {
		return
	}
	select {
	case p.outbox <- protocol.Notification(method, params):
	default:
		p.dropped.Add(1)
	}
}

// KillSync kills the process and what it started without waiting: the last resort when orbit itself is exiting.
func (p *Process) KillSync() {
	p.mu.Lock()
	pid := p.pid
	p.mu.Unlock()
	if pid > 0 {
		platform.KillTree(pid, syscall.SIGKILL)
	}
}

// Stop asks the plugin to shut down and kills it if it does not do it in time. It does not return before the
// process is gone.
func (p *Process) Stop() {
	p.mu.Lock()
	if p.stopped {
		p.mu.Unlock()
		return
	}
	p.stopped = true
	close(p.stop)
	done, pid := p.done, p.pid
	p.mu.Unlock()
	if done == nil {
		return
	}
	// a plugin that does not answer shutdown is hung: do not wait for it to exit as well
	if _, err := p.call(protocol.MethodShutdown, struct{}{}, shutdownWait); err == nil {
		p.mu.Lock()
		if p.stdin != nil {
			p.stdin.Close()
		}
		p.mu.Unlock()
		select {
		case <-done:
			p.closeLog()
			return
		case <-time.After(shutdownWait):
		}
	}
	platform.KillTree(pid, syscall.SIGTERM)
	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		platform.KillTree(pid, syscall.SIGKILL)
		<-done
	}
	p.closeLog()
}

func (p *Process) closeLog() {
	if p.log != nil {
		p.log.Close()
	}
}
