//go:build darwin

package tune

import (
	"strings"
	"testing"
)

func TestCheckScriptRejectsInjection(t *testing.T) {
	bad := []string{
		`/usr/sbin/sysctl iogpu.wired_limit_mb=1"; rm -rf ~; "`,
		"/usr/sbin/sysctl iogpu.wired_limit_mb=1\nrm -rf /",
		"/usr/sbin/sysctl x=$(id)",
		"/usr/sbin/sysctl x=`id`",
		`/bin/echo \"`,
		"/bin/echo x > /etc/passwd",
		"/bin/echo 'x'",
	}
	for _, s := range bad {
		if checkScript(s) == nil {
			t.Errorf("accepted %q", s)
		}
	}
}

func TestGeneratedScriptsPassTheAllowList(t *testing.T) {
	for _, s := range []string{
		"/usr/sbin/sysctl iogpu.wired_limit_mb=27648",
		daemonInstallScript(27648),
		daemonRemoveScript(),
	} {
		if err := checkScript(s); err != nil {
			t.Errorf("%q: %v", s, err)
		}
	}
	if !strings.Contains(daemonInstallScript(27648), "iogpu.wired_limit_mb=27648") {
		t.Fatal("value missing from daemon script")
	}
}
