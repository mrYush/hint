//go:build !windows

package builtin

import (
	"context"
	"fmt"
	"os/exec"
)

const shellDescription = "Use bash syntax (sh is the fallback when bash is unavailable)."
const shellWaitDelayNote = "[a background child process was detached]"

// shellCommand builds the command that runs script through the user's
// shell. bash is preferred for its ubiquity in the commands models write;
// sh is the POSIX fallback on minimal images.
func shellCommand(ctx context.Context, script string) (*exec.Cmd, error) {
	shell, err := exec.LookPath("bash")
	if err != nil {
		if shell, err = exec.LookPath("sh"); err != nil {
			return nil, fmt.Errorf("no shell found: %w", err)
		}
	}
	return exec.CommandContext(ctx, shell, "-c", script), nil
}
