//go:build darwin

package platform

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type darwinPlatform struct{}

func Current() Platform { return darwinPlatform{} }

func (darwinPlatform) Name() string { return "darwin" }

// DataDir is ~/Library/Application Support/aituner unless AITUNER_DATA_DIR overrides it (dev/tests).
func (darwinPlatform) DataDir() (string, error) {
	if d := os.Getenv("AITUNER_DATA_DIR"); d != "" {
		return d, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "Application Support", "aituner"), nil
}

func sysctl(ctx context.Context, key string) (string, bool) {
	out, err := Run(ctx, 3*time.Second, "/usr/sbin/sysctl", "-n", key)
	return out, err == nil
}

func sysctlInt(ctx context.Context, key string) (int64, bool) {
	s, ok := sysctl(ctx, key)
	if !ok {
		return 0, false
	}
	v, err := strconv.ParseInt(s, 10, 64)
	return v, err == nil
}

func (p darwinPlatform) Detect(ctx context.Context) (*Hardware, error) {
	h := &Hardware{Platform: "darwin", DetectedAt: time.Now().UnixMilli()}
	h.Arch, _ = sysctl(ctx, "hw.machine")
	if h.Arch == "" {
		h.Arch = "unknown"
	}
	if v, ok := sysctl(ctx, "kern.osproductversion"); ok {
		h.OS.Version = v
	}
	h.OS.Name = "macOS"
	h.OS.Build, _ = sysctl(ctx, "kern.osversion")

	// system_profiler gives the marketing names and GPU core count
	sp, err := Run(ctx, 30*time.Second, "/usr/sbin/system_profiler", "-json", "SPHardwareDataType", "SPDisplaysDataType", "SPMemoryDataType")
	if err != nil {
		return nil, fmt.Errorf("system_profiler: %w", err)
	}
	if err := applySystemProfiler(h, []byte(sp)); err != nil {
		return nil, err
	}

	if n, ok := sysctlInt(ctx, "hw.ncpu"); ok && h.CPU.Cores == 0 {
		h.CPU.Cores = int(n)
	}
	if n, ok := sysctlInt(ctx, "hw.perflevel0.physicalcpu"); ok {
		h.CPU.PerformanceCores = int(n)
	}
	if n, ok := sysctlInt(ctx, "hw.perflevel1.physicalcpu"); ok {
		h.CPU.EfficiencyCores = int(n)
	}
	if h.CPU.PerformanceCores+h.CPU.EfficiencyCores > 0 {
		h.CPU.Cores = h.CPU.PerformanceCores + h.CPU.EfficiencyCores
	}
	if n, ok := sysctlInt(ctx, "hw.perflevel0.l2cachesize"); ok {
		h.CPU.L2PerfBytes = n
	}
	if n, ok := sysctlInt(ctx, "hw.cachelinesize"); ok {
		h.CPU.CacheLineBytes = int(n)
	}
	if h.CPU.Chip == "" {
		h.CPU.Chip, _ = sysctl(ctx, "machdep.cpu.brand_string")
	}
	if n, ok := sysctlInt(ctx, "hw.memsize"); ok {
		h.Memory.TotalBytes = n
	}
	h.Memory.Unified = h.Arch == "arm64"
	if n, ok := sysctlInt(ctx, "iogpu.wired_limit_mb"); ok {
		h.Memory.WiredLimitMB, h.Memory.WiredLimitKnown = n, true
	}
	h.Memory.PressureFreePct = memoryFreePct(ctx)

	// storage of the volume that holds the data dir (where models will land)
	if dir, err := p.DataDir(); err == nil {
		h.Storage = diskFor(dir)
	}

	h.Power = detectPower(ctx)
	h.Software = p.detectSoftware(ctx)
	return h, nil
}

