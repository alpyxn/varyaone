package opctl

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

var errLockBusy = errors.New("opctl: lock held")

// acquireLockFile takes an exclusive, non-blocking lock. Like flock on Unix,
// Windows releases a LockFileEx lock when the owning handle is closed, which
// includes the process dying, so a killed operation does not strand the lock.
func acquireLockFile(path string) (*os.File, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	overlapped := new(windows.Overlapped)
	err = windows.LockFileEx(
		windows.Handle(file.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0, 1, 0, overlapped)
	if err != nil {
		_ = file.Close()
		if errors.Is(err, windows.ERROR_LOCK_VIOLATION) || errors.Is(err, windows.ERROR_IO_PENDING) {
			return nil, errLockBusy
		}
		return nil, err
	}
	return file, nil
}

func releaseLockFile(file *os.File) error {
	if file == nil {
		return nil
	}
	overlapped := new(windows.Overlapped)
	_ = windows.UnlockFileEx(windows.Handle(file.Fd()), 0, 1, 0, overlapped)
	return file.Close()
}

// syncDir is a no-op on Windows: a directory handle cannot be flushed the way
// Unix flushes one, and NTFS orders the metadata write behind the data.
func syncDir(string) error { return nil }
