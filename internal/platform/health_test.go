package platform

import (
	"strings"
	"testing"
)

// realTherm is captured from the reference machine (`pmset -g therm`).
const realTherm = `Note: No thermal warning level has been recorded
Note: No performance warning level has been recorded
Note: No CPU power status has been recorded
`

func TestParseThermHealthyRealCapture(t *testing.T) {
	w, lim, note := ParseTherm(realTherm)
	if w || lim != 0 || !strings.HasPrefix(note, "No thermal") {
		t.Fatalf("warning=%v limit=%d note=%q", w, lim, note)
	}
}

// Synthetic: the throttled format (CPU_Speed_Limit key) is documented behaviour but has not been captured
// here because this machine never throttles under test.
func TestParseThermThrottledSynthetic(t *testing.T) {
	w, lim, _ := ParseTherm(realTherm + "CPU_Scheduler_Limit \t= 100\nCPU_Available_CPUs \t= 10\nCPU_Speed_Limit \t= 78\n")
	if !w || lim != 78 {
		t.Fatalf("warning=%v limit=%d", w, lim)
	}
	if w, _, note := ParseTherm("Note: Thermal warning level 2 recorded\n"); !w || !strings.Contains(note, "level 2") {
		t.Fatalf("non-'No' note must warn: %v %q", w, note)
	}
	if w, lim, _ := ParseTherm("CPU_Speed_Limit = 100\n"); w || lim != 100 {
		t.Fatal("100% is not throttled")
	}
}

func TestParseLoadAvg(t *testing.T) {
	if v, ok := ParseLoadAvg("{ 1.23 1.50 1.70 }"); !ok || v != 1.23 {
		t.Fatalf("%v %v", v, ok)
	}
	if _, ok := ParseLoadAvg("garbage"); ok {
		t.Fatal("garbage parsed")
	}
}

func TestWarnings(t *testing.T) {
	if w := (Health{Cores: 10, Load1: 1, FreeMemPct: 60, SpeedLimitPct: 100}).Warnings("baseline"); len(w) != 0 {
		t.Fatalf("healthy machine warned: %v", w)
	}
	w := Health{OnBattery: true, ThermalWarning: true, ThermalNote: "x", Cores: 10, Load1: 8, FreeMemPct: 5}.Warnings("baseline")
	if len(w) != 4 {
		t.Fatalf("want 4 warnings, got %v", w)
	}
}
