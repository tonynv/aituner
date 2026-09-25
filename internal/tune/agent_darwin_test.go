//go:build darwin

package tune

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// Real launchctl/plutil, throwaway label and variable name: proves install, RunAtLoad execution, and removal.
func TestAgentInstallRunsAtLoadAndRemoves(t *testing.T) {
	ctx := context.Background()
	a := AgentSpec{Label: "ai.aituner.test." + strings.ReplaceAll(t.Name(), "/", "-"), Dir: t.TempDir(), Env: [][2]string{{"AITUNER_TEST_VAR", "hello"}}}
	t.Cleanup(func() { _ = a.Remove(ctx); _ = guiLaunchctl(ctx, "unsetenv", "AITUNER_TEST_VAR").Run() })

	if a.Installed() {
		t.Fatal("must start absent")
	}
	if err := a.Install(ctx); err != nil {
		t.Fatal(err)
	}
	if !a.Installed() {
		t.Fatal("plist missing")
	}
	if fi, _ := os.Stat(a.path()); fi.Mode().Perm()&0o022 != 0 {
		t.Fatalf("plist must not be group/world writable: %v", fi.Mode())
	}
	// Install verifies the variables itself; check independently, then prove a re-run repairs a cleared value.
	if out, _ := guiLaunchctl(ctx, "getenv", "AITUNER_TEST_VAR").Output(); strings.TrimSpace(string(out)) != "hello" {
		t.Fatalf("variable not set after Install: %q", out)
	}
	_ = guiLaunchctl(ctx, "unsetenv", "AITUNER_TEST_VAR").Run()
	if err := a.Install(ctx); err != nil { // idempotent re-install re-applies the variable
		t.Fatalf("re-install: %v", err)
	}
	if out, _ := guiLaunchctl(ctx, "getenv", "AITUNER_TEST_VAR").Output(); strings.TrimSpace(string(out)) != "hello" {
		t.Fatalf("re-install did not restore the variable: %q", out)
	}
	if err := a.Remove(ctx); err != nil {
		t.Fatal(err)
	}
	if a.Installed() {
		t.Fatal("plist not removed")
	}
	if err := exec.Command("/bin/launchctl", "print", a.domain()+"/"+a.Label).Run(); err == nil {
		t.Fatal("agent still loaded after Remove")
	}
	if err := a.Remove(ctx); err != nil {
		t.Fatalf("Remove must be idempotent: %v", err)
	}
}

func TestAgentScriptIsConstantOnly(t *testing.T) {
	s := DefaultAgent().script()
	if s != "/bin/launchctl setenv OLLAMA_FLASH_ATTENTION 1; /bin/launchctl setenv OLLAMA_KV_CACHE_TYPE q8_0" {
		t.Fatalf("script: %q", s)
	}
}

// Environment changes must land in the GUI domain (where Ollama.app reads them) even when the caller runs in another
// launchd session, such as tmux's Background session. Checked against launchd's own view of the GUI domain.
func TestGUILaunchctlTargetsTheGUIDomain(t *testing.T) {
	ctx := context.Background()
	t.Cleanup(func() { _ = guiLaunchctl(ctx, "unsetenv", "AITUNER_TEST_GUI").Run() })
	if err := guiLaunchctl(ctx, "setenv", "AITUNER_TEST_GUI", "yes").Run(); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("/bin/launchctl", "print", DefaultAgent().domain()).Output()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "AITUNER_TEST_GUI => yes") {
		t.Fatal("variable is not in the GUI domain's environment")
	}
	if v, _ := guiLaunchctl(ctx, "getenv", "AITUNER_TEST_GUI").Output(); strings.TrimSpace(string(v)) != "yes" {
		t.Fatalf("getenv read %q", v)
	}
}
