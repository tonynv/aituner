//go:build darwin

package tune

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

const (
	daemonLabel = "ai.aituner.wiredlimit"
	daemonPath  = "/Library/LaunchDaemons/" + daemonLabel + ".plist"
)

// DarwinRunner applies changes on macOS. Every apply is read back and verified.
type DarwinRunner struct {
	OllamaRestart func(ctx context.Context) error // injected so the API layer can wait for readiness
	Agent         AgentSpec                       // zero value = DefaultAgent()
}

func (r DarwinRunner) agent() AgentSpec {
	if r.Agent.Label == "" {
		return DefaultAgent()
	}
	return r.Agent
}

func sysctlWiredMB(ctx context.Context) (int64, error) {
	out, err := exec.CommandContext(ctx, "/usr/sbin/sysctl", "-n", "iogpu.wired_limit_mb").Output()
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
}

func launchctlGet(ctx context.Context, key string) string {
	out, _ := exec.CommandContext(ctx, "/bin/launchctl", "getenv", key).Output()
	return strings.TrimSpace(string(out))
}

// ReadEnvVars reads the two Ollama env vars from the user's launchd domain.
func ReadEnvVars(ctx context.Context) (flash, kv string) {
	return launchctlGet(ctx, "OLLAMA_FLASH_ATTENTION"), launchctlGet(ctx, "OLLAMA_KV_CACHE_TYPE")
}

// DaemonInstalled reports whether the persistence LaunchDaemon exists.
func DaemonInstalled() bool {
	_, err := os.Stat(daemonPath)
	return err == nil
}

func (r DarwinRunner) Apply(ctx context.Context, c Change) error {
	switch c.Key {
	case KeyWiredLimit:
		if err := runAdmin(ctx, fmt.Sprintf("/usr/sbin/sysctl iogpu.wired_limit_mb=%d", c.value)); err != nil {
			return err
		}
		got, err := sysctlWiredMB(ctx)
		if err != nil || got != c.value {
			return fmt.Errorf("verification failed: iogpu.wired_limit_mb is %d, expected %d", got, c.value)
		}
		return nil
	case KeyPersist:
		return runAdmin(ctx, daemonInstallScript(c.value))
	case KeyOllamaPersist:
		return r.agent().Install(ctx)
	case KeyOllamaEnv:
		for k, v := range map[string]string{"OLLAMA_FLASH_ATTENTION": "1", "OLLAMA_KV_CACHE_TYPE": "q8_0"} {
			if err := exec.CommandContext(ctx, "/bin/launchctl", "setenv", k, v).Run(); err != nil {
				return fmt.Errorf("launchctl setenv %s: %w", k, err)
			}
		}
		if f, kv := ReadEnvVars(ctx); f != "1" || kv != "q8_0" {
			return fmt.Errorf("verification failed: env is %q/%q", f, kv)
		}
		return r.restartOllama(ctx)
	}
	return fmt.Errorf("unknown change %q", c.Key)
}

func (r DarwinRunner) Revert(ctx context.Context, c Change) error {
	switch c.Key {
	case KeyWiredLimit:
		prev, err := strconv.ParseInt(strings.Fields(c.Before)[0], 10, 64)
		if err != nil {
			return fmt.Errorf("cannot parse previous value %q", c.Before)
		}
		if err := runAdmin(ctx, fmt.Sprintf("/usr/sbin/sysctl iogpu.wired_limit_mb=%d", prev)); err != nil {
			return err
		}
		if got, err := sysctlWiredMB(ctx); err != nil || got != prev {
			return fmt.Errorf("verification failed: iogpu.wired_limit_mb is %d, expected %d", got, prev)
		}
		return nil
	case KeyPersist:
		return runAdmin(ctx, daemonRemoveScript())
	case KeyOllamaPersist:
		return r.agent().Remove(ctx)
	case KeyOllamaEnv:
		for _, k := range []string{"OLLAMA_FLASH_ATTENTION", "OLLAMA_KV_CACHE_TYPE"} {
			_ = exec.CommandContext(ctx, "/bin/launchctl", "unsetenv", k).Run()
		}
		return r.restartOllama(ctx)
	}
	return fmt.Errorf("unknown change %q", c.Key)
}

// daemonInstallScript builds the LaunchDaemon with Apple's plutil, writing directly as root. Nothing
// user-writable is ever copied into /Library (TOCTOU-safe) and no shell quoting is needed: the only
// variable part is one integer.
func daemonInstallScript(mb int64) string {
	const plutil = "/usr/bin/plutil"
	p := daemonPath
	steps := []string{
		fmt.Sprintf("/bin/rm -f %s", p),
		fmt.Sprintf("%s -create xml1 %s", plutil, p),
		fmt.Sprintf("%s -insert Label -string %s %s", plutil, daemonLabel, p),
		fmt.Sprintf("%s -insert ProgramArguments -array %s", plutil, p),
		fmt.Sprintf("%s -insert ProgramArguments.0 -string /usr/sbin/sysctl %s", plutil, p),
		fmt.Sprintf("%s -insert ProgramArguments.1 -string iogpu.wired_limit_mb=%d %s", plutil, mb, p),
		fmt.Sprintf("%s -insert RunAtLoad -bool YES %s", plutil, p),
		fmt.Sprintf("/usr/sbin/chown root:wheel %s", p),
		fmt.Sprintf("/bin/chmod 644 %s", p),
		fmt.Sprintf("/bin/launchctl bootstrap system %s", p),
	}
	return strings.Join(steps, " && ")
}

func daemonRemoveScript() string {
	return fmt.Sprintf("/bin/launchctl bootout system/%s ; /bin/rm -f %s", daemonLabel, daemonPath)
}

func (r DarwinRunner) restartOllama(ctx context.Context) error {
	if r.OllamaRestart != nil {
		return r.OllamaRestart(ctx)
	}
	return RestartOllamaApp(ctx)
}

// RestartOllamaApp stops Ollama.app and relaunches it so it re-reads the launchd environment, then waits
// until the server answers. It uses SIGTERM (graceful) rather than AppleScript: controlling another app via
// Apple Events needs an Automation permission that a background tool cannot rely on (error -128).
func RestartOllamaApp(ctx context.Context) error {
	// the app's main binary, exactly; then the server it launched, in case it outlives the app
	_ = exec.CommandContext(ctx, "/usr/bin/pkill", "-TERM", "-f", "^/Applications/Ollama.app/Contents/MacOS/Ollama$").Run()
	if err := waitOllama(ctx, false, 15*time.Second); err != nil {
		_ = exec.CommandContext(ctx, "/usr/bin/pkill", "-TERM", "-f", "^/Applications/Ollama.app/Contents/Resources/ollama serve$").Run()
		if err := waitOllama(ctx, false, 15*time.Second); err != nil {
			return fmt.Errorf("Ollama did not stop: %w", err)
		}
	}
	if err := exec.CommandContext(ctx, "/usr/bin/open", "-a", "Ollama").Run(); err != nil {
		return fmt.Errorf("relaunch Ollama: %w", err)
	}
	if err := waitOllama(ctx, true, 90*time.Second); err != nil {
		return fmt.Errorf("Ollama did not come back: %w", err)
	}
	return nil
}

func waitOllama(ctx context.Context, wantUp bool, within time.Duration) error {
	cl := &http.Client{Timeout: 2 * time.Second}
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1:11434/api/version", nil)
		resp, err := cl.Do(req)
		up := err == nil && resp.StatusCode == 200
		if resp != nil {
			resp.Body.Close()
		}
		if up == wantUp {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	return fmt.Errorf("timed out after %s", within)
}
