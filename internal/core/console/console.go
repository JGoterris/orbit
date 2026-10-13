// Package console runs interactive programs (psql, a shell, `docker exec -it`) on a pseudo-terminal, for the `i`
// console of a service.
package console

import (
	"errors"
	"fmt"
	"io"
	"slices"
	"sync"
	"syscall"
	"time"

	"github.com/JGoterris/orbit/internal/config"
	"github.com/JGoterris/orbit/internal/platform"
	"github.com/JGoterris/orbit/internal/proc"
)

// Spec is what to run.
type Spec struct {
	Argv []string
	Cwd  string
	Env  map[string]string
	// Title is what the modal shows
	Title string
}

// ServiceState is the part of a service's runtime state that decides what its console can be.
type ServiceState struct {
	Status      string
	ContainerID string
}

const (
	backlogBytes   = 2 * 1024 * 1024
	containerShell = "command -v bash >/dev/null && exec bash || exec sh"
)

// Command is what `i` runs for a service. A process (or an external service, on the host) gets its `console:`
// command (or $SHELL) in its own cwd and env; a container gets `docker exec -it`, so it has to be running.
func Command(svc *config.ServiceConfig, st ServiceState) (Spec, error) {
	if !ptySupported() {
		return Spec{}, errors.New("the console needs a pseudo-terminal, which this platform does not offer")
	}
	if svc.Type == config.TypeProcess || svc.Type == config.TypeExternal {
		env, err := config.ReadEnvFiles(svc.EnvFiles)
		if err != nil {
			return Spec{}, err
		}
		for k, v := range svc.Env {
			env[k] = v
		}
		env["ORBIT_SERVICE"] = svc.Name
		shell := platform.UserShell()
		spec := Spec{Argv: []string{shell}, Cwd: svc.Cwd, Env: env, Title: shell}
		if svc.Console != "" {
			spec.Argv = platform.ShellArgv(svc.Console, svc.Shell, platform.IsWindows)
			spec.Title = svc.Console
		}
		return spec, nil
	}
	if st.ContainerID == "" || !slices.Contains([]string{"running", "healthy", "unhealthy"}, st.Status) {
		return Spec{}, fmt.Errorf("%s is not running", svc.Name)
	}
	cmd, title := containerShell, "docker exec shell"
	if svc.Console != "" {
		cmd, title = svc.Console, "docker exec "+svc.Console
	}
	return Spec{Argv: []string{"docker", "exec", "-it", st.ContainerID, "sh", "-c", cmd}, Env: map[string]string{}, Title: title}, nil
}

// ptyProcess is a program attached to a pseudo-terminal; one implementation per OS.
type ptyProcess interface {
	io.ReadWriter
	Pid() int
	Resize(cols, rows int) error
	// Wait blocks until the program ends and returns its exit code
	Wait() int
	Signal(sig syscall.Signal)
	Close() error
}

// Session is an interactive program on a PTY. It keeps the raw output so a modal opened later can repaint the screen.
type Session struct {
	Spec Spec

	p       ptyProcess
	mu      sync.Mutex
	chunks  [][]byte
	size    int
	subs    map[int]func([]byte)
	nextSub int
	code    int
	alive   bool
	done    chan struct{}
}

// NewSession starts the program with a terminal of cols x rows.
func NewSession(spec Spec, cols, rows int) (*Session, error) {
	if !ptySupported() {
		return nil, errors.New("pseudo-terminals are not supported on this platform")
	}
	if len(spec.Argv) == 0 {
		return nil, errors.New("empty command")
	}
	extra := map[string]string{}
	for k, v := range spec.Env {
		extra[k] = v
	}
	extra["TERM"] = "xterm-256color"
	p, err := startPTY(spec.Argv, spec.Cwd, proc.EnvList(extra), cols, rows)
	if err != nil {
		return nil, err
	}
	s := &Session{Spec: spec, p: p, subs: map[int]func([]byte){}, alive: true, done: make(chan struct{})}

	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		buf := make([]byte, 32*1024)
		for {
			n, err := p.Read(buf)
			if n > 0 {
				s.push(append([]byte(nil), buf[:n]...))
			}
			if err != nil {
				return
			}
		}
	}()
	go func() {
		code := p.Wait()
		// the last output can still be in flight in the pty: let the reader drain it before closing
		select {
		case <-readDone:
		case <-time.After(500 * time.Millisecond):
		}
		p.Close()
		s.mu.Lock()
		s.code, s.alive = code, false
		s.mu.Unlock()
		close(s.done)
	}()
	return s, nil
}

