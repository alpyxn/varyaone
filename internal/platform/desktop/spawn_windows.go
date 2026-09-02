//go:build windows

package desktop

import (
	"os/exec"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/windows"
)

// spawnDetachedUpdater starts `varyaone update-apply [--target v]` as a process
// that is not a child of this one in any way that matters: it gets its own
// process group, no console, and does not inherit our handles. The service the
// updater is about to stop (this process) can exit without taking it down.
func spawnDetachedUpdater(installDir, target string) error {
	args := []string{"update-apply"}
	if target != "" {
		args = append(args, "--target", target)
	}
	cmd := exec.Command(filepath.Join(installDir, "varyaone.exe"), args...)
	cmd.Dir = installDir
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, nil, nil
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: windows.DETACHED_PROCESS | windows.CREATE_NEW_PROCESS_GROUP,
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}
