//go:build windows

package platform

import (
	"os/exec"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/sys/windows"
)

const stillActive = 259

// PidAlive reports whether a process with this pid is running.
func PidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		// access denied means it exists but is not ours
		return err == windows.ERROR_ACCESS_DENIED
	}
	defer windows.CloseHandle(h)
	var code uint32
	if windows.GetExitCodeProcess(h, &code) != nil {
		return false
	}
	return code == stillActive
}

// KillTree ends a process and everything it spawned. Windows has no signals nor groups: `taskkill /T` walks the
// tree, and only SIGKILL forces (`/F`).
func KillTree(pid int, sig syscall.Signal) bool {
	taskkill := func(force bool) bool {
		args := []string{"/T"}
		if force {
			args = append(args, "/F")
		}
		args = append(args, "/PID", strconv.Itoa(pid))
		return exec.Command("taskkill", args...).Run() == nil
	}
	// a console program without a window cannot be asked to close ("can only be terminated forcefully"): do not wait for it
	if sig == syscall.SIGKILL {
		return taskkill(true)
	}
	return taskkill(false) || taskkill(true)
}

// TreeAlive cannot see orphans on Windows, so it is "the leader is alive".
func TreeAlive(pid int) bool { return PidAlive(pid) }

// SetDetached gives the command its own process group and no console window.
func SetDetached(c *exec.Cmd) {
	if c.SysProcAttr == nil {
		c.SysProcAttr = &syscall.SysProcAttr{}
	}
	c.SysProcAttr.CreationFlags |= windows.CREATE_NEW_PROCESS_GROUP
	c.SysProcAttr.HideWindow = true
}

// SetVerbatimArgs makes the command line exactly argv joined by spaces, without Go's quoting (see ShellCommand).
func SetVerbatimArgs(c *exec.Cmd, argv []string) {
	if c.SysProcAttr == nil {
		c.SysProcAttr = &syscall.SysProcAttr{}
	}
	c.SysProcAttr.CmdLine = strings.Join(argv, " ")
}
