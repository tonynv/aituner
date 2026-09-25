package platform

import (
	"regexp"
	"strconv"
	"strings"
)

// Health is the machine's state at a point in time, used to decide whether benchmark numbers can be trusted.
type Health struct {
	OnBattery      bool    `json:"on_battery"`
	ThermalWarning bool    `json:"thermal_warning"`
	ThermalNote    string  `json:"thermal_note"`
	SpeedLimitPct  int     `json:"speed_limit_pct"` // 100 = not throttled; 0 = unknown
	Load1          float64 `json:"load1"`
	Cores          int     `json:"cores"`
	FreeMemPct     int     `json:"free_mem_pct"` // -1 unknown
	GPUBusyPct     int     `json:"gpu_busy_pct"` // -1 unknown
}

var loadRe = regexp.MustCompile(`([0-9]+(?:\.[0-9]+)?)`)

// ParseLoadAvg reads sysctl vm.loadavg output such as "{ 1.23 1.50 1.70 }" and returns the 1-minute load.
func ParseLoadAvg(s string) (float64, bool) {
	m := loadRe.FindString(s)
	if m == "" {
		return 0, false
	}
	v, err := strconv.ParseFloat(m, 64)
	return v, err == nil
}

var gpuBusyRe = regexp.MustCompile(`"Device Utilization %"\s*=\s*(\d+)`)

// ParseGPUBusy reads the GPU's "Device Utilization %" from `ioreg -r -d 1 -c IOAccelerator` (no admin needed).
func ParseGPUBusy(out string) (int, bool) {
	m := gpuBusyRe.FindStringSubmatch(out)
	if m == nil {
		return 0, false
	}
	v, err := strconv.Atoi(m[1])
	return v, err == nil && v >= 0 && v <= 100
}

var speedRe = regexp.MustCompile(`CPU_Speed_Limit\s*=\s*(\d+)`)

// ParseTherm interprets `pmset -g therm`. On a healthy machine every line is a "Note: No ... recorded"
// line. A throttled machine reports CPU_Speed_Limit below 100 and/or a Note that is not a "No ..." note.
func ParseTherm(out string) (warning bool, speedLimit int, note string) {
	for _, l := range strings.Split(out, "\n") {
		l = strings.TrimSpace(l)
		if m := speedRe.FindStringSubmatch(l); m != nil {
			speedLimit, _ = strconv.Atoi(m[1])
			if speedLimit < 100 {
				warning = true
			}
		}
		if rest, ok := strings.CutPrefix(l, "Note:"); ok {
			rest = strings.TrimSpace(rest)
			if note == "" {
				note = rest
			}
			if !strings.HasPrefix(rest, "No ") {
				warning, note = true, rest
			}
		}
	}
	return
}

// Warnings turns a Health snapshot into user-facing cautions about benchmark validity.
func (h Health) Warnings(phase string) []string {
	var w []string
	if h.OnBattery {
		w = append(w, "Running on battery: performance is reduced and results are not comparable to runs on AC power.")
	}
	if h.ThermalWarning {
		w = append(w, phase+": the system reports thermal or performance throttling ("+h.ThermalNote+"); results may understate what this machine can do.")
	}
	if h.Cores > 0 && h.Load1 > 0.5*float64(h.Cores) {
		w = append(w, "The machine is busy (load average "+strconv.FormatFloat(h.Load1, 'f', 1, 64)+" on "+strconv.Itoa(h.Cores)+" cores): close heavy apps for accurate numbers.")
	}
	if h.FreeMemPct >= 0 && h.FreeMemPct < 20 {
		w = append(w, "Memory is under pressure ("+strconv.Itoa(h.FreeMemPct)+"% free): benchmark models may be slowed by swapping.")
	}
	return w
}
