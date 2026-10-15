//go:build windows

package ipc

import (
	"net"
	"time"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

func listen(path string) (net.Listener, error) {
	sd, err := ownerOnly()
	if err != nil {
		return nil, err
	}
	return winio.ListenPipe(path, &winio.PipeConfig{SecurityDescriptor: sd})
}

// ownerOnly is a security descriptor that lets only the current user open the pipe: the default one lets every
// user on the machine read from it.
func ownerOnly() (string, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return "", err
	}
	return "D:P(A;;GA;;;" + user.User.Sid.String() + ")", nil
}

func dial(path string, timeout time.Duration) (net.Conn, error) {
	return winio.DialPipe(path, &timeout)
}

// Named pipes need no socket directory nor start lock: a pipe name cannot be taken twice.
func checkSocketDir(string) error { return nil }

func lockSocket(string) (func(), error) { return func() {}, nil }
