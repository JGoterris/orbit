package cli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"

	"github.com/JGoterris/orbit/internal/config"
	"github.com/JGoterris/orbit/internal/core/tasks"
	"github.com/JGoterris/orbit/internal/ipc"
	"github.com/JGoterris/orbit/internal/platform"
	"github.com/JGoterris/orbit/internal/proc"
)

// RunTask is `orbit run [task] [-- args]`: the task runs in the foreground on this terminal and its exit code is
// ours. Without a task name it lists them.
func RunTask(cfg *config.OrbitConfig, name string, args []string) int {
	if name == "" {
		return listTasks(cfg)
	}
	task := cfg.Tasks[name]
	if task == nil {
		msg := fmt.Sprintf("unknown task %q", name)
		if s := config.Suggest(name, cfg.TaskOrder); s != "" {
			msg += fmt.Sprintf(" (did you mean %q?)", s)
		} else if len(cfg.TaskOrder) > 0 {
			msg += " (tasks: " + strings.Join(cfg.TaskOrder, ", ") + ")"
		}
		fmt.Fprintln(os.Stderr, red(msg))
		return 1
	}
	if len(task.DependsOn) > 0 {
		if code := startTaskDeps(cfg, task); code != 0 {
			return code
		}
	}
	spec, err := tasks.Command(task, args)
	if err != nil {
		fmt.Fprintln(os.Stderr, red(err.Error()))
		return 1
	}
	cmd := exec.Command(spec.Argv[0], spec.Argv[1:]...)
	cmd.Dir = spec.Cwd
	cmd.Env = proc.EnvList(spec.Env)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if spec.Verbatim {
		platform.SetVerbatimArgs(cmd, spec.Argv)
	}
	restoreTerminal := prepareTask(cmd)
	defer restoreTerminal()
	if err := cmd.Start(); err != nil {
		fmt.Fprintln(os.Stderr, red(fmt.Sprintf("task %s could not start: %v", name, err)))
		return 1
	}
	// orbit waits for the task instead of dying first. What reaches orbit alone (a kill, a closed terminal) is passed
	// on to everything the task started, or it would be left running on its own.
	sigs := make(chan os.Signal, 4)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(sigs)
	go func() {
		for s := range sigs {
			if sig, ok := s.(syscall.Signal); ok && (s != os.Interrupt || forwardInterrupt) {
				platform.KillTree(cmd.Process.Pid, sig)
			}
		}
	}()
	err = cmd.Wait()
	restoreTerminal()
	var ee *exec.ExitError
	switch {
	case err == nil:
		return 0
	case errors.As(err, &ee):
		if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			return 128 + int(ws.Signal())
		}
		if ee.ExitCode() > 0 {
			return ee.ExitCode()
		}
	}
	return 1
}

// startTaskDeps asks the running orbit of the project to start the services the task needs.
func startTaskDeps(cfg *config.OrbitConfig, task *config.TaskConfig) int {
	deps := strings.Join(task.DependsOn, " ")
	client := connectIPC(cfg)
	if client == nil {
		fmt.Fprintln(os.Stderr, red(fmt.Sprintf("%s needs %s: start %s with `orbit up %s` (or open orbit) and run it again",
			task.Name, strings.Join(task.DependsOn, ", "), pluralWord(len(task.DependsOn), "it", "them"), deps)))
		return 1
	}
	defer client.Close()
	var res struct {
		OK bool `json:"ok"`
	}
	fmt.Println(dim("» starting " + strings.Join(task.DependsOn, ", ")))
	if err := client.Call(ipc.MethodStart, map[string]any{"services": task.DependsOn}, &res); err != nil {
		fmt.Fprintln(os.Stderr, red(err.Error()))
		return 1
	}
	if !res.OK {
		fmt.Fprintln(os.Stderr, red(task.Name+": some of its services did not become ready (see `orbit logs`)"))
		return 1
	}
	return 0
}

func pluralWord(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func listTasks(cfg *config.OrbitConfig) int {
	if len(cfg.TaskOrder) == 0 {
		fmt.Println(dim("no tasks: add a `tasks:` section to orbit.yaml"))
		return 0
	}
	width := 0
	for _, n := range cfg.TaskOrder {
		width = max(width, len([]rune(n)))
	}
	for _, n := range cfg.TaskOrder {
		t := cfg.Tasks[n]
		what := t.Description
		if what == "" {
			what = t.Cmd
		}
		fmt.Printf("%s  %s\n", bold(padEnd(n, width)), what)
	}
	return 0
}
