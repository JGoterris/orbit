// Package session holds the project that is open and swaps it for another one without restarting the interface.
package session

import (
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/JGoterris/orbit/internal/config"
	"github.com/JGoterris/orbit/internal/core/notify"
	"github.com/JGoterris/orbit/internal/core/supervisor"
	"github.com/JGoterris/orbit/internal/ipc"
	"github.com/JGoterris/orbit/internal/projects"
	"github.com/JGoterris/orbit/internal/state"
)

// Relaunch is an orbit.yaml on disk that differs from what the orbit that already runs the project has loaded.
type Relaunch struct {
	Diff   config.ConfigDiff
	Config *config.OrbitConfig
}

// Options of New.
type Options struct {
	// Version is what the local socket server reports
	Version string
	// Daemon: projects opened from here run in an `orbit daemon` (started if needed) and this process only
	// mirrors them; without it they run in this process.
	Daemon bool
	// OnShutdown runs when a client asks over the socket to quit orbit (`orbit down`).
	OnShutdown func(how string)
}

// Session is the open project.
type Session struct {
	opts Options

	mu         sync.Mutex
	sup        supervisor.Like
	srv        *ipc.Server
	stopNotify func()
}

// New wraps an already created supervisor (its lock, if local, is the caller's).
func New(sup supervisor.Like, opts Options) *Session {
	s := &Session{opts: opts, sup: sup}
	s.notifyOf(sup)
	return s
}

// Sup is the project on screen.
func (s *Session) Sup() supervisor.Like {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sup
}

// notifyOf: services that run in this process (not in a daemon) notify the desktop from here.
func (s *Session) notifyOf(sup supervisor.Like) {
	if s.stopNotify != nil {
		s.stopNotify()
		s.stopNotify = nil
	}
	if local, ok := sup.(*supervisor.Supervisor); ok {
		s.stopNotify = notify.Attach(local, notify.Options{})
	}
}

// Serve exposes the open project on its socket. Best effort: orbit works the same without it.
func (s *Session) Serve() {
	s.CloseIPC()
	sup := s.Sup()
	if _, remote := sup.(*ipc.Remote); remote {
		return // its daemon already serves it
	}
	srv := ipc.NewServer(sup, ipc.ServerOptions{Version: s.opts.Version, OnShutdown: s.opts.OnShutdown})
	if started, err := srv.Start(); err == nil && started {
		s.mu.Lock()
		s.srv = srv
		s.mu.Unlock()
	}
}

// CloseIPC stops answering on the socket.
func (s *Session) CloseIPC() {
	s.mu.Lock()
	srv := s.srv
	s.srv = nil
	s.mu.Unlock()
	if srv != nil {
		srv.Close()
	}
}

// SwitchTo opens the project in dir: how says what happens to the services of the current one ("stop" them, or
// "detach" and leave them running to be picked up the next time that project is opened). On error nothing changed.
func (s *Session) SwitchTo(dir, how string) (supervisor.Like, *Relaunch, error) {
	prev := s.Sup()
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return nil, nil, fmt.Errorf("%s is not a folder", dir)
	}
	// allowEmpty would happily turn a typo into a project with no services, hence the check above
	cfg, err := config.LoadConfig(config.LoadOptions{Dir: dir, AllowEmpty: true, SavedProfiles: state.SavedProfiles})
	if err != nil {
		return nil, nil, err
	}
	if state.Dir(cfg) == prev.StateDir() {
		return nil, nil, fmt.Errorf("%s is already open", cfg.Name)
	}

	var next supervisor.Like
	var relaunch *Relaunch
	if s.opts.Daemon && len(cfg.Services) > 0 {
		remote, err := ipc.ConnectProject(cfg)
		if err != nil {
			return nil, nil, err // a daemon that would not start
		}
		next = remote
		// a daemon that was already running keeps the config it started with: offer what changed since
		if diff := config.DiffConfig(remote.Config(), cfg); !diff.IsEmpty() {
			relaunch = &Relaunch{Diff: diff, Config: cfg}
		}
	} else {
		local := supervisor.New(cfg, supervisor.Options{})
		if holder := state.AcquireLock(local.StateDir()); holder != 0 {
			return nil, nil, fmt.Errorf("%s is already open in another orbit (pid %d)", cfg.Name, holder)
		}
		next = local
	}

	s.CloseIPC()
	if s.stopNotify != nil {
		s.stopNotify()
		s.stopNotify = nil
	}
	if how == "detach" {
		prev.Detach()
	} else {
		done := make(chan struct{})
		go func() { prev.Dispose(); close(done) }()
		select {
		case <-done:
		case <-time.After(20 * time.Second):
		}
	}
	state.ReleaseLock(prev.StateDir())

	s.mu.Lock()
	s.sup = next
	s.mu.Unlock()
	s.notifyOf(next)
	projects.Register(cfg.Root, cfg.Name, 0)
	if _, remote := next.(*ipc.Remote); !remote {
		s.Serve()
	}
	go next.Init()
	return next, relaunch, nil
}
