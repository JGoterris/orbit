//go:build windows

package console

import (
	"context"
	"syscall"

	"github.com/UserExistsError/conpty"
	"golang.org/x/sys/windows"

	"github.com/JGoterris/orbit/internal/platform"
)

type winPTY struct{ c *conpty.ConPty }

func ptySupported() bool { return conpty.IsConPtyAvailable() }

func startPTY(argv []string, dir string, env []string, cols, rows int) (ptyProcess, error) {
	c, err := conpty.Start(windows.ComposeCommandLine(argv),
		conpty.ConPtyDimensions(cols, rows), conpty.ConPtyWorkDir(dir), conpty.ConPtyEnv(env))
	if err != nil {
		return nil, err
	}
	return &winPTY{c: c}, nil
}

func (p *winPTY) Read(b []byte) (int, error)  { return p.c.Read(b) }
func (p *winPTY) Write(b []byte) (int, error) { return p.c.Write(b) }
func (p *winPTY) Pid() int                    { return p.c.Pid() }
func (p *winPTY) Close() error                { return p.c.Close() }

func (p *winPTY) Resize(cols, rows int) error { return p.c.Resize(cols, rows) }

func (p *winPTY) Wait() int {
	code, err := p.c.Wait(context.Background())
	if err != nil {
		return 1
	}
	return int(code)
}

func (p *winPTY) Signal(sig syscall.Signal) { platform.KillTree(p.Pid(), sig) }
