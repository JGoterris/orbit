//go:build !windows

package ipc

import (
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/JGoterris/orbit/internal/core/supervisor"
	"github.com/JGoterris/orbit/internal/platform"
	"github.com/JGoterris/orbit/internal/state"
)

func TestRunningListsTheOrbitsOfAnyProject(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	if got := Running(); len(got) != 0 {
		t.Fatalf("nothing runs yet: %+v", got)
	}

	cfg := cfgOf("elsewhere", svc("a", "sleep 30"))
	sup := supervisor.New(cfg, supervisor.Options{})
	t.Cleanup(sup.Dispose)
	if holder := state.AcquireLock(sup.StateDir()); holder != 0 {
		t.Fatalf("lock held by %d", holder)
	}
	t.Cleanup(func() { state.ReleaseLock(sup.StateDir()) })
	srv := NewServer(sup, ServerOptions{Version: "test", Daemon: true})
	if started, err := srv.Start(); err != nil || !started {
		t.Fatalf("server: %v %v", started, err)
	}
	t.Cleanup(srv.Close)

	got := Running()
	if len(got) != 1 || got[0].Config == nil || got[0].Config.Name != "elsewhere" || !got[0].Daemon || got[0].StateDir != sup.StateDir() {
		t.Fatalf("Running() = %+v", got)
	}

	// holding the project without listening is reported too, without a config
	srv.Close()
	got = Running()
	if len(got) != 1 || got[0].Config != nil {
		t.Fatalf("after closing the socket: %+v", got)
	}
}

func TestRunningDoesNotHangOnAnOrbitThatDoesNotAnswer(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	dir := filepath.Join(platform.StateBase(), "orbit", "hung-0123abcd")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if holder := state.AcquireLock(dir); holder != 0 {
		t.Fatalf("lock held by %d", holder)
	}
	t.Cleanup(func() { state.ReleaseLock(dir) })
	l, err := net.Listen("unix", SocketPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	go func() {
		// accepts and keeps the connection open, like a stopped process whose kernel still queues the connection
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			t.Cleanup(func() { c.Close() })
		}
	}()

	began := time.Now()
	got := Running()
	if len(got) != 1 || got[0].Config != nil || got[0].Pid != os.Getpid() {
		t.Fatalf("Running() = %+v", got)
	}
	if took := time.Since(began); took > 5*time.Second {
		t.Fatalf("Running() took %v", took)
	}
}
