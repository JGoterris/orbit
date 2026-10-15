//go:build !windows

package platform

import (
	"errors"
	"os"
	"syscall"
)

// TryLockFile takes an exclusive lock on f without waiting. It returns false when another open file (in this or
// another process) holds it. The kernel drops the lock when the holder dies, so a lock is never left stale.
func TryLockFile(f *os.File) (bool, error) {
	err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if errors.Is(err, syscall.EWOULDBLOCK) {
		return false, nil
	}
	return err == nil, err
}

// LockFile is TryLockFile that waits for the lock.
func LockFile(f *os.File) error {
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX)
		if !errors.Is(err, syscall.EINTR) {
			return err
		}
	}
}

// UnlockFile releases a lock taken by TryLockFile or LockFile.
func UnlockFile(f *os.File) error { return syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }
