package connect

import (
	"context"
	"fmt"
)

// TerminalMonitor is a sudoless performance monitor for Apple Silicon that aituner can install with Homebrew and open in
// Terminal. Each was checked against its own README: macmon and mactop read IOReport/SMC without root; nvtop's Apple
// support is its own "limited" Metal backend. (gpustat is NVIDIA-only and asitop needs sudo, so neither is offered.)
type TerminalMonitor struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	About    string `json:"about"`
	Homepage string `json:"homepage"`
	Formula  string `json:"-"` // Homebrew formula; also the command it provides
}

var terminalMonitors = []TerminalMonitor{
	{ID: "macmon", Name: "macmon", Formula: "macmon", Homepage: "https://github.com/vladkens/macmon",
		About: "CPU, GPU and ANE power, frequency, temperature and memory. When installed, aituner's live stats come from it."},
	{ID: "mactop", Name: "mactop", Formula: "mactop", Homepage: "https://github.com/metaspartan/mactop",
		About: "Full-screen monitor: per-core usage, GPU usage and frequency, power, DRAM bandwidth, fans, per-process GPU."},
	{ID: "nvtop", Name: "nvtop", Formula: "nvtop", Homepage: "https://github.com/Syllo/nvtop",
		About: "GPU process monitor. Its Apple Silicon support is limited (its README says so); useful if you know it from NVIDIA."},
}

// TerminalMonitors lists the monitors aituner offers.
func TerminalMonitors() []TerminalMonitor { return terminalMonitors }

// TerminalMonitorByID returns the monitor with this id, or nil.
func TerminalMonitorByID(id string) *TerminalMonitor {
	for i := range terminalMonitors {
		if terminalMonitors[i].ID == id {
			return &terminalMonitors[i]
		}
	}
	return nil
}

// Installed reports whether the monitor's command is on this machine.
func (m TerminalMonitor) Installed(env Env) bool {
	_, ok := env.Run.Look(m.Formula)
	return ok
}

// Install installs the monitor with Homebrew (a no-op when it is already present).
func (m TerminalMonitor) Install(ctx context.Context, env Env, emit Emit) error {
	return ensureBrew(ctx, env, emit, m.Formula, false, m.Formula)
}

// Open runs the monitor in a new Terminal window, in the home folder.
func (m TerminalMonitor) Open(ctx context.Context, env Env) error {
	bin, ok := env.Run.Look(m.Formula)
	if !ok {
		return fmt.Errorf("%s is not installed", m.Name)
	}
	_, err := openTerminal(ctx, env, "monitor-"+m.ID, bin, env.Home)
	return err
}
