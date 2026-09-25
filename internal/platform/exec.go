package platform

import (
	"bytes"
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Probe is one command detection ran, reported to a watcher (the start-up scan). Output is withheld except for
// binaries on showOutput: others can print machine identifiers (system_profiler prints the serial number and UUIDs).
type Probe struct {
	Cmd   string `json:"cmd"`
	Ms    int64  `json:"ms"`
	OK    bool   `json:"ok"`
	Bytes int    `json:"bytes"`
	Out   string `json:"out,omitempty"` // first line, only for showOutput binaries
}

// showOutput lists the binaries whose output is plain kernel/tool values, safe to show as-is.
var showOutput = map[string]bool{"sysctl": true}

type probesKey struct{}

// WithProbes returns a context under which every Run reports a Probe to fn.
func WithProbes(ctx context.Context, fn func(Probe)) context.Context {
	return context.WithValue(ctx, probesKey{}, fn)
}

// Run executes argv (never a shell string) with a timeout and returns trimmed stdout.
func Run(ctx context.Context, timeout time.Duration, name string, args ...string) (string, error) {
	start := time.Now()
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, name, args...)
	var out bytes.Buffer
	cmd.Stdout = &out
	err := cmd.Run()
	res := string(bytes.TrimSpace(out.Bytes()))
	if fn, ok := ctx.Value(probesKey{}).(func(Probe)); ok {
		p := Probe{Cmd: probeCmd(name, args), Ms: time.Since(start).Milliseconds(), OK: err == nil, Bytes: out.Len()}
		if showOutput[filepath.Base(name)] {
			p.Out = clip(strings.SplitN(res, "\n", 2)[0], 60)
		}
		fn(p)
	}
	return res, err
}

// probeCmd renders argv for display: the binary's base name and each argument on one line, long ones clipped
// (an inline Python script becomes its first few words).
func probeCmd(name string, args []string) string {
	parts := []string{filepath.Base(name)}
	for _, a := range args {
		parts = append(parts, clip(strings.Join(strings.Fields(a), " "), 40))
	}
	return strings.Join(parts, " ")
}

func clip(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}
