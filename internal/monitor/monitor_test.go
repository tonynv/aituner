package monitor

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tonynv/aituner/internal/platform"
)

// testdata/macmon_pipe.json is one real `macmon pipe` line from macmon 0.8.2 on the reference machine (M1 Max).
func TestParseMacmonRealCapture(t *testing.T) {
	b, err := os.ReadFile("testdata/macmon_pipe.json")
	if err != nil {
		t.Fatal(err)
	}
	s, ok := ParseMacmon(b)
	if !ok {
		t.Fatal("did not parse")
	}
	if s.RAMTotal != 34359738368 || s.RAMUsed <= 0 || s.GPUMHz <= 0 || s.SysW <= 0 || s.GPUTempC <= 0 || s.CPUTempC <= 0 {
		t.Fatalf("%+v", s)
	}
	if s.GPUPct < 0 || s.GPUPct > 100 || s.CPUPct < 0 || s.CPUPct > 100 {
		t.Fatalf("percentages out of range: %+v", s)
	}
	for _, bad := range []string{"", "{}", "not json", `{"memory":{"ram_total":0}}`} {
		if _, ok := ParseMacmon([]byte(bad)); ok {
			t.Errorf("parsed %q", bad)
		}
	}
	if s, _ := ParseMacmon([]byte(`{"gpu_active_ratio":3,"memory":{"ram_total":1}}`)); s.GPUPct != 100 || s.GPUTempC != -1 {
		t.Fatalf("not clamped or missing temperature not marked unknown: %+v", s)
	}
}

// fakeMacmon is a script that prints the real capture once a tenth of a second, like `macmon pipe` does once a second.
func fakeMacmon(t *testing.T) string {
	t.Helper()
	fixture, _ := filepath.Abs("testdata/macmon_pipe.json")
	p := filepath.Join(t.TempDir(), "macmon")
	script := "#!/bin/sh\nwhile :; do cat '" + fixture + "'; sleep 0.1; done\n"
	if err := os.WriteFile(p, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return p
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for end := time.Now().Add(5 * time.Second); time.Now().Before(end); time.Sleep(20 * time.Millisecond) {
		if cond() {
			return
		}
	}
	t.Fatal("timed out waiting for " + what)
}

func (m *Monitor) isRunning() bool { m.mu.Lock(); defer m.mu.Unlock(); return m.running }

func TestStreamsMacmonStopsWhenIdleAndRestarts(t *testing.T) {
	bin := fakeMacmon(t)
	m := New(1, nil)
	m.macmon = func() (string, bool) { return bin, true }
	m.idleFor = 300 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	m.Want(ctx)
	waitFor(t, "samples", func() bool { s, _ := m.Since(0); return len(s) >= 2 })
	s, src := m.Since(0)
	if src != SourceMacmon || s[1].Seq <= s[0].Seq || s[0].T == 0 {
		t.Fatalf("%s %+v", src, s[:2])
	}
	if newer, _ := m.Since(s[len(s)-1].Seq); len(newer) != 0 {
		t.Fatal("Since returned samples it was told the caller has")
	}
	waitFor(t, "idle stop", func() bool { return !m.isRunning() })
	m.Want(ctx)
	if !m.isRunning() {
		t.Fatal("Want did not restart sampling")
	}
}

func TestKeepHoldsSamplingWithoutWatchers(t *testing.T) {
	bin := fakeMacmon(t)
	keep := true
	m := New(1, func() bool { return keep })
	m.macmon = func() (string, bool) { return bin, true }
	m.idleFor = time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.Want(ctx)
	time.Sleep(400 * time.Millisecond)
	if !m.isRunning() {
		t.Fatal("stopped while keep() was true")
	}
}

func TestBuiltinFallbackWhenMacmonMissing(t *testing.T) {
	m := New(32<<30, nil)
	m.macmon = func() (string, bool) { return "", false }
	m.health = func(context.Context) platform.Health { return platform.Health{GPUBusyPct: 40, FreeMemPct: 75} }
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.Want(ctx)
	waitFor(t, "a sample", func() bool { s, _ := m.Since(0); return len(s) > 0 })
	s, src := m.Since(0)
	if src != SourceBuiltin || s[0].GPUPct != 40 || s[0].RAMUsed != 8<<30 || s[0].CPUPct != -1 || s[0].GPUTempC != -1 {
		t.Fatalf("%s %+v", src, s[0])
	}
}

// Live: the real macmon, when installed, streams parseable samples.
func TestRealMacmonLive(t *testing.T) {
	if _, ok := FindMacmon(); !ok {
		t.Skip("macmon not installed")
	}
	m := New(0, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.Want(ctx)
	waitFor(t, "a macmon sample", func() bool { s, src := m.Since(0); return len(s) > 0 && src == SourceMacmon })
	if s, _ := m.Since(0); s[0].RAMTotal <= 0 || s[0].SysW <= 0 {
		t.Fatalf("%+v", s[0])
	}
}
