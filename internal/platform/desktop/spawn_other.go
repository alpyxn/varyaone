//go:build !windows

package desktop

import (
	"os/exec"
	"path/filepath"
	"syscall"
)

// spawnDetachedUpdater starts `varyaone update-apply` in its own session so it
// survives this process exiting. On Linux the systemd update agent normally
// owns this; the in-process path exists so `varyaone stack` works standalone.
func spawnDetachedUpdater(installDir, target string) error {
	args := []string{"update-apply"}
	if target != "" {
		args = append(args, "--target", target)
	}
	cmd := exec.Command(filepath.Join(installDir, "varyaone"), args...)
	cmd.Dir = installDir
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}
