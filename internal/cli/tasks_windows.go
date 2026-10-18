//go:build windows

package cli

import "os/exec"

// forwardInterrupt: ctrl+c already reaches every process attached to the console, the task included.
const forwardInterrupt = false

// prepareTask: Windows has no foreground process groups; platform.KillTree walks the tree of the task instead.
func prepareTask(cmd *exec.Cmd) (restore func()) { return func() {} }
