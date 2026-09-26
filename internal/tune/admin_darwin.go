//go:build darwin

package tune

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// runAdmin runs a shell script with the native macOS administrator prompt. aituner never sees the
// password. The script must be built from constants and integers only; checkScript enforces a strict
// character allow-list as a second line of defence against accidental interpolation of untrusted text.
func runAdmin(ctx context.Context, script string) error {
	if err := checkScript(script); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	as := fmt.Sprintf(`do shell script "%s" with administrator privileges`, script)
	out, err := exec.CommandContext(ctx, "/usr/bin/osascript", "-e", as).CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if strings.Contains(msg, "-128") {
			return errors.New("administrator approval was cancelled")
		}
		return fmt.Errorf("privileged command failed: %s", msg)
	}
	return nil
}

// allowed: letters, digits, space and the punctuation our fixed templates use. No backslash, quotes,
// backticks, dollar signs, redirects, parentheses or newlines, which are what would break out of the
// AppleScript/sh strings.
var scriptRe = regexp.MustCompile(`^[A-Za-z0-9 ._=/:&|;-]+$`)

func checkScript(s string) error {
	if !scriptRe.MatchString(s) {
		return errors.New("refusing privileged script with disallowed characters")
	}
	return nil
}

// OllamaCLILink is the command-line link Ollama.app installs (owned by root).
const OllamaCLILink = "/usr/local/bin/ollama"

// RemoveOllamaCLILink removes Ollama.app's root-owned command-line link, with the native admin prompt, and only when
// it is a symlink into /Applications/Ollama.app (never a real binary or a link into anything else).
func RemoveOllamaCLILink(ctx context.Context) error {
	target, err := os.Readlink(OllamaCLILink)
	if err != nil {
		return nil // not a link (or not there): nothing of Ollama.app's to remove
	}
	if !strings.HasPrefix(target, "/Applications/Ollama.app/") {
		return fmt.Errorf("%s points to %s, not into Ollama.app: left in place", OllamaCLILink, target)
	}
	return runAdmin(ctx, "/bin/rm -f "+OllamaCLILink)
}
