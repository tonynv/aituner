//go:build darwin

package platform

import (
	"context"
	"os"
	"testing"
)

// The fixture is a real system_profiler capture from a Mac Studio (M1 Max) with identifiers removed.
func TestApplySystemProfilerRealCapture(t *testing.T) {
	raw, err := os.ReadFile("testdata/sp_m1max_studio.json")
	if err != nil {
		t.Fatal(err)
	}
	var h Hardware
	if err := applySystemProfiler(&h, raw); err != nil {
		t.Fatal(err)
	}
	if h.Model.Name != "Mac Studio" || h.Model.Identifier != "Mac13,1" || h.CPU.Chip != "Apple M1 Max" {
		t.Fatalf("model/chip: %+v %+v", h.Model, h.CPU)
	}
	if h.CPU.Cores != 10 || h.CPU.PerformanceCores != 8 || h.CPU.EfficiencyCores != 2 {
		t.Fatalf("cores: %+v", h.CPU)
	}
	if h.GPU.Cores != 24 || h.GPU.MetalSupport != "metal4" || h.GPU.Vendor != "Apple" || h.Memory.Type != "LPDDR5" {
		t.Fatalf("gpu/mem: %+v %+v", h.GPU, h.Memory)
	}
}

func TestApplySystemProfilerRejectsGarbage(t *testing.T) {
	var h Hardware
	if err := applySystemProfiler(&h, []byte(`{"SPHardwareDataType":[]}`)); err == nil {
		t.Fatal("expected error for missing hardware section")
	}
	if err := applySystemProfiler(&h, []byte(`not json`)); err == nil {
		t.Fatal("expected error for bad json")
	}
}

func TestCapListedExactToken(t *testing.T) {
	out := "Capabilities for AC Power:\n displaysleep\n lowpowermode\n"
	if !capListed(out, "lowpowermode") || capListed(out, "powermode") {
		t.Fatal("token match wrong")
	}
}

// Live check against the machine running the tests.
func TestDetectLive(t *testing.T) {
	h, err := Current().Detect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if h.Arch == "arm64" && (!h.Memory.Unified || h.GPU.Cores == 0 || h.CPU.PerformanceCores == 0) {
		t.Fatalf("incomplete detection: %+v", h)
	}
	if h.Memory.TotalBytes < 4<<30 || h.Storage.TotalBytes == 0 || h.OS.Version == "" {
		t.Fatalf("implausible: %+v", h)
	}
}

// Live: this machine reports every health field, including GPU utilisation, without admin rights.
func TestCheckHealthLive(t *testing.T) {
	h := CheckHealth(context.Background())
	if h.Cores <= 0 || h.FreeMemPct < 0 || h.GPUBusyPct < 0 || h.SpeedLimitPct <= 0 {
		t.Fatalf("%+v", h)
	}
}
