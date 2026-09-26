//go:build darwin

package platform

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

// Live: a full detection under WithProbes reports its commands, and none of what it reports carries this Mac's serial
// number or hardware UUID (system_profiler prints both).
func TestDetectProbesNeverLeakIdentifiers(t *testing.T) {
	var sb strings.Builder
	n := 0
	ctx := WithProbes(context.Background(), func(p Probe) { n++; b, _ := json.Marshal(p); sb.Write(b) })
	if _, err := Current().Detect(ctx); err != nil {
		t.Fatal(err)
	}
	if n < 5 || !strings.Contains(sb.String(), "system_profiler") {
		t.Fatalf("%d probes: %s", n, sb.String())
	}
	raw, err := Run(context.Background(), 30*time.Second, "/usr/sbin/system_profiler", "-json", "SPHardwareDataType")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		SP []map[string]any `json:"SPHardwareDataType"`
	}
	if err := json.Unmarshal([]byte(raw), &doc); err != nil || len(doc.SP) == 0 {
		t.Fatal(err)
	}
	for _, k := range []string{"serial_number", "platform_UUID", "provisioning_UDID"} {
		if v, _ := doc.SP[0][k].(string); v != "" && strings.Contains(sb.String(), v) {
			t.Fatalf("probe output contains %s", k)
		}
	}
}

// Live: macOS's own device table maps this Mac and known models to their pictures, and unknown codes to nothing.
func TestDeviceIconFromCoreTypes(t *testing.T) {
	ctx := context.Background()
	if p, ok := DeviceIcon(ctx, "Mac13,1"); !ok || filepath.Base(p) != "com.apple.macstudio.icns" {
		t.Fatalf("Mac Studio: %q %v", p, ok)
	}
	if p, ok := DeviceIcon(ctx, "MacPro7,1"); !ok || !strings.Contains(p, "macpro") {
		t.Fatalf("colour-variant codes: %q %v", p, ok)
	}
	if _, ok := DeviceIcon(ctx, "NotAMac99,9"); ok {
		t.Fatal("unknown identifier matched")
	}
	h, err := Current().Detect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := DeviceIcon(ctx, h.Model.Identifier); !ok {
		t.Fatalf("no picture for this Mac (%s)", h.Model.Identifier)
	}
}

// A virtual Mac (GitHub's macos-26 runner) reports number_processors as a number; detection must not fail on it.
func TestApplySystemProfilerNumericFields(t *testing.T) {
	raw := []byte(`{"SPHardwareDataType":[{"machine_name":"Apple Virtual Machine 1","machine_model":"VirtualMac2,1","chip_type":"Apple M1 (Virtual)","number_processors":3,"physical_memory":"7 GB"}],
		"SPDisplaysDataType":[{"_name":"Apple Paravirtual device","sppci_cores":8,"sppci_device_type":"spdisplays_gpu"}],"SPMemoryDataType":[]}`)
	var h Hardware
	if err := applySystemProfiler(&h, raw); err != nil {
		t.Fatal(err)
	}
	if h.CPU.Cores != 3 || h.GPU.Cores != 8 || h.Model.Identifier != "VirtualMac2,1" {
		t.Fatalf("%+v %+v", h.CPU, h.GPU)
	}
	if err := applySystemProfiler(&h, []byte(`{"SPHardwareDataType":[{"number_processors":true}]}`)); err != nil {
		t.Fatalf("an unexpected type must not fail detection: %v", err)
	}
}
