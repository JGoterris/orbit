package runner

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/JGoterris/orbit/internal/config"
	"github.com/JGoterris/orbit/internal/core/logs"
	"github.com/JGoterris/orbit/internal/platform"
	"github.com/JGoterris/orbit/internal/proc"
	"github.com/JGoterris/orbit/internal/state"
)

// wrapper runs the command and leaves its exit code in a file, so a later orbit session can tell how it ended
// (POSIX; Windows uses `orbit __wrap`, see RunWrap).
const wrapper = `sh -c "$1"; c=$?; echo $c > "$2"; exit $c`

// WrapCommand is the hidden subcommand the Windows wrapper runs as.
const WrapCommand = "__wrap"

var backlog = &logs.Backlog{Bytes: 64 * 1024, Lines: 200}

// ProcessRunner: output goes to files instead of pipes, and the process lives in its own group, so it can outlive
// orbit: a new session re-attaches with Attach (pid + start time), re-reads the logs and watches it from there.
type ProcessRunner struct {
	svc   *config.ServiceConfig
	cb    Callbacks
	files state.ProcFiles

	mu        sync.Mutex
	cmd       *exec.Cmd
	exited    chan struct{} // closed when the process we started ends
	pid       int
	startTime int64
	tails     []*logs.FileTail
	watcher   chan struct{} // closes the adopted-process poller
	released  bool
	verified  bool
	draining  sync.Mutex
}

func newProcess(svc *config.ServiceConfig, cb Callbacks, files state.ProcFiles) *ProcessRunner {
	return &ProcessRunner{svc: svc, cb: cb, files: files}
}

func (r *ProcessRunner) Pid() int            { r.mu.Lock(); defer r.mu.Unlock(); return r.pid }
func (r *ProcessRunner) StartTime() int64    { r.mu.Lock(); defer r.mu.Unlock(); return r.startTime }
func (r *ProcessRunner) ContainerID() string { return "" }

func (r *ProcessRunner) argv() ([]string, error) {
	if platform.IsWindows {
		exe, err := os.Executable()
		if err != nil {
			return nil, err
		}
		argv := []string{exe, WrapCommand, r.files.Exit, r.svc.Cmd}
		if r.svc.Shell != "" {
			argv = append(argv, r.svc.Shell)
		}
		return argv, nil
	}
	if r.svc.Shell != "" {
		return append(platform.ShellArgv(wrapper, r.svc.Shell, false), "orbit", r.svc.Cmd, r.files.Exit), nil
	}
	return []string{"/bin/sh", "-c", wrapper, "orbit", r.svc.Cmd, r.files.Exit}, nil
}

