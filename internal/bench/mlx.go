package bench

import (
	"bufio"
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

//go:embed py/mlxbench.py
var mlxScript []byte

// BenchModelMLX is mlx-lm's own default model: small, 4-bit, fixed so runs are comparable over time.
const BenchModelMLX = "mlx-community/Llama-3.2-3B-Instruct-4bit"

type MLX struct {
	Python string // venv python
	Dir    string // where the script is written
}

func (m MLX) script() (string, error) {
	if err := os.MkdirAll(m.Dir, 0o700); err != nil {
		return "", err
	}
	p := filepath.Join(m.Dir, "mlxbench.py")
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, mlxScript, 0o600); err != nil {
		return "", err
	}
	return p, os.Rename(tmp, p)
}

// Probe reports MLX versions and Metal's recommended working set.
type Probe struct {
	MLX                       string   `json:"mlx"`
	MLXLM                     string   `json:"mlx_lm"`
	Device                    string   `json:"device"`
	MaxRecommendedWorkingSetB int64    `json:"max_recommended_working_set_size"`
	MaxBufferLength           int64    `json:"max_buffer_length"`
	SupportedModelTypes       []string `json:"supported_model_types"`
}

type line struct {
	Event  string          `json:"event"`
	Suite  string          `json:"suite"`
	Engine string          `json:"engine"`
	Metric string          `json:"metric"`
	Unit   string          `json:"unit"`
	Value  float64         `json:"value"`
	Trials []float64       `json:"trials"`
	Model  string          `json:"model"`
	I      int             `json:"i"`
	Raw    json.RawMessage `json:"-"`
}

// run executes a subcommand, streaming stderr as info events and returning parsed stdout JSON lines.
func (m MLX) run(ctx context.Context, emit Emit, args ...string) ([]line, [][]byte, error) {
	script, err := m.script()
	if err != nil {
		return nil, nil, err
	}
	cmd := exec.CommandContext(ctx, m.Python, append([]string{script}, args...)...)
	cmd.Env = append(os.Environ(), "PYTHONUNBUFFERED=1", "HF_HUB_DISABLE_TELEMETRY=1", "TOKENIZERS_PARALLELISM=false")
	cmd.WaitDelay = 5 * time.Second
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, nil, err
	}
	var tail bytes.Buffer
	done := make(chan struct{})
	go func() {
		defer close(done)
		sc := bufio.NewScanner(stderr)
		sc.Buffer(make([]byte, 64<<10), 1<<20)
		sc.Split(splitCRLF)
		for sc.Scan() {
			t := strings.TrimSpace(sc.Text())
			if t == "" || strings.HasPrefix(t, "Warning: You are sending unauthenticated") {
				continue
			}
			if tail.Len() < 8<<10 {
				tail.WriteString(t + "\n")
			}
			emit(Event{Level: "info", Message: t})
		}
	}()
	var lines []line
	var raws [][]byte
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 64<<10), 4<<20)
	for sc.Scan() {
		b := append([]byte(nil), sc.Bytes()...)
		var l line
		if err := json.Unmarshal(b, &l); err != nil {
			continue // non-JSON stdout noise from libraries
		}
		lines = append(lines, l)
		raws = append(raws, b)
	}
	io.Copy(io.Discard, stdout)
	<-done
	if err := cmd.Wait(); err != nil {
		if ctx.Err() != nil {
			return nil, nil, ctx.Err()
		}
		return nil, nil, fmt.Errorf("mlx %s failed: %w: %s", args[0], err, strings.TrimSpace(tail.String()))
	}
	return lines, raws, nil
}

func splitCRLF(data []byte, atEOF bool) (int, []byte, error) {
	if atEOF && len(data) == 0 {
		return 0, nil, nil
	}
	if i := bytes.IndexAny(data, "\r\n"); i >= 0 {
		return i + 1, data[:i], nil
	}
	if atEOF {
		return len(data), data, nil
	}
	return 0, nil, nil
}

func (m MLX) Probe(ctx context.Context) (Probe, error) {
	_, raws, err := m.run(ctx, func(Event) {}, "probe")
	if err != nil {
		return Probe{}, err
	}
	for _, r := range raws {
		var p struct {
			Event string `json:"event"`
			Probe
		}
		if json.Unmarshal(r, &p) == nil && p.Event == "probe" {
			return p.Probe, nil
		}
	}
	return Probe{}, errors.New("mlx probe returned no data")
}

func (m MLX) Fetch(ctx context.Context, emit Emit, model string) error {
	_, _, err := m.run(ctx, emit, "fetch", "--model", model)
	return err
}

func metricsFrom(lines []line, versions map[string]string) []Metric {
	var out []Metric
	for _, l := range lines {
		if l.Event != "result" {
			continue
		}
		mt := Metric{Suite: l.Suite, Engine: l.Engine, Name: l.Metric, Unit: l.Unit, Trials: l.Trials, Model: l.Model, Versions: versions}
		mt.Finalize()
		out = append(out, mt)
	}
	return out
}

func (m MLX) GPU(ctx context.Context, emit Emit, trials int, versions map[string]string) ([]Metric, error) {
	lines, _, err := m.run(ctx, emit, "gpu", "--trials", fmt.Sprint(trials))
	if err != nil {
		return nil, err
	}
	for _, l := range lines {
		if l.Event == "trial" {
			emit(Event{Level: "trial", Suite: "gpu", Message: fmt.Sprintf("%s trial %d: %.2f %s", l.Metric, l.I, l.Value, l.Unit)})
		}
	}
	return metricsFrom(lines, versions), nil
}

func (m MLX) LLM(ctx context.Context, emit Emit, model string, trials int, versions map[string]string) ([]Metric, error) {
	lines, _, err := m.run(ctx, emit, "llm", "--model", model, "--trials", fmt.Sprint(trials))
	if err != nil {
		return nil, err
	}
	return metricsFrom(lines, versions), nil
}

// Download saves a repo into dir using the given file patterns. Progress is observed by the caller from the
// directory size; the subprocess only reports success or failure.
func (m MLX) Download(ctx context.Context, repo, dir string, allow, ignore []string) error {
	a, _ := json.Marshal(allow)
	i, _ := json.Marshal(ignore)
	_, _, err := m.run(ctx, func(Event) {}, "download", "--model", repo, "--dir", dir, "--allow", string(a), "--ignore", string(i))
	return err
}
