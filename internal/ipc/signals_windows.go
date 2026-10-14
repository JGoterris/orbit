//go:build windows

package ipc

import "os"

func syscallHUP() []os.Signal { return nil }