func (r *ProcessRunner) Start() error {
	if st, err := os.Stat(r.svc.Cwd); err != nil || !st.IsDir() {
		return fmt.Errorf("working directory does not exist: %s", r.svc.Cwd)
	}
	r.mu.Lock()
	r.released = false
	r.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(r.files.Out), 0o755); err != nil {
		return err
	}
	os.Remove(r.files.Exit)
	out, err := os.OpenFile(r.files.Out, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer out.Close()
	errf, err := os.OpenFile(r.files.Err, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer errf.Close()

	argv, err := r.argv()
	if err != nil {
		return err
	}
	extra := map[string]string{"FORCE_COLOR": "1"}
	for k, v := range r.svc.Env {
		extra[k] = v
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = r.svc.Cwd
	cmd.Env = proc.EnvList(extra)
	cmd.Stdout, cmd.Stderr = out, errf
	// own process group, so we can signal the whole tree (npm -> node -> esbuild...)
	platform.SetDetached(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}

	r.mu.Lock()
	r.cmd = cmd
	r.exited = make(chan struct{})
	r.pid = cmd.Process.Pid
	r.startTime, _ = platform.ProcStartTime(r.pid)
	r.verified = true
	exited := r.exited
	pid := r.pid
	r.mu.Unlock()
	r.follow(nil)

	go func() {
		err := cmd.Wait()
		close(exited)
		// leftovers of the group (e.g. a dev server's workers) must not keep ports busy
		if platform.TreeAlive(pid) {
			platform.KillTree(pid, syscall.SIGTERM)
		}
		r.mu.Lock()
		skip := r.cmd != cmd || r.released
		r.mu.Unlock()
		if skip {
			return
		}
		r.stopTails()
		r.mu.Lock()
		skip = r.cmd != cmd || r.released
		r.mu.Unlock()
		if skip {
			return
		}
		code, signal := exitInfo(cmd, err)
		r.cb.Exit(code, signal)
	}()
	return nil
}

// exitInfo is the exit code of a finished command (nil when a signal ended it) and that signal's name.
func exitInfo(cmd *exec.Cmd, err error) (*int, string) {
	if cmd.ProcessState == nil {
		return nil, ""
	}
	if ws, ok := cmd.ProcessState.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return nil, signalName(ws.Signal())
	}
	c := cmd.ProcessState.ExitCode()
	return &c, ""
}

func signalName(s syscall.Signal) string {
	switch s {
	case syscall.SIGKILL:
		return "SIGKILL"
	case syscall.SIGTERM:
		return "SIGTERM"
	case syscall.SIGHUP:
		return "SIGHUP"
	case syscall.SIGINT:
		return "SIGINT"
	}
	return "signal " + strconv.Itoa(int(s))
}

func (r *ProcessRunner) Attach(saved *state.SavedService) (bool, error) {
	if saved == nil || saved.Pid == 0 || !r.isSame(saved.Pid, saved.StartTime) {
		return false, nil
	}
	r.mu.Lock()
	r.pid, r.startTime, r.released = saved.Pid, saved.StartTime, false
	r.watcher = make(chan struct{})
	watcher := r.watcher
	r.mu.Unlock()
	r.follow(backlog)
	go func() {
		t := time.NewTicker(time.Second)
		defer t.Stop()
		for {
			select {
			case <-watcher:
				return
			case <-t.C:
				if r.checkAdopted() {
					return
				}
			}
		}
	}()
	return true, nil
}

func (r *ProcessRunner) follow(b *logs.Backlog) {
	tails := []*logs.FileTail{
		logs.NewFileTail(r.files.Out, func(l string) { r.cb.Log(logs.Stdout, l) }),
		logs.NewFileTail(r.files.Err, func(l string) { r.cb.Log(logs.Stderr, l) }),
	}
	for _, t := range tails {
		t.Start(b)
	}
	r.mu.Lock()
	r.tails = tails
	r.mu.Unlock()
}

func (r *ProcessRunner) stopTails() {
	r.mu.Lock()
	tails := r.tails
	r.tails = nil
	r.mu.Unlock()
	r.draining.Lock()
	defer r.draining.Unlock()
	for _, t := range tails {
		t.Stop(true)
	}
}

// isSame: is pid still the process we launched (not a recycled pid)?
func (r *ProcessRunner) isSame(pid int, startTime int64) bool {
	r.mu.Lock()
	verified := r.verified
	r.mu.Unlock()
	// where reading the start time spawns a process, only the first check (attach) pays for it; polling just asks the pid
	if !platform.CheapStartTime && verified {
		return platform.PidAlive(pid)
	}
	if now, ok := platform.ProcStartTime(pid); ok {
		same := startTime == 0 || now == startTime
		r.mu.Lock()
		r.verified = same
		r.mu.Unlock()
		return same
	}
	// the start time is unavailable (the process is gone, or the platform cannot say): fall back to "the tree exists"
	return !platform.CheapStartTime && platform.TreeAlive(pid)
}

func (r *ProcessRunner) leaderAlive() bool {
	r.mu.Lock()
	pid, start := r.pid, r.startTime
	r.mu.Unlock()
	return pid != 0 && r.isSame(pid, start)
}

// checkAdopted reports true when the adopted process is gone (and the exit was reported).
func (r *ProcessRunner) checkAdopted() bool {
	r.mu.Lock()
	released := r.released
	r.mu.Unlock()
	if released {
		return true
	}
	if r.leaderAlive() {
		return false
	}
	r.mu.Lock()
	pid := r.pid
	r.mu.Unlock()
	if platform.TreeAlive(pid) {
		platform.KillTree(pid, syscall.SIGTERM)
	}
	r.stopTails()
	r.mu.Lock()
	released = r.released
	r.mu.Unlock()
	if released {
		return true
	}
	var code *int
	if data, err := os.ReadFile(r.files.Exit); err == nil {
		if n, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil {
			code = &n
		}
	}
	r.cb.Exit(code, "")
	return true
}

func (r *ProcessRunner) Release() {
	r.mu.Lock()
	r.released = true
	if r.watcher != nil {
		close(r.watcher)
		r.watcher = nil
	}
	tails := r.tails
	r.tails = nil
	r.mu.Unlock()
	for _, t := range tails {
		go t.Stop(false)
	}
}

func (r *ProcessRunner) KillSync() {
	r.mu.Lock()
	released, pid := r.released, r.pid
	r.mu.Unlock()
	if !released && pid != 0 && r.leaderAlive() {
		platform.KillTree(pid, syscall.SIGKILL)
	}
}

// waitLeader waits for the leader to be gone; false when ms elapsed first.
func (r *ProcessRunner) waitLeader(ms time.Duration) bool {
	r.mu.Lock()
	exited := r.exited
	hasCmd := r.cmd != nil
	r.mu.Unlock()
	if hasCmd {
		select {
		case <-exited:
			return true
		case <-time.After(ms):
			return false
		}
	}
	end := time.Now().Add(ms)
	for r.leaderAlive() {
		if time.Now().After(end) {
			return false
		}
		time.Sleep(25 * time.Millisecond)
	}
	return true
}

func (r *ProcessRunner) Stop(timeoutMs int64) error {
	r.mu.Lock()
	pid, released, exited := r.pid, r.released, r.exited
	hasCmd := r.cmd != nil
	r.mu.Unlock()
	if pid == 0 || released {
		return nil
	}
	running := false
	if hasCmd {
		select {
		case <-exited:
		default:
			running = true
		}
	} else {
		running = r.leaderAlive()
	}
	if !running {
		return nil
	}
	r.mu.Lock()
	if r.watcher != nil {
		close(r.watcher)
		r.watcher = nil
	}
	r.mu.Unlock()

	platform.KillTree(pid, syscall.SIGTERM)
	if !r.waitLeader(time.Duration(timeoutMs) * time.Millisecond) {
		r.cb.Log(logs.System, fmt.Sprintf("did not stop after %dms, sending SIGKILL", timeoutMs))
		platform.KillTree(pid, syscall.SIGKILL)
		r.waitLeader(5 * time.Second)
	}
	if platform.TreeAlive(pid) {
		// the main process exited but left group members behind (e.g. background jobs): the leader is already
		// gone, so wait for the group itself to disappear
		r.cb.Log(logs.System, "killing leftover child processes")
		platform.KillTree(pid, syscall.SIGKILL)
		for i := 0; i < 50 && platform.TreeAlive(pid); i++ {
			time.Sleep(10 * time.Millisecond)
		}
	}
	r.stopTails()
	return nil
}

// RunWrap is the Windows replacement for the `sh -c '...; echo $? > exitfile'` wrapper: it runs the command through
// the shell (stdio inherited, so the output goes to the service's log files) and leaves its exit code in a file.
// It builds the shell command line itself because only here the quoting can be kept verbatim.
// args: <exit file> <command> [shell]. It returns the exit code of orbit.
func RunWrap(args []string) int {
	if len(args) < 2 {
		return 2
	}
	exitFile, cmdline := args[0], args[1]
	shell := ""
	if len(args) > 2 {
		shell = args[2]
	}
	argv, verbatim := platform.ShellCommand(cmdline, shell, platform.IsWindows)
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if verbatim {
		platform.SetVerbatimArgs(cmd, argv)
	}
	code := 0
	if err := cmd.Run(); err != nil {
		code = 1
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		}
	}
	os.WriteFile(exitFile, []byte(strconv.Itoa(code)), 0o644)
	return code
}
