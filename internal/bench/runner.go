package bench

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/tonynv/aituner/internal/platform"
)

type Options struct {
	MLX         MLX
	Ollama      *Ollama // nil = no Ollama suite
	Threads     int     // CPU threads for the memory test (performance cores)
	Trials      int
	FetchModels bool // download benchmark models if missing
}

type Result struct {
	Metrics  []Metric `json:"metrics"`
	Skipped  []string `json:"skipped"`
	Warnings []string `json:"warnings"`
	Probe    Probe    `json:"probe"`
}

// Run executes every suite in order. Core suites (memory, GPU, MLX LLM) must succeed; the Ollama suite is
// optional and is reported as skipped with its reason if Ollama is absent or fails.
func Run(ctx context.Context, o Options, emit Emit) (Result, error) {
	var res Result
	step := 0
	const total = 8.0
	prog := func(msg string) {
		emit(Event{Level: "info", Message: msg, Progress: float64(step) / total})
		step++
	}

	// keep the Mac awake for the duration (idle sleep would corrupt timing); released when we finish
	if cf := exec.CommandContext(ctx, "/usr/bin/caffeinate", "-i", "-w", strconv.Itoa(os.Getpid())); cf.Start() == nil {
		defer func() { _ = cf.Process.Kill(); _ = cf.Wait() }()
	}

	pre := platform.CheckHealth(ctx)
	for _, w := range pre.Warnings("Before the run") {
		res.Warnings = append(res.Warnings, w)
		emit(Event{Level: "warn", Message: w})
	}

	prog("Preparing: freeing GPU memory held by idle models")
	if o.Ollama != nil && o.Ollama.Running(ctx) {
		names, err := o.Ollama.UnloadAll(ctx)
		if err != nil {
			emit(Event{Level: "warn", Message: "could not fully unload Ollama models: " + err.Error()})
		} else if len(names) > 0 {
			emit(Event{Level: "info", Message: "unloaded from Ollama (reloads on demand): " + strings.Join(names, ", ")})
		}
	}
	probe, err := o.MLX.Probe(ctx)
	if err != nil {
		return res, fmt.Errorf("MLX runtime not ready: %w", err)
	}
	res.Probe = probe
	versions := map[string]string{"mlx": probe.MLX, "mlx_lm": probe.MLXLM}
	emit(Event{Level: "info", Message: fmt.Sprintf("MLX %s / mlx-lm %s on %s", probe.MLX, probe.MLXLM, probe.Device)})

	prog("CPU memory bandwidth")
	cp, rd, err := MemBandwidth(ctx, o.Threads, o.Trials)
	if err != nil {
		return res, err
	}
	for _, m := range []Metric{
		{Suite: "memory", Engine: "cpu", Name: "copy_bandwidth", Unit: "GB/s", Trials: cp},
		{Suite: "memory", Engine: "cpu", Name: "read_bandwidth", Unit: "GB/s", Trials: rd},
	} {
		m.Finalize()
		res.Metrics = append(res.Metrics, m)
		emit(Event{Level: "trial", Suite: "memory", Message: fmt.Sprintf("%s: %.0f GB/s (median of %d)", m.Name, m.Value, len(m.Trials))})
	}

	prog("GPU compute and memory bandwidth (Metal via MLX)")
	gm, err := o.MLX.GPU(ctx, emit, o.Trials, versions)
	if err != nil {
		return res, err
	}
	res.Metrics = append(res.Metrics, gm...)

	prog("Fetching MLX benchmark model")
	if o.FetchModels {
		if err := o.MLX.Fetch(ctx, emit, BenchModelMLX); err != nil {
			return res, err
		}
	}
	prog("LLM inference: MLX")
	lm, err := o.MLX.LLM(ctx, emit, BenchModelMLX, o.Trials, versions)
	if err != nil {
		return res, err
	}
	res.Metrics = append(res.Metrics, lm...)

	prog("LLM inference: prompt-length sweep and long-context decode (MLX)")
	sw, err := o.MLX.Sweep(ctx, emit, BenchModelMLX, 3, versions)
	if err != nil {
		return res, err
	}
	res.Metrics = append(res.Metrics, sw...)

	prog("LLM inference: Ollama")
	if o.Ollama == nil || !o.Ollama.Running(ctx) {
		res.Skipped = append(res.Skipped, "ollama: not running")
	} else if om, err := o.runOllama(ctx, emit, versions); err != nil {
		res.Skipped = append(res.Skipped, "ollama: "+err.Error())
		emit(Event{Level: "warn", Message: "Ollama suite skipped: " + err.Error()})
	} else {
		res.Metrics = append(res.Metrics, om...)
	}
	for _, m := range res.Metrics {
		if u := UnstablePct(m.Trials); u > UnstableThresholdPct && !isInfo(m) && !m.IsSeries() {
			w := fmt.Sprintf("%s varied by ±%.0f%% between trials (something else may have used the machine); the median is reported, but treat this number with care.", m.Key(), u)
			res.Warnings = append(res.Warnings, w)
			emit(Event{Level: "warn", Message: w})
		}
	}
	// throttling that started during the run would silently understate every number, so look again
	if post := platform.CheckHealth(ctx); post.ThermalWarning && !pre.ThermalWarning {
		w := "Thermal throttling began during the run (" + post.ThermalNote + "); results may understate this machine."
		res.Warnings = append(res.Warnings, w)
		emit(Event{Level: "warn", Message: w})
	}
	emit(Event{Level: "info", Message: "Benchmark complete", Progress: 1})
	return res, nil
}

func (o Options) runOllama(ctx context.Context, emit Emit, versions map[string]string) ([]Metric, error) {
	v := map[string]string{"ollama": o.Ollama.Version(ctx)}
	for k, x := range versions {
		v[k] = x
	}
	ok, err := o.Ollama.Has(ctx, BenchModelOllama)
	if err != nil {
		return nil, err
	}
	if !ok {
		if !o.FetchModels {
			return nil, fmt.Errorf("%s not installed", BenchModelOllama)
		}
		emit(Event{Level: "info", Message: "pulling " + BenchModelOllama})
		last := -1
		if err := o.Ollama.Pull(ctx, BenchModelOllama, func(status string, f float64) {
			if p := int(f * 10); p != last {
				last = p
				emit(Event{Level: "info", Message: fmt.Sprintf("ollama pull: %s %.0f%%", status, f*100)})
			}
		}); err != nil {
			return nil, err
		}
	}
	m, err := o.Ollama.Bench(ctx, emit, BenchModelOllama, o.Trials, v)
	if _, uerr := o.Ollama.UnloadAll(ctx); uerr != nil {
		emit(Event{Level: "warn", Message: "unload after benchmark: " + uerr.Error()})
	}
	return m, err
}
