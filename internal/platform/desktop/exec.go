package desktop

import (
	"context"
	"fmt"
	"os"
	"os/exec"
)

// runCommand runs an external command, inheriting stdout/stderr, with the
// process environment plus any extraEnv ("KEY=value") entries appended.
func runCommand(ctx context.Context, dir string, extraEnv []string, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = append(os.Environ(), extraEnv...)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s %v: %w", name, args, err)
	}
	return nil
}
