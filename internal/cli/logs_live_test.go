//go:build !windows

package cli

import (
	"testing"
	"time"

	"github.com/JGoterris/orbit/internal/config"
	"github.com/JGoterris/orbit/internal/core/logs"
	"github.com/JGoterris/orbit/internal/core/supervisor"
	"github.com/JGoterris/orbit/internal/ipc"
)

func TestLogsFromARunningOrbitAreInterleaved(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root := t.TempDir()
	svc := &config.ServiceConfig{
		Name: "api", Type: config.TypeProcess, Cwd: root, Env: map[string]string{}, EnvFiles: []config.EnvFileRef{},
		Cmd: "echo o1; sleep 0.3; echo e1 >&2; sleep 0.3; echo o2", DependsOn: []string{}, Restart: config.RestartNo,
		StartTimeout: 5000, StopTimeout: 1000, Ports: []string{}, Volumes: []string{}, DockerArgs: []string{},
	}
	cfg := &config.OrbitConfig{Root: root, Name: "live", Order: []string{"api"}, Services: map[string]*config.ServiceConfig{"api": svc},
		Groups: map[string][]string{}}
	sup := supervisor.New(cfg, supervisor.Options{})
	t.Cleanup(sup.Dispose)
	srv := ipc.NewServer(sup, ipc.ServerOptions{Version: "test"})
	if started, err := srv.Start(); err != nil || !started {
		t.Fatalf("server: %v %v", started, err)
	}
	t.Cleanup(srv.Close)
	sup.Start("api")
	for i := 0; i < 100 && sup.State("api").Status != supervisor.Exited; i++ {
		time.Sleep(50 * time.Millisecond)
	}
	time.Sleep(200 * time.Millisecond) // the file tails poll

	var order []string
	sawSystem := false
	for _, l := range logsJSON(t, cfg, nil, 50) {
		if l.Stream == logs.System {
			sawSystem = true
			continue
		}
		order = append(order, l.Text)
	}
	if !sawSystem || len(order) != 3 || order[0] != "o1" || order[1] != "e1" || order[2] != "o2" {
		t.Fatalf("lines = %v (system lines: %v)", order, sawSystem)
	}
}
