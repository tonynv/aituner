// Package platform isolates everything OS-specific. Only darwin is implemented (SPEC §4); linux returns
// ErrUnsupported so callers report it honestly instead of faking data.
package platform

import (
	"context"
	"errors"
)

var ErrUnsupported = errors.New("platform not yet supported")

// Hardware is the detected machine. Fields that could not be determined are left zero/nil, never guessed.
type Hardware struct {
	Platform   string   `json:"platform"`
	Arch       string   `json:"arch"`
	OS         OS       `json:"os"`
	Model      Model    `json:"model"`
	CPU        CPU      `json:"cpu"`
	GPU        GPU      `json:"gpu"`
	Memory     Memory   `json:"memory"`
	Storage    Disk     `json:"storage"`
	Displays   []string `json:"displays"`
	Power      Power    `json:"power"`
	Software   Software `json:"software"`
	DetectedAt int64    `json:"detected_at"`
}

type OS struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Build   string `json:"build"`
}

type Model struct {
	Name       string `json:"name"`       // e.g. Mac Studio
	Identifier string `json:"identifier"` // e.g. Mac13,1
	Number     string `json:"number"`
}

type CPU struct {
	Chip             string `json:"chip"`
	Cores            int    `json:"cores"`
	PerformanceCores int    `json:"performance_cores"`
	EfficiencyCores  int    `json:"efficiency_cores"`
	L2PerfBytes      int64  `json:"l2_perf_bytes"`
	CacheLineBytes   int    `json:"cache_line_bytes"`
}

type GPU struct {
	Name         string `json:"name"`
	Cores        int    `json:"cores"`
	MetalSupport string `json:"metal_support"`
	Vendor       string `json:"vendor"`
	// MetalWorkingSetBytes is Metal's recommended max working set, known only once MLX is installed.
	MetalWorkingSetBytes int64 `json:"metal_working_set_bytes,omitempty"`
}

type Memory struct {
	TotalBytes int64  `json:"total_bytes"`
	Type       string `json:"type"`
	Unified    bool   `json:"unified"`
	// WiredLimitMB is iogpu.wired_limit_mb; 0 means the macOS default policy is in effect.
	WiredLimitMB    int64 `json:"wired_limit_mb"`
	WiredLimitKnown bool  `json:"wired_limit_known"`
	PressureFreePct int   `json:"pressure_free_pct"`
}

type Disk struct {
	Path       string `json:"path"`
	TotalBytes int64  `json:"total_bytes"`
	FreeBytes  int64  `json:"free_bytes"`
}

type Power struct {
	Source        string `json:"source"` // ac | battery | unknown
	ThermalNote   string `json:"thermal_note"`
	HighPowerMode bool   `json:"high_power_mode_supported"`
	LowPowerMode  bool   `json:"low_power_mode_supported"`
}

type Software struct {
	Homebrew bool         `json:"homebrew"`
	Python   Tool         `json:"python"`
	MLX      MLXStatus    `json:"mlx"`
	Ollama   OllamaStatus `json:"ollama"`
}

type Tool struct {
	Path    string `json:"path"`
	Version string `json:"version"`
}

type MLXStatus struct {
	Ready    bool   `json:"ready"`
	VenvPath string `json:"venv_path"`
	MLX      string `json:"mlx_version"`
	MLXLM    string `json:"mlx_lm_version"`
}

type OllamaStatus struct {
	Installed bool          `json:"installed"`
	Path      string        `json:"path"`
	Running   bool          `json:"running"`
	Version   string        `json:"version"`
	App       bool          `json:"app"` // managed by Ollama.app
	Models    []OllamaModel `json:"models"`
}

type OllamaModel struct {
	Name      string `json:"name"`
	SizeBytes int64  `json:"size_bytes"`
	Params    string `json:"params"`
	Quant     string `json:"quant"`
}

// Platform is the OS seam. Tunables are added in internal/tune to keep this package dependency-free.
type Platform interface {
	Name() string
	Detect(ctx context.Context) (*Hardware, error)
	DataDir() (string, error)
}
