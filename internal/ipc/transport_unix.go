//go:build !windows

package ipc

import (
	"fmt"
	"net"
	"os"
	"syscall"
	"time"

	"github.com/JGoterris/orbit/internal/platform"
)

func listen(path string) (net.Listener, error) { return net.Listen("unix", path) }

func dial(path string, timeout time.Duration) (net.Conn, error) {
	return net.DialTimeout("unix", path, timeout)
}

// checkSocketDir refuses a socket directory another user could tamper with: one that is a symlink, belongs to
// someone else, or others can write to. The short-path fallback lives in a shared temp dir, where another user
// could have created it first.
func checkSocketDir(dir string) error {
	st, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !st.IsDir() {
		return fmt.Errorf("socket directory %s is not a directory", dir)
	}
	if sys, ok := st.Sys().(*syscall.Stat_t); ok && int(sys.Uid) != os.Getuid() {
		return fmt.Errorf("socket directory %s belongs to another user", dir)
	}
	if st.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("socket directory %s is writable by other users", dir)
	}
	return nil
}

// lockSocket serializes starting a server on path across processes.
func lockSocket(path string) (unlock func(), err error) {
	f, err := os.OpenFile(path+".lock", os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	if err := platform.LockFile(f); err != nil {
		f.Close()
		return nil, err
	}
	return func() {
		platform.UnlockFile(f)
		f.Close()
	}, nil
}
