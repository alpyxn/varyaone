//go:build !windows

package backup

import "golang.org/x/sys/unix"

// freeBytes reports the space available to this process, which on a filesystem
// with reserved blocks is less than the space that is merely unallocated.
func freeBytes(path string) (int64, error) {
	var stat unix.Statfs_t
	if err := unix.Statfs(path, &stat); err != nil {
		return 0, err
	}
	return int64(stat.Bavail) * int64(stat.Bsize), nil
}
