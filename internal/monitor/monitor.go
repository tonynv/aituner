// Package monitor samples the machine's live performance for the Monitor view and the menu bar. It streams macmon
// (sudoless Apple Silicon power, frequency, temperature and memory, from the same counters powermetrics reads) when it
// is installed, and falls back to the built-in health sampler (GPU utilisation and memory) when it is not.
//
// Sampling runs only while someone is watching (Want) or keep reports true (a model is being served), and the history
// is an in-memory ring: live telemetry is not worth persisting.
package monitor

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/tonynv/aituner/internal/platform"
)

const (
	historyLen   = 600 // samples kept: 10 minutes at macmon's 1 s interval
	builtinEvery = 2 * time.Second
)

// Sample is one reading. Fields a source cannot measure are -1 (percentages, temperatures) or 0 (watts, MHz).
type Sample struct {
	Seq      int64   `json:"seq"`
	T        int64   `json:"t"`       // unix milliseconds
	GPUPct   float64 `json:"gpu_pct"` // GPU busy (active residency), 0-100
	GPUMHz   int     `json:"gpu_mhz"`
	CPUPct   float64 `json:"cpu_pct"`
	GPUW     float64 `json:"gpu_w"`
	CPUW     float64 `json:"cpu_w"`
	ANEW     float64 `json:"ane_w"`
	SysW     float64 `json:"sys_w"`
	RAMUsed  int64   `json:"ram_used"`
	RAMTotal int64   `json:"ram_total"`
	SwapUsed int64   `json:"swap_used"`
	GPUTempC float64 `json:"gpu_temp_c"`
	CPUTempC float64 `json:"cpu_temp_c"`
}

// Source names where samples come from.
const (
	SourceMacmon  = "macmon"
	SourceBuiltin = "builtin"
)

// macmonLine is the subset of `macmon pipe` output aituner uses (macmon 0.8).
type macmonLine struct {
	GPUActive float64 `json:"gpu_active_ratio"`
	GPUMHz    int     `json:"gpu_freq_mhz"`
	CPUUsage  float64 `json:"cpu_usage_pct"` // a 0-1 ratio despite the name
	GPUPower  float64 `json:"gpu_power"`
	CPUPower  float64 `json:"cpu_power"`
	ANEPower  float64 `json:"ane_power"`
	SysPower  float64 `json:"sys_power"`
	Memory    struct {
		RAMTotal  int64 `json:"ram_total"`
		RAMUsage  int64 `json:"ram_usage"`
		SwapUsage int64 `json:"swap_usage"`
	} `json:"memory"`
	Temp struct {
		CPU float64 `json:"cpu_temp_avg"`
		GPU float64 `json:"gpu_temp_avg"`
	} `json:"temp"`
}

// ParseMacmon converts one `macmon pipe` JSON line into a Sample (without Seq or T).
func ParseMacmon(line []byte) (Sample, bool) {
	var m macmonLine
	if err := json.Unmarshal(line, &m); err != nil || m.Memory.RAMTotal <= 0 {
		return Sample{}, false
	}
	temp := func(v float64) float64 {
		if v <= 0 {
			return -1
		}
		return v
	}
	return Sample{
		GPUPct: clampPct(m.GPUActive * 100), GPUMHz: m.GPUMHz, CPUPct: clampPct(m.CPUUsage * 100),
		GPUW: m.GPUPower, CPUW: m.CPUPower, ANEW: m.ANEPower, SysW: m.SysPower,
		RAMUsed: m.Memory.RAMUsage, RAMTotal: m.Memory.RAMTotal, SwapUsed: m.Memory.SwapUsage,
		GPUTempC: temp(m.Temp.GPU), CPUTempC: temp(m.Temp.CPU),
	}, true
}

func clampPct(v float64) float64 { return min(max(v, 0), 100) }

// Monitor keeps the live history. The zero value is not usable; call New.
type Monitor struct {
	ramTotal func() int64 // for the built-in fallback, which only knows the free percentage
	keep     func() bool  // keep sampling without watchers (a model is being served)
	macmon   func() (string, bool)
	health   func(context.Context) platform.Health
	idleFor  time.Duration // stop sampling this long after the last watcher, unless keep() is true

	mu       sync.Mutex
	ring     []Sample
	seq      int64
	source   string
	running  bool
	lastWant time.Time
}

