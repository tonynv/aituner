//go:build darwin

package tune

import (
	"regexp"
	"testing"
)

// Property: anything checkScript accepts has no character that could escape the AppleScript string or
// introduce a redirect, substitution, quote or newline.
func FuzzCheckScript(f *testing.F) {
	f.Add("/usr/sbin/sysctl iogpu.wired_limit_mb=27648")
	f.Add(`x"; rm -rf ~; "`)
	f.Add("a\nb")
	dangerous := regexp.MustCompile("[\"'`$\\\\<>()\\n\\r\\t*?~{}\\[\\]!#]")
	f.Fuzz(func(t *testing.T, s string) {
		if checkScript(s) == nil && dangerous.MatchString(s) {
			t.Fatalf("accepted dangerous script %q", s)
		}
	})
}