type spDoc struct {
	Hardware []struct {
		MachineName  string     `json:"machine_name"`
		MachineModel string     `json:"machine_model"`
		ModelNumber  string     `json:"model_number"`
		ChipType     string     `json:"chip_type"`
		NumProc      flexString `json:"number_processors"`
		Memory       flexString `json:"physical_memory"`
	} `json:"SPHardwareDataType"`
	Displays []struct {
		Name    string     `json:"_name"`
		Cores   flexString `json:"sppci_cores"`
		Metal   string     `json:"spdisplays_mtlgpufamilysupport"`
		Vendor  string     `json:"spdisplays_vendor"`
		Type    string     `json:"sppci_device_type"`
		Model   string     `json:"sppci_model"`
		Screens []struct {
			Name string `json:"_name"`
		} `json:"spdisplays_ndrvs"`
	} `json:"SPDisplaysDataType"`
	Memory []struct {
		Type string `json:"dimm_type"`
	} `json:"SPMemoryDataType"`
}

// flexString accepts a JSON string or number. system_profiler's types differ between machines: number_processors is
// "proc 10:8:2:0" on an M1 Max but a bare number on a virtual Mac (seen on GitHub's macos-26 runner). Other JSON types
// leave it empty rather than failing the whole detection.
type flexString string

func (f *flexString) UnmarshalJSON(b []byte) error {
	var s string
	if json.Unmarshal(b, &s) == nil {
		*f = flexString(s)
		return nil
	}
	var n json.Number
	if json.Unmarshal(b, &n) == nil {
		*f = flexString(n.String())
	}
	return nil
}

var procRe = regexp.MustCompile(`proc (\d+):(\d+):(\d+)`)

// applySystemProfiler fills the fields system_profiler is authoritative for. Exposed for tests with real captures.
func applySystemProfiler(h *Hardware, raw []byte) error {
	var d spDoc
	if err := json.Unmarshal(raw, &d); err != nil {
		return fmt.Errorf("parse system_profiler: %w", err)
	}
	if len(d.Hardware) == 0 {
		return errors.New("system_profiler: no hardware section")
	}
	hw := d.Hardware[0]
	h.Model = Model{Name: hw.MachineName, Identifier: hw.MachineModel, Number: hw.ModelNumber}
	h.CPU.Chip = hw.ChipType
	if m := procRe.FindStringSubmatch(string(hw.NumProc)); m != nil {
		h.CPU.Cores, _ = strconv.Atoi(m[1])
		h.CPU.PerformanceCores, _ = strconv.Atoi(m[2])
		h.CPU.EfficiencyCores, _ = strconv.Atoi(m[3])
	} else if n, err := strconv.Atoi(string(hw.NumProc)); err == nil {
		h.CPU.Cores = n // a bare count: the performance/efficiency split comes from sysctl hw.perflevel*
	}
	for _, g := range d.Displays {
		if g.Type == "spdisplays_gpu" || g.Cores != "" {
			h.GPU.Name = g.Name
			h.GPU.Cores, _ = strconv.Atoi(string(g.Cores))
			h.GPU.MetalSupport = strings.TrimPrefix(g.Metal, "spdisplays_")
			h.GPU.Vendor = strings.TrimPrefix(g.Vendor, "sppci_vendor_")
		}
		for _, s := range g.Screens {
			h.Displays = append(h.Displays, s.Name)
		}
	}
	if len(d.Memory) > 0 {
		h.Memory.Type = d.Memory[0].Type
	}
	return nil
}

func diskFor(dir string) Disk {
	// the dir may not exist yet; walk up to an existing ancestor
	p := dir
	for {
		if _, err := os.Stat(p); err == nil {
			break
		}
		parent := filepath.Dir(p)
		if parent == p {
			break
		}
		p = parent
	}
	var st syscall.Statfs_t
	if err := syscall.Statfs(p, &st); err != nil {
		return Disk{}
	}
	return Disk{Path: p, TotalBytes: int64(st.Blocks) * int64(st.Bsize), FreeBytes: int64(st.Bavail) * int64(st.Bsize)}
}

var freeRe = regexp.MustCompile(`free percentage: (\d+)%`)

func memoryFreePct(ctx context.Context) int {
	out, err := Run(ctx, 5*time.Second, "/usr/bin/memory_pressure")
	if err != nil {
		return -1
	}
	if m := freeRe.FindStringSubmatch(out); m != nil {
		v, _ := strconv.Atoi(m[1])
		return v
	}
	return -1
}

