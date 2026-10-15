//go:build windows

package platform

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// lockOffset: the lock covers one byte far past the end of the file. Windows locks are mandatory, so locking the
// contents would stop other processes from reading them (the pid in orbit.lock).
const lockOffset = 1 << 30

func lockFile(f *os.File, flags uint32) error {
	return windows.LockFileEx(windows.Handle(f.Fd()), flags, 0, 1, 0, &windows.Overlapped{Offset: lockOffset})
}

// TryLockFile takes an exclusive lock on f without waiting. It returns false when another open file (in this or
// another process) holds it. Windows drops the lock when the holder dies, so a lock is never left stale.
func TryLockFile(f *os.File) (bool, error) {
	err := lockFile(f, windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY)
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return false, nil
	}
	return err == nil, err
}

// LockFile is TryLockFile that waits for the lock.
func LockFile(f *os.File) error { return lockFile(f, windows.LOCKFILE_EXCLUSIVE_LOCK) }

// UnlockFile releases a lock taken by TryLockFile or LockFile.
func UnlockFile(f *os.File) error {
	return windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, &windows.Overlapped{Offset: lockOffset})
}
