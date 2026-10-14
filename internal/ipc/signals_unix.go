//go:build !windows

package ipc

import (
	"os"
	"syscall"
)

func syscallHUP() []os.Signal { return []os.Signal{syscall.SIGHUP} }
