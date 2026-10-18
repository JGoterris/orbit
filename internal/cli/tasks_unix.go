//go:build !windows

package cli

import (
	"os"
	"os/exec"
	"os/signal"
	"syscall"

	"golang.org/x/sys/unix"
)

// forwardInterrupt: the task is in its own process group, so a SIGINT that reaches orbit alone (a kill, or a
// terminal whose foreground group is ours) is passed on to it.
const forwardInterrupt = true

// prepareTask puts the task in its own process group, so that a signal can reach everything it started. When stdin
// is a terminal it also becomes the foreground group, as in a shell: ctrl+c and the terminal's own hangup go to the
// task and nothing else, and it can read the keyboard. The returned function gives the terminal back.
func prepareTask(cmd *exec.Cmd) (restore func()) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	fd := int(os.Stdin.Fd())
	if _, err := unix.IoctlGetWinsize(fd, unix.TIOCGWINSZ); err != nil {
		return func() {}
	}
	cmd.SysProcAttr.Foreground, cmd.SysProcAttr.Ctty = true, fd
	self := syscall.Getpgrp()
	return func() {
		// taking the terminal back from the background raises SIGTTOU
		signal.Ignore(syscall.SIGTTOU)
		unix.IoctlSetPointerInt(fd, unix.TIOCSPGRP, self)
		signal.Reset(syscall.SIGTTOU)
	}
}
