package ipc

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/JGoterris/orbit/internal/config"
	"github.com/JGoterris/orbit/internal/core/notify"
	"github.com/JGoterris/orbit/internal/core/supervisor"
	"github.com/JGoterris/orbit/internal/platform"
	"github.com/JGoterris/orbit/internal/state"
)

// startupTimeout: the daemon re-attaches to containers before it listens, which can take a few seconds.
const startupTimeout = 20 * time.Second

func stamp(format string, a ...any) {
	fmt.Printf("%s %s\n", time.Now().UTC().Format("2006-01-02T15:04:05.000Z"), fmt.Sprintf(format, a...))
}

// DaemonOptions of RunDaemon.
type DaemonOptions struct {
	// IdleMinutes quits after this many minutes with nothing running and nobody connected (0: never)
	IdleMinutes float64
	Version     string
}

// RunDaemon is `orbit daemon`: the supervisor of a project without a UI. It owns the project's lock and its
// socket, and keeps health checks, restarts and watchers running while clients come and go. It returns the exit
// code once a client asks it to quit (shutdown) or after IdleMinutes with nothing running and nobody connected.
func RunDaemon(cfg *config.OrbitConfig, opts DaemonOptions) int {
	sup := supervisor.New(cfg, supervisor.Options{})
	if holder := state.AcquireLock(sup.StateDir()); holder != 0 {
		fmt.Fprintf(os.Stderr, "already open for this project (pid %d)\n", holder)
		return 1
	}
	defer func() {
		sup.KillAllSync()
		state.ReleaseLock(sup.StateDir())
	}()
	signal.Ignore(syscallHUP()...) // the terminal that spawned us may go away

	done := make(chan struct{})
	var once sync.Once
	var srv *Server
	quit := func(how, why string) {
		once.Do(func() {
			go func() {
				stamp("quitting (%s, %s)", why, how)
				if srv != nil {
					srv.Close()
				}
				if how == "detach" {
					sup.Detach()
				} else {
					finished := make(chan struct{})
					go func() { sup.Dispose(); close(finished) }()
					select {
					case <-finished:
					case <-time.After(20 * time.Second):
					}
				}
				close(done)
			}()
		})
	}
	srv = NewServer(sup, ServerOptions{Version: opts.Version, OnShutdown: func(how string) { quit(how, "asked by a client") }})
	sigs := make(chan os.Signal, 2)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
	go func() {
		s := <-sigs
		quit("stop", s.String())
	}()

	// init first: a client that connects must not race the re-attach of what a previous session left running
	sup.Init()
	started, err := srv.Start()
	if err != nil || !started {
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
		} else {
			fmt.Fprintln(os.Stderr, "another orbit already serves this project")
		}
		state.ReleaseLock(sup.StateDir())
		return 1
	}
	notify.Attach(sup, notify.Options{})
	if b := notify.DefaultBackend(); b != nil {
		stamp("desktop notifications: %s", b.Name)
	} else {
		stamp("desktop notifications: no notifier found (notify-send, osascript or powershell)")
	}
	stamp("orbit daemon for %s ready (pid %d, socket %s)", cfg.Name, os.Getpid(), srv.Path())

	if opts.IdleMinutes > 0 {
		go func() {
			var idleSince time.Time
			t := time.NewTicker(10 * time.Second)
			defer t.Stop()
			for {
				select {
				case <-done:
					return
				case <-t.C:
					if srv.ClientCount() > 0 || sup.RunningCount() > 0 {
						idleSince = time.Time{}
						continue
					}
					if idleSince.IsZero() {
						idleSince = time.Now()
					}
					if time.Since(idleSince) >= time.Duration(opts.IdleMinutes*float64(time.Minute)) {
						quit("stop", fmt.Sprintf("idle for %g min", opts.IdleMinutes))
					}
				}
			}
		}()
	}
	<-done
	return 0
}

// Connect is the orbit that serves this project, started as an `orbit daemon` when there is none. It fails with
// a readable message when the project is open somewhere that does not answer.
func ConnectProject(cfg *config.OrbitConfig) (*Remote, error) {
	dir := state.Dir(cfg)
	path := SocketPath(dir)
	attempt := func() (*Remote, error) {
		r, err := ConnectRemote(path)
		if err == nil {
			return r, nil
		}
		var pe *fs.PathError
		if errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ECONNREFUSED) || errors.As(err, &pe) || isTimeout(err) {
			return nil, nil
		}
		return nil, err
	}

	if r, err := attempt(); r != nil || err != nil {
		return r, err
	}

	// someone holds the lock but does not listen yet (a daemon still starting): wait for it instead of racing it
	spawned := false
	if state.ReadLock(dir) == 0 {
		if err := spawnDaemon(cfg, dir); err != nil {
			return nil, err
		}
		spawned = true
	}

	began := time.Now()
	for time.Since(began) < startupTimeout {
		time.Sleep(50 * time.Millisecond)
		if r, err := attempt(); r != nil || err != nil {
			return r, err
		}
		if spawned && time.Since(began) > 1500*time.Millisecond && state.ReadLock(dir) == 0 {
			break // it took no lock: it died on startup
		}
	}
	if holder := state.ReadLock(dir); holder != 0 && !spawned {
		return nil, fmt.Errorf("orbit is open for this project (pid %d) but does not answer", holder)
	}
	return nil, fmt.Errorf("the orbit daemon did not start; see %s", filepath.Join(dir, "daemon.log"))
}

func isTimeout(err error) bool {
	var t interface{ Timeout() bool }
	return errors.As(err, &t) && t.Timeout()
}

func spawnDaemon(cfg *config.OrbitConfig, dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	logf, err := os.OpenFile(filepath.Join(dir, "daemon.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer logf.Close()
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	// `-c` when the project came from a config file, the folder otherwise: either way it is the same project
	target := []string{cfg.Root}
	if cfg.File != "" {
		target = []string{"-c", cfg.File}
	}
	cmd := exec.Command(exe, append([]string{"daemon"}, target...)...)
	cmd.Dir = cfg.Root
	cmd.Env = os.Environ()
	cmd.Stdout, cmd.Stderr = logf, logf
	platform.SetDetached(cmd) // its own session: closing our terminal must not take it down
	if err := cmd.Start(); err != nil {
		return err
	}
	// reap it when it ends, so a daemon that quits while we are still around does not linger as a zombie
	go cmd.Wait()
	return nil
}

// IsServed reports whether an orbit already serves this project's socket.
func IsServed(cfg *config.OrbitConfig) bool {
	c, err := Connect(SocketPath(state.Dir(cfg)), 500*time.Millisecond)
	if err != nil {
		return false
	}
	c.Close()
	return true
}
