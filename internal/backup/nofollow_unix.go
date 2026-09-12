//go:build !windows

package backup

import "syscall"

// openNoFollowFlag makes an open fail rather than traverse a final symlink.
const openNoFollowFlag = syscall.O_NOFOLLOW
