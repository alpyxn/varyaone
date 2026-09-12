//go:build !windows

package backup

import "os"

// syncDir flushes a directory entry so a rename into it survives a power loss.
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
