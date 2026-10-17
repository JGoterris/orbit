//go:build !windows

package cli

import (
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/JGoterris/orbit/internal/config"
)

// A kill that reaches orbit alone must stop the task too, and orbit reports it the way a shell would.
func TestRunTaskForwardsTerminationToTheTask(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.OrbitConfig{Root: dir, Tasks: map[string]*config.TaskConfig{
		"slow": {Name: "slow", Cmd: "sleep 30", Cwd: dir, Env: map[string]string{}},
	}}
	go func() {
		time.Sleep(300 * time.Millisecond)
		syscall.Kill(os.Getpid(), syscall.SIGTERM)
	}()
	done := make(chan int, 1)
	go func() { done <- RunTask(cfg, "slow", nil) }()
	select {
	case code := <-done:
		if code != 128+int(syscall.SIGTERM) {
			t.Fatalf("exit code = %d, want %d", code, 128+int(syscall.SIGTERM))
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the task was not stopped")
	}
}
