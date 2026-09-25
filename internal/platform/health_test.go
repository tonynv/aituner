package platform

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
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

// testdata/ioreg_accelerator.txt is trimmed from `ioreg -r -d 1 -c IOAccelerator` on the reference machine (M1 Max).
func TestParseGPUBusyRealCapture(t *testing.T) {
	b, err := os.ReadFile("testdata/ioreg_accelerator.txt")
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := ParseGPUBusy(string(b)); !ok || v < 0 || v > 100 {
		t.Fatalf("%v %v", v, ok)
	}
	if _, ok := ParseGPUBusy("no accelerator"); ok {
		t.Fatal("parsed garbage")
	}
	if _, ok := ParseGPUBusy(`"Device Utilization %"=250`); ok {
		t.Fatal("accepted an out-of-range percentage")
	}
}

func TestProbesReportCommandsWithoutOutputUnlessAllowed(t *testing.T) {
	var got []Probe
	ctx := WithProbes(context.Background(), func(p Probe) { got = append(got, p) })
	if _, err := Run(ctx, 5*time.Second, "/bin/echo", "serial-ABC123", strings.Repeat("x", 80)); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(ctx, 5*time.Second, "/usr/sbin/sysctl", "-n", "hw.ncpu"); err != nil {
		t.Fatal(err)
	}
	Run(ctx, 5*time.Second, "/usr/bin/false")
	if len(got) != 3 {
		t.Fatalf("%+v", got)
	}
	if got[0].Out != "" || got[0].Bytes == 0 || !got[0].OK || !strings.HasPrefix(got[0].Cmd, "echo serial-ABC123 xxx") || !strings.HasSuffix(got[0].Cmd, "…") {
		t.Fatalf("echo probe leaked output or rendered badly: %+v", got[0])
	}
	if got[1].Out == "" || got[1].Cmd != "sysctl -n hw.ncpu" {
		t.Fatalf("sysctl probe: %+v", got[1])
	}
	if got[2].OK {
		t.Fatal("a failing command reported OK")
	}
	if _, err := Run(context.Background(), time.Second, "/usr/bin/true"); err != nil {
		t.Fatal("Run without a watcher", err)
	}
}
