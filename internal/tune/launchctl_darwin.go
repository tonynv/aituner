//go:build darwin

package tune

import (
	"context"
	"os"
	"os/exec"
	"strconv"
)

// guiLaunchctl runs a launchctl environment command (setenv, unsetenv, getenv) in the user's GUI launchd domain, where
// apps such as Ollama.app read their environment. Plain `launchctl setenv/getenv` act on the caller's own session,
// which is the Background session when aituner runs inside tmux or over SSH, so the change would never reach the app.
// `asuser` with one's own uid needs no privileges.
func guiLaunchctl(ctx context.Context, args ...string) *exec.Cmd {
	return exec.CommandContext(ctx, "/bin/launchctl", append([]string{"asuser", strconv.Itoa(os.Getuid()), "/bin/launchctl"}, args...)...)
}
