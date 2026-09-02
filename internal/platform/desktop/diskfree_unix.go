//go:build !windows

package desktop

import "syscall"

// freeDiskBytes reports the bytes available to an unprivileged writer at path
// (the filesystem holding it must exist).
func freeDiskBytes(path string) (uint64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, err
	}
	return st.Bavail * uint64(st.Bsize), nil
}
