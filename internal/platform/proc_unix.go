//go:build !windows

package platform

import (
	"errors"
	"os/exec"
	"syscall"
)

// PidAlive reports whether a process with this pid exists (even if it belongs to someone else).
func PidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// KillTree signals a process and everything it spawned: the whole process group (the process was started with
// SetDetached, so its group id is its pid), or just the process if it has none.
func KillTree(pid int, sig syscall.Signal) bool {
	if pid <= 0 {
		return false
	}
	if syscall.Kill(-pid, sig) == nil {
		return true
	}
	return syscall.Kill(pid, sig) == nil
}

// TreeAlive reports whether any process of the group is still there.
func TreeAlive(pid int) bool { return pid > 0 && syscall.Kill(-pid, 0) == nil }

// SetDetached makes the command lead a new session and process group, so KillTree can reach its descendants and it
// has no controlling terminal (ssh/gpg fail instead of prompting over the TUI).
func SetDetached(c *exec.Cmd) {
	if c.SysProcAttr == nil {
		c.SysProcAttr = &syscall.SysProcAttr{}
	}
	c.SysProcAttr.Setsid = true
}

// SetVerbatimArgs is only meaningful on Windows.
func SetVerbatimArgs(c *exec.Cmd, argv []string) {}