// New returns a monitor. ramTotal reports the machine's memory in bytes (0 while unknown); keep may be nil.
func New(ramTotal func() int64, keep func() bool) *Monitor {
	if keep == nil {
		keep = func() bool { return false }
	}
	return &Monitor{ramTotal: ramTotal, keep: keep, macmon: FindMacmon, health: platform.CheckHealth, idleFor: 30 * time.Second}
}

// FindMacmon returns the macmon binary from Homebrew or MacPorts, never from PATH, so a stray binary cannot stand in.
func FindMacmon() (string, bool) {
	for _, p := range []string{"/opt/homebrew/bin/macmon", "/usr/local/bin/macmon", "/opt/local/bin/macmon"} {
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() && fi.Mode()&0o111 != 0 {
			return p, true
		}
	}
	return "", false
}

// Want marks that someone is watching and starts sampling if it is not running. ctx bounds the sampler's lifetime
// (aituner's own context, not a request's).
func (m *Monitor) Want(ctx context.Context) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastWant = time.Now()
	if !m.running {
		m.running = true
		go m.loop(ctx)
	}
}

// Since returns the samples newer than seq (all of them for seq 0), and the current source.
func (m *Monitor) Since(seq int64) ([]Sample, string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []Sample{}
	for _, s := range m.ring {
		if s.Seq > seq {
			out = append(out, s)
		}
	}
	return out, m.source
}

func (m *Monitor) add(s Sample, source string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.seq++
	s.Seq, s.T = m.seq, time.Now().UnixMilli()
	m.ring = append(m.ring, s)
	if len(m.ring) > historyLen {
		m.ring = m.ring[len(m.ring)-historyLen:]
	}
	m.source = source
}

// idle reports whether sampling should stop, and marks it stopped if so (under the same lock Want takes, so a
// watcher arriving now either sees running=false and starts a new loop, or keeps this one alive).
func (m *Monitor) idle() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if time.Since(m.lastWant) < m.idleFor || m.keep() {
		return false
	}
	m.running = false
	return true
}

func (m *Monitor) loop(ctx context.Context) {
	for ctx.Err() == nil {
		if bin, ok := m.macmon(); ok {
			if m.streamMacmon(ctx, bin) {
				return // stopped because idle or cancelled
			}
			// macmon exited or failed: sample with the built-in source for a while, then try it again
		}
		if m.builtin(ctx, time.Minute) {
			return
		}
	}
	m.mu.Lock()
	m.running = false
	m.mu.Unlock()
}

// streamMacmon reads `macmon pipe` until idle or cancelled (true) or until macmon stops (false).
func (m *Monitor) streamMacmon(ctx context.Context, bin string) bool {
	cctx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(cctx, bin, "pipe", "-i", "1000")
	cmd.WaitDelay = 2 * time.Second
	out, err := cmd.StdoutPipe()
	if err != nil || cmd.Start() != nil {
		return false
	}
	defer cmd.Wait() //nolint:errcheck // cancelled on purpose; its exit status is not interesting
	sc := bufio.NewScanner(out)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		if s, ok := ParseMacmon(sc.Bytes()); ok {
			m.add(s, SourceMacmon)
		}
		if ctx.Err() != nil {
			return true
		}
		if m.idle() {
			return true
		}
	}
	return ctx.Err() != nil
}

// builtin samples the health checker every builtinEvery for up to d, returning true if it stopped because idle or
// cancelled.
func (m *Monitor) builtin(ctx context.Context, d time.Duration) bool {
	end := time.Now().Add(d)
	t := time.NewTicker(builtinEvery)
	defer t.Stop()
	for time.Now().Before(end) {
		h := m.health(ctx)
		s := Sample{GPUPct: -1, CPUPct: -1, GPUTempC: -1, CPUTempC: -1, RAMTotal: m.ramTotal()}
		if h.GPUBusyPct >= 0 {
			s.GPUPct = float64(h.GPUBusyPct)
		}
		if h.FreeMemPct >= 0 && s.RAMTotal > 0 {
			s.RAMUsed = s.RAMTotal * int64(100-h.FreeMemPct) / 100
		}
		m.add(s, SourceBuiltin)
		if m.idle() {
			return true
		}
		select {
		case <-ctx.Done():
			return true
		case <-t.C:
		}
	}
	return false
}