func detectPower(ctx context.Context) Power {
	p := Power{Source: "unknown"}
	if out, err := Run(ctx, 5*time.Second, "/usr/bin/pmset", "-g", "batt"); err == nil {
		switch {
		case strings.Contains(out, "AC Power"):
			p.Source = "ac"
		case strings.Contains(out, "Battery Power"):
			p.Source = "battery"
		}
	}
	if out, err := Run(ctx, 5*time.Second, "/usr/bin/pmset", "-g", "therm"); err == nil {
		p.ThermalNote = firstLine(out)
	}
	if out, err := Run(ctx, 5*time.Second, "/usr/bin/pmset", "-g", "cap"); err == nil {
		p.HighPowerMode = capListed(out, "powermode")
		p.LowPowerMode = capListed(out, "lowpowermode")
	}
	return p
}

// capListed reports whether a pmset -g cap output lists the capability as its own token.
func capListed(out, name string) bool {
	for _, l := range strings.Split(out, "\n") {
		if strings.EqualFold(strings.TrimSpace(l), name) {
			return true
		}
	}
	return false
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return strings.TrimSpace(s)
}

func (p darwinPlatform) detectSoftware(ctx context.Context) Software {
	var s Software
	if _, err := os.Stat("/opt/homebrew/bin/brew"); err == nil {
		s.Homebrew = true
	}
	for _, cand := range []string{"/opt/homebrew/bin/python3.14", "/opt/homebrew/bin/python3.13", "/opt/homebrew/bin/python3"} {
		if _, err := os.Stat(cand); err == nil {
			if v, err := Run(ctx, 5*time.Second, cand, "--version"); err == nil {
				s.Python = Tool{Path: cand, Version: strings.TrimPrefix(v, "Python ")}
				break
			}
		}
	}
	if dir, err := p.DataDir(); err == nil {
		s.MLX = detectMLX(ctx, filepath.Join(dir, "venv"))
	}
	s.Ollama = detectOllama(ctx)
	return s
}

func detectMLX(ctx context.Context, venv string) MLXStatus {
	st := MLXStatus{VenvPath: venv}
	py := filepath.Join(venv, "bin", "python")
	if _, err := os.Stat(py); err != nil {
		return st
	}
	out, err := Run(ctx, 20*time.Second, py, "-c",
		"import importlib.metadata as m; print(m.version('mlx')); print(m.version('mlx-lm'))")
	if err != nil {
		return st
	}
	lines := strings.Split(out, "\n")
	if len(lines) >= 2 {
		st.MLX, st.MLXLM, st.Ready = strings.TrimSpace(lines[0]), strings.TrimSpace(lines[1]), true
	}
	return st
}

func detectOllama(ctx context.Context) OllamaStatus {
	var o OllamaStatus
	for _, c := range []string{"/usr/local/bin/ollama", "/opt/homebrew/bin/ollama", "/Applications/Ollama.app/Contents/Resources/ollama"} {
		if _, err := os.Stat(c); err == nil {
			o.Installed, o.Path = true, c
			break
		}
	}
	if _, err := os.Stat("/Applications/Ollama.app"); err == nil {
		o.App, o.Installed = true, true
	}
	cl := &http.Client{Timeout: 3 * time.Second}
	get := func(path string, v any) bool {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1:11434"+path, nil)
		resp, err := cl.Do(req)
		if err != nil {
			return false
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			return false
		}
		return json.NewDecoder(http.MaxBytesReader(nil, resp.Body, 4<<20)).Decode(v) == nil
	}
	var ver struct{ Version string }
	if get("/api/version", &ver) {
		o.Running, o.Version, o.Installed = true, ver.Version, true
	}
	var tags struct {
		Models []struct {
			Name    string `json:"name"`
			Size    int64  `json:"size"`
			Details struct {
				ParameterSize     string `json:"parameter_size"`
				QuantizationLevel string `json:"quantization_level"`
			} `json:"details"`
		} `json:"models"`
	}
	if o.Running && get("/api/tags", &tags) {
		for _, m := range tags.Models {
			o.Models = append(o.Models, OllamaModel{Name: m.Name, SizeBytes: m.Size, Params: m.Details.ParameterSize, Quant: m.Details.QuantizationLevel})
		}
	}
	return o
}
