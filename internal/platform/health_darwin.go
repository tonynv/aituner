//go:build darwin

package platform

import (
	"context"
	"strings"
	"time"
)

// CheckHealth samples power source, thermal state, load, memory pressure and GPU utilisation.
func CheckHealth(ctx context.Context) Health {
	h := Health{FreeMemPct: memoryFreePct(ctx), SpeedLimitPct: 100, GPUBusyPct: -1}
	if out, err := Run(ctx, 5*time.Second, "/usr/bin/pmset", "-g", "batt"); err == nil {
		h.OnBattery = strings.Contains(out, "Battery Power")
	}
	if out, err := Run(ctx, 5*time.Second, "/usr/bin/pmset", "-g", "therm"); err == nil {
		h.ThermalWarning, h.SpeedLimitPct, h.ThermalNote = ParseTherm(out)
		if h.SpeedLimitPct == 0 {
			h.SpeedLimitPct = 100
		}
	}
	if out, err := Run(ctx, 3*time.Second, "/usr/sbin/sysctl", "-n", "vm.loadavg"); err == nil {
		h.Load1, _ = ParseLoadAvg(out)
	}
	if n, ok := sysctlInt(ctx, "hw.ncpu"); ok {
		h.Cores = int(n)
	}
	if out, err := Run(ctx, 3*time.Second, "/usr/sbin/ioreg", "-r", "-d", "1", "-c", "IOAccelerator"); err == nil {
		if v, ok := ParseGPUBusy(out); ok {
			h.GPUBusyPct = v
		}
	}
	return h
}
