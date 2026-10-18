//go:build !windows

package console

import (
	"os"
	"os/exec"
	"sync"
	"syscall"

	"github.com/creack/pty"

	"github.com/JGoterris/orbit/internal/platform"
)

type unixPTY struct {
	f   *os.File
	cmd *exec.Cmd
	// mu keeps Resize (which needs the descriptor itself, and File.Fd is not safe against a concurrent Close) and
	// Close apart; Read and Write go through the file's own locking
	mu     sync.Mutex
	closed bool
}

func ptySupported() bool { return true }

// startPTY runs argv on a new pseudo-terminal that becomes its controlling terminal (job control, ctrl+c and ctrl+z
// work in an interactive shell).
func startPTY(argv []string, dir string, env []string, cols, rows int) (ptyProcess, error) {
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = dir
	cmd.Env = env
	f, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
	if err != nil {
		return nil, err
	}
	return &unixPTY{f: f, cmd: cmd}, nil
}

func (p *unixPTY) Read(b []byte) (int, error)  { return p.f.Read(b) }
func (p *unixPTY) Write(b []byte) (int, error) { return p.f.Write(b) }
func (p *unixPTY) Pid() int                    { return p.cmd.Process.Pid }
func (p *unixPTY) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed = true
	return p.f.Close()
}

func (p *unixPTY) Resize(cols, rows int) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil
	}
	return pty.Setsize(p.f, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
}

func (p *unixPTY) Wait() int {
	err := p.cmd.Wait()
	if err == nil {
		return 0
	}
	if ee, ok := err.(*exec.ExitError); ok {
		if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			return 128 + int(ws.Signal())
		}
		return ee.ExitCode()
	}
	return 1
}

func (p *unixPTY) Signal(sig syscall.Signal) {
	if !platform.KillTree(p.Pid(), sig) {
		p.cmd.Process.Signal(sig)
	}
}
