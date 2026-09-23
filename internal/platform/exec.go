package platform

import (
	"bytes"
	"context"
	"os/exec"
	"time"
)

// Run executes argv (never a shell string) with a timeout and returns trimmed stdout.
func Run(ctx context.Context, timeout time.Duration, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	var out bytes.Buffer
	cmd.Stdout = &out
	err := cmd.Run()
	return string(bytes.TrimSpace(out.Bytes())), err
}
