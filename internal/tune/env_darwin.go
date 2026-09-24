//go:build darwin

package tune

import (
	"context"
	"os"
)

// NewRunner returns the macOS change applier.
func NewRunner() Runner { return DarwinRunner{} }

// ReadEnv assembles the planning environment from live system state.
func ReadEnv(ctx context.Context, memTotalBytes, metalWorkingSetBytes int64, ollamaRunning bool) Env {
	e := Env{MemTotalBytes: memTotalBytes, MetalWorkingSetBytes: metalWorkingSetBytes, OllamaRunning: ollamaRunning}
	if mb, err := sysctlWiredMB(ctx); err == nil {
		e.WiredLimitMB = mb
	}
	if _, err := os.Stat("/Applications/Ollama.app"); err == nil {
		e.OllamaApp = true
	}
	e.OllamaFlashAttention, e.OllamaKVCache = ReadEnvVars(ctx)
	e.PersistDaemon = DaemonInstalled()
	e.OllamaAgent = DefaultAgent().Installed()
	return e
}