func (s *Session) push(data []byte) {
	s.mu.Lock()
	s.chunks = append(s.chunks, data)
	s.size += len(data)
	for s.size > backlogBytes && len(s.chunks) > 1 {
		s.size -= len(s.chunks[0])
		s.chunks = s.chunks[1:]
	}
	subs := make([]func([]byte), 0, len(s.subs))
	for _, f := range s.subs {
		subs = append(subs, f)
	}
	s.mu.Unlock()
	for _, f := range subs {
		f(data)
	}
}

// Subscribe calls f with every chunk of output from now on; the returned function stops it. f runs on the reader
// goroutine, so it must not block for long.
func (s *Session) Subscribe(f func([]byte)) (unsubscribe func()) {
	s.mu.Lock()
	id := s.nextSub
	s.nextSub++
	s.subs[id] = f
	s.mu.Unlock()
	return func() {
		s.mu.Lock()
		delete(s.subs, id)
		s.mu.Unlock()
	}
}

// Attach replays everything printed so far to f and then keeps calling it with new output, without losing or
// reordering a byte in between: this is how a modal opened later paints a fresh emulator. f runs while the session
// holds its lock for the replay, and on the reader goroutine afterwards, so it must not call back into the Session.
func (s *Session) Attach(f func([]byte)) (detach func()) {
	s.mu.Lock()
	id := s.nextSub
	s.nextSub++
	s.subs[id] = f
	if s.size > 0 {
		backlog := make([]byte, 0, s.size)
		for _, c := range s.chunks {
			backlog = append(backlog, c...)
		}
		f(backlog)
	}
	s.mu.Unlock()
	return func() {
		s.mu.Lock()
		delete(s.subs, id)
		s.mu.Unlock()
	}
}

// Backlog is everything the program has printed (bounded), to replay into a fresh emulator.
func (s *Session) Backlog() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]byte, 0, s.size)
	for _, c := range s.chunks {
		out = append(out, c...)
	}
	return out
}

func (s *Session) Alive() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.alive
}

// ExitCode is only meaningful once Done is closed.
func (s *Session) ExitCode() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.code
}

// Done is closed when the program has ended and its output has been drained.
func (s *Session) Done() <-chan struct{} { return s.done }

// Write sends keystrokes (or pasted text) to the program.
func (s *Session) Write(data []byte) {
	if s.Alive() {
		s.p.Write(data)
	}
}

func (s *Session) Resize(cols, rows int) {
	if s.Alive() {
		s.p.Resize(cols, rows)
	}
}

// Kill hangs the program up and, if it is still there a second later, kills it.
func (s *Session) Kill() {
	if !s.Alive() {
		return
	}
	s.p.Signal(syscall.SIGHUP)
	time.AfterFunc(time.Second, func() {
		if s.Alive() {
			s.p.Signal(syscall.SIGKILL)
		}
	})
}

// Manager keeps one live console per service: closing the modal keeps it, `i` brings it back.
type Manager struct {
	mu       sync.Mutex
	sessions map[string]*Session
}

func NewManager() *Manager { return &Manager{sessions: map[string]*Session{}} }

func (m *Manager) Has(name string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.sessions[name]
	return s != nil && s.Alive()
}

func (m *Manager) Get(name string) *Session {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sessions[name]
}

// Open returns the live session of the service, or starts a new one.
func (m *Manager) Open(name string, spec Spec, cols, rows int) (*Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if cur := m.sessions[name]; cur != nil && cur.Alive() {
		return cur, nil
	}
	s, err := NewSession(spec, cols, rows)
	if err != nil {
		return nil, err
	}
	m.sessions[name] = s
	return s, nil
}

// Discard forgets a finished session (its modal was dismissed).
func (m *Manager) Discard(name string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s := m.sessions[name]; s != nil && !s.Alive() {
		delete(m.sessions, name)
	}
}

// CloseAll ends every console: quitting orbit must not leave a psql behind.
func (m *Manager) CloseAll() {
	m.mu.Lock()
	sessions := m.sessions
	m.sessions = map[string]*Session{}
	m.mu.Unlock()
	for _, s := range sessions {
		s.Kill()
	}
}
