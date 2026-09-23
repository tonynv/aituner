package bench

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// BenchModelOllama is the fixed Ollama benchmark model (comparable across runs and machines).
const BenchModelOllama = "llama3.2:3b"

type Ollama struct {
	URL    string // e.g. http://127.0.0.1:11434
	Client *http.Client
}

func NewOllama() *Ollama {
	return &Ollama{URL: "http://127.0.0.1:11434", Client: &http.Client{}}
}

func (o *Ollama) do(ctx context.Context, method, path string, body any, timeout time.Duration) (*http.Response, context.CancelFunc, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, o.URL+path, rd)
	if err != nil {
		cancel()
		return nil, nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := o.Client.Do(req)
	if err != nil {
		cancel()
		return nil, nil, err
	}
	return resp, cancel, nil
}

func (o *Ollama) getJSON(ctx context.Context, path string, v any) error {
	resp, cancel, err := o.do(ctx, http.MethodGet, path, nil, 10*time.Second)
	if err != nil {
		return err
	}
	defer cancel()
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("ollama %s: %s", path, resp.Status)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(v)
}

func (o *Ollama) Running(ctx context.Context) bool {
	var v struct{ Version string }
	return o.getJSON(ctx, "/api/version", &v) == nil
}

func (o *Ollama) Version(ctx context.Context) string {
	var v struct{ Version string }
	_ = o.getJSON(ctx, "/api/version", &v)
	return v.Version
}

// UnloadAll evicts loaded models so they do not hold GPU memory or compete during measurement.
// It returns the names it unloaded; Ollama reloads them on demand.
func (o *Ollama) UnloadAll(ctx context.Context) ([]string, error) {
	var ps struct {
		Models []struct{ Name string } `json:"models"`
	}
	if err := o.getJSON(ctx, "/api/ps", &ps); err != nil {
		return nil, err
	}
	var names []string
	for _, m := range ps.Models {
		resp, cancel, err := o.do(ctx, http.MethodPost, "/api/generate", map[string]any{"model": m.Name, "keep_alive": 0}, 60*time.Second)
		if err != nil {
			return names, err
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		cancel()
		names = append(names, m.Name)
	}
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if err := o.getJSON(ctx, "/api/ps", &ps); err == nil && len(ps.Models) == 0 {
			return names, nil
		}
		select {
		case <-ctx.Done():
			return names, ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	return names, errors.New("ollama models did not unload within 20s")
}

func (o *Ollama) Has(ctx context.Context, model string) (bool, error) {
	var tags struct {
		Models []struct{ Name, Model string } `json:"models"`
	}
	if err := o.getJSON(ctx, "/api/tags", &tags); err != nil {
		return false, err
	}
	for _, m := range tags.Models {
		if m.Name == model || m.Model == model || strings.TrimSuffix(m.Name, ":latest") == model {
			return true, nil
		}
	}
	return false, nil
}

// Pull downloads a model, reporting progress (0..1) through cb.
func (o *Ollama) Pull(ctx context.Context, model string, cb func(status string, frac float64)) error {
	resp, cancel, err := o.do(ctx, http.MethodPost, "/api/pull", map[string]any{"model": model, "stream": true}, 2*time.Hour)
	if err != nil {
		return err
	}
	defer cancel()
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("ollama pull: %s", resp.Status)
	}
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		var m struct {
			Status    string `json:"status"`
			Total     int64  `json:"total"`
			Completed int64  `json:"completed"`
			Error     string `json:"error"`
		}
		if json.Unmarshal(sc.Bytes(), &m) != nil {
			continue
		}
		if m.Error != "" {
			return errors.New("ollama pull: " + m.Error)
		}
		frac := 0.0
		if m.Total > 0 {
			frac = float64(m.Completed) / float64(m.Total)
		}
		cb(m.Status, frac)
	}
	return sc.Err()
}

type genStats struct {
	PromptEvalCount    int    `json:"prompt_eval_count"`
	PromptEvalDuration int64  `json:"prompt_eval_duration"`
	EvalCount          int    `json:"eval_count"`
	EvalDuration       int64  `json:"eval_duration"`
	Error              string `json:"error"`
}

func (o *Ollama) generate(ctx context.Context, model, prompt string, numPredict int) (genStats, error) {
	resp, cancel, err := o.do(ctx, http.MethodPost, "/api/generate", map[string]any{
		"model": model, "prompt": prompt, "stream": false, "keep_alive": "10m",
		"options": map[string]any{"num_predict": numPredict, "temperature": 0, "seed": 1, "num_ctx": 4096},
	}, 10*time.Minute)
	if err != nil {
		return genStats{}, err
	}
	defer cancel()
	defer resp.Body.Close()
	var s genStats
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&s); err != nil {
		return s, err
	}
	if s.Error != "" {
		return s, errors.New(s.Error)
	}
	return s, nil
}

// prompt builds ~1000 tokens of deterministic text. A per-trial marker at the START defeats prefix caching
// so prompt-processing speed is really measured.
func benchPrompt(marker int) string {
	const sentence = "The compiler walks the syntax tree, resolves each symbol against its scope, and lowers the result into a linear intermediate form. "
	return fmt.Sprintf("[trial %d] ", marker) + strings.Repeat(sentence, 45) + "\nSummarise the text above."
}

// Bench measures prompt and generation tok/s from Ollama's own eval counters.
func (o *Ollama) Bench(ctx context.Context, emit Emit, model string, trials int, versions map[string]string) ([]Metric, error) {
	if _, err := o.generate(ctx, model, benchPrompt(0), 8); err != nil { // warmup / load
		return nil, fmt.Errorf("ollama warmup: %w", err)
	}
	var pp, tg []float64
	for i := 1; i <= trials; i++ {
		s, err := o.generate(ctx, model, benchPrompt(i), 128)
		if err != nil {
			return nil, err
		}
		if s.PromptEvalDuration <= 0 || s.EvalDuration <= 0 || s.EvalCount == 0 {
			return nil, errors.New("ollama returned no timing data")
		}
		p := float64(s.PromptEvalCount) / (float64(s.PromptEvalDuration) / 1e9)
		g := float64(s.EvalCount) / (float64(s.EvalDuration) / 1e9)
		pp, tg = append(pp, p), append(tg, g)
		emit(Event{Level: "trial", Suite: "llm", Message: fmt.Sprintf("ollama trial %d: prompt %.0f tok/s, generation %.1f tok/s", i, p, g)})
	}
	mk := func(name string, v []float64) Metric {
		m := Metric{Suite: "llm", Engine: "ollama", Name: name, Unit: "tok/s", Trials: v, Model: model, Versions: versions}
		m.Finalize()
		return m
	}
	return []Metric{mk("prompt_tps", pp), mk("generation_tps", tg)}, nil
}
