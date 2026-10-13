// Package hooks runs the commands of a service's pre_start, post_start and post_stop phases.
package hooks

import (
	"fmt"
	"math"
	"os/exec"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/JGoterris/orbit/internal/config"
	"github.com/JGoterris/orbit/internal/core/logs"
	"github.com/JGoterris/orbit/internal/platform"
	"github.com/JGoterris/orbit/internal/proc"
)

type Phase string

const (
	PreStart  Phase = "pre_start"
	PostStart Phase = "post_start"
	PostStop  Phase = "post_stop"
)

// Context is what a hook runs with.
type Context struct {
	Service string
	Cwd     string
	// Shell is the `shell:` of the service
	Shell string
	Env   map[string]string
	// ExitCode is the exit code of the service, for post_stop after it ended on its own
	ExitCode *int
	Log      func(stream logs.Stream, text string)
	// Track tells the owner the pid of the hook running right now (0 between hooks), so it can kill it
	Track func(pid int)
}

// Result of a phase.
type Result struct {
	OK    bool
	Error string
}

// Run runs the hooks of one phase in order through the shell, in the service's cwd; it stops at the first failure.
func Run(phase Phase, hooks []config.Hook, ctx Context) Result {
	for _, hook := range hooks {
		started := time.Now()
		ctx.Log(logs.System, fmt.Sprintf("▸ %s: %s", phase, hook.Cmd))

		argv, verbatim := platform.ShellCommand(hook.Cmd, ctx.Shell, platform.IsWindows)
		extra := map[string]string{"FORCE_COLOR": "1"}
		for k, v := range ctx.Env {
			extra[k] = v
		}
		extra["ORBIT_SERVICE"] = ctx.Service
		extra["ORBIT_HOOK"] = string(phase)
		if ctx.ExitCode != nil {
			extra["ORBIT_EXIT_CODE"] = strconv.Itoa(*ctx.ExitCode)
		}
		cmd := exec.Command(argv[0], argv[1:]...)
		cmd.Dir = ctx.Cwd
		cmd.Env = proc.EnvList(extra)
		// own process group: a timeout or a stop kills the whole tree
		platform.SetDetached(cmd)
		if verbatim {
			platform.SetVerbatimArgs(cmd, argv)
		}
		stdout, err1 := cmd.StdoutPipe()
		stderr, err2 := cmd.StderrPipe()
		if err := firstErr(err1, err2, cmd.Start()); err != nil {
			e := fmt.Sprintf("%s could not start: %v", phase, err)
			ctx.Log(logs.System, "✖ "+e)
			return Result{Error: e}
		}
		if ctx.Track != nil {
			ctx.Track(cmd.Process.Pid)
		}
		var timedOut bool
		var mu sync.Mutex
		timer := time.AfterFunc(time.Duration(hook.Timeout)*time.Millisecond, func() {
			mu.Lock()
			timedOut = true
			mu.Unlock()
			platform.KillTree(cmd.Process.Pid, syscall.SIGKILL)
		})
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); logs.PipeLines(stdout, func(l string) { ctx.Log(logs.Stdout, l) }) }()
		go func() { defer wg.Done(); logs.PipeLines(stderr, func(l string) { ctx.Log(logs.Stderr, l) }) }()
		wg.Wait()
		waitErr := cmd.Wait()
		timer.Stop()
		if ctx.Track != nil {
			ctx.Track(0)
		}

		code := 0
		if waitErr != nil {
			code = 1
			if ee, ok := waitErr.(*exec.ExitError); ok {
				code = ee.ExitCode()
			}
		}
		mu.Lock()
		to := timedOut
		mu.Unlock()
		if code != 0 || to {
			why := fmt.Sprintf("failed (exit %d)", code)
			if to {
				why = fmt.Sprintf("timed out after %ds", int(math.Round(float64(hook.Timeout)/1000)))
			}
			e := fmt.Sprintf("%s %s: %s", phase, why, hook.Cmd)
			ctx.Log(logs.System, "✖ "+e)
			return Result{Error: e}
		}
		ctx.Log(logs.System, fmt.Sprintf("✓ %s in %.1fs", phase, time.Since(started).Seconds()))
	}
	return Result{OK: true}
}

func firstErr(errs ...error) error {
	for _, e := range errs {
		if e != nil {
			return e
		}
	}
	return nil
}
