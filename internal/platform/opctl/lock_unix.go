//go:build !windows

package opctl

import (
	"errors"
	"os"
	"syscall"
)

var errLockBusy = errors.New("opctl: lock held")

// acquireLockFile takes an exclusive, non-blocking advisory lock.
//
// The lock lives in the open file description, so the kernel drops it when the
// process exits for any reason — including SIGKILL, an OOM kill or a container
// stop. That is the whole reason this is not a PID file: a PID file survives
// its owner, cannot distinguish a dead process from one owned by another user,
// and is wrong again as soon as the PID is reused.
func acquireLockFile(path string) (*os.File, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = file.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
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
	// Closing the descriptor releases the lock; unlocking first keeps the
	// window between the two from being observable.
	_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
	return file.Close()
}

func syncDir(directory string) error {
	handle, err := os.Open(directory)
	if err != nil {
		return err
	}
	syncErr := handle.Sync()
	closeErr := handle.Close()
	if syncErr != nil {
		return syncErr
	}
	return closeErr
}
