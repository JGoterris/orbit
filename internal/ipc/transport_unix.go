//go:build !windows

package ipc

import (
	"net"
	"time"
)

func listen(path string) (net.Listener, error) { return net.Listen("unix", path) }

func dial(path string, timeout time.Duration) (net.Conn, error) {
	return net.DialTimeout("unix", path, timeout)
}
