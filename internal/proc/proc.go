// Package proc runs external commands and probes ports.
package proc

import (
	"bytes"
	"context"
	"net"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/JGoterris/orbit/internal/platform"
)

// Result is the outcome of a command that ran to completion.
type Result struct {
	Code   int
	Stdout string
	Stderr string
}

// Options tune Exec; the zero value runs with orbit's own environment and a closed stdin.
type Options struct {
	Dir string
	// Env replaces the environment when non-nil (KEY=value entries)
	Env []string
	// Timeout kills the command when it takes longer; 0 means no limit
	Timeout time.Duration
	// Input is written to the command's stdin; nil closes it
	Input *string
	// Detached runs it in a new session, without a controlling terminal: ssh/gpg cannot prompt over the TUI, they fail instead
	Detached bool
	// Verbatim (Windows): do not quote or escape the arguments (see platform.ShellCommand)
	Verbatim bool
}

// Exec runs a command to completion and captures its output. It never fails for a non-zero exit: a command that
// cannot even start gets code 127 and the reason in Stderr.
func Exec(argv []string, opts Options) Result {
	return ExecContext(context.Background(), argv, opts)
}

func ExecContext(ctx context.Context, argv []string, opts Options) Result {
	if len(argv) == 0 {
		return Result{Code: 127, Stderr: "empty command"}
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = opts.Dir
	cmd.Env = opts.Env
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if opts.Input != nil {
		cmd.Stdin = strings.NewReader(*opts.Input)
	}
	if opts.Detached {
		platform.SetDetached(cmd)
	}
	if opts.Verbatim {
		platform.SetVerbatimArgs(cmd, argv)
	}
	if opts.Timeout > 0 {
		// a grandchild that outlives the command (the `sleep` of `sh -c "sleep 30"`) keeps the pipes open: do not wait for it
		cmd.WaitDelay = 2 * time.Second
	}
	if err := cmd.Start(); err != nil {
		return Result{Code: 127, Stderr: err.Error()}
	}
	kill := func() {
		if opts.Detached && platform.KillTree(cmd.Process.Pid, syscall.SIGKILL) {
			return
		}
		cmd.Process.Kill()
	}
	done := make(chan struct{})
	defer close(done)
	var timeout <-chan time.Time
	if opts.Timeout > 0 {
		t := time.NewTimer(opts.Timeout)
		defer t.Stop()
		timeout = t.C
	}
	go func() {
		select {
		case <-done:
		case <-timeout:
			kill()
		case <-ctx.Done():
			kill()
		}
	}()
	err := cmd.Wait()
	code := 0
	switch {
	case cmd.ProcessState != nil:
		code = cmd.ProcessState.ExitCode()
		if code == -1 { // killed by a signal
			code = 128
			if ws, ok := cmd.ProcessState.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
				code = 128 + int(ws.Signal())
			}
		}
	case err != nil:
		code = 127
		stderr.WriteString(err.Error())
	}
	return Result{Code: code, Stdout: stdout.String(), Stderr: stderr.String()}
}

// IsPortOpen reports whether something accepts connections on host:port.
func IsPortOpen(port int, host string, timeout time.Duration) bool {
	if host == "" {
		host = "127.0.0.1"
	}
	if timeout <= 0 {
		timeout = 800 * time.Millisecond
	}
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, strconv.Itoa(port)), timeout)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// OpenURL opens a URL in the host browser.
func OpenURL(url string) bool {
	winOpen := []string{"cmd.exe", "/c", "start", "", strings.ReplaceAll(url, "&", "^&")}
	var candidates [][]string
	switch runtime.GOOS {
	case "darwin":
		candidates = [][]string{{"open", url}}
	case "windows":
		candidates = [][]string{winOpen}
	default:
		candidates = [][]string{{"xdg-open", url}, {"wslview", url}, winOpen}
	}
	for _, argv := range candidates {
		if _, err := exec.LookPath(argv[0]); err != nil {
			continue
		}
		if Exec(argv, Options{Timeout: 5 * time.Second}).Code == 0 {
			return true
		}
	}
	return false
}

// Which is the path of a program on PATH, or "".
func Which(name string) string {
	p, err := exec.LookPath(name)
	if err != nil {
		return ""
	}
	return p
}

// EnvList turns a map into KEY=value entries on top of orbit's own environment (extra wins).
func EnvList(extra map[string]string) []string {
	merged := map[string]string{}
	for _, kv := range os.Environ() {
		if k, v, ok := strings.Cut(kv, "="); ok {
			merged[k] = v
		}
	}
	for k, v := range extra {
		merged[k] = v
	}
	out := make([]string, 0, len(merged))
	for k, v := range merged {
		out = append(out, k+"="+v)
	}
	return out
}
