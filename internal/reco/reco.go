// Package reco builds the post-tuning recommendations (SPEC §8). It only runs after the second benchmark:
// inputs are the tuned memory budget and measured throughput, so nothing here is a spec-sheet guess.
package reco

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/tonynv/aituner/internal/canirun"
	"github.com/tonynv/aituner/internal/hf"
)

const (
	cacheTTL      = 6 * time.Hour
	perCategory   = 6
	maxVariants   = 3
	minHeadroomGB = 1.5
	fitComfort    = 0.85
)

// Categories the user cares about, mapped to canirun.ai use cases.
var Categories = []struct{ Key, UseCase, Title string }{
	{"code", "code", "Coding"},
	{"chat", "chat", "Chat and reasoning"},
	{"image", "image", "Image generation"},
}

// Cache persists remote lookups (backed by the datastore). Errors are non-fatal.
type Cache interface {
	Get(ctx context.Context, key string) (body []byte, fetchedAt time.Time, ok bool)
	Put(ctx context.Context, key string, body []byte)
}

type Input struct {
	Hardware        canirun.Hardware
	BudgetBytes     int64   // GPU-usable memory after tuning
	GPUBandwidthGBs float64 // measured (MLX)
	MLXGenTPS       float64 // measured generation tok/s on the benchmark model
	BenchModelBytes int64   // size of that benchmark model
	Unrestricted    bool
	Installed       []string // installed Ollama model names, for marking
	// SupportedModelTypes is what the installed mlx-lm can load (from the MLX probe). Empty = unchecked.
	SupportedModelTypes map[string]bool
}

type Variant struct {
	Repo         string   `json:"repo"`
	Bits         int      `json:"bits"`
	SizeGB       float64  `json:"size_gb"`
	Downloads    int      `json:"downloads"`
	License      string   `json:"license"`
	LastModified string   `json:"last_modified"`
	Fit          string   `json:"fit"`
	EstTPS       float64  `json:"est_tps"`
	Warnings     []string `json:"warnings"`
	Run          string   `json:"run"`
}

type Candidate struct {
	Category  string    `json:"category"`
	ModelID   string    `json:"model_id"`
	Name      string    `json:"name"`
	Provider  string    `json:"provider"`
	ParamsB   float64   `json:"params_b"`
	ActiveB   float64   `json:"active_b,omitempty"`
	Grade     string    `json:"grade"`
	Score     float64   `json:"score"`
	RefTPS    float64   `json:"canirun_est_tps"`
	Runtime   string    `json:"runtime"` // mlx | mflux
	Repo      string    `json:"repo,omitempty"`
	Bits      int       `json:"bits,omitempty"`
	SizeGB    float64   `json:"size_gb"`
	License   string    `json:"license,omitempty"`
	Fit       string    `json:"fit"` // comfortable | tight
	EstTPS    float64   `json:"est_tps,omitempty"`
	Run       string    `json:"run,omitempty"`
	Notes     []string  `json:"notes,omitempty"`
	Installed bool      `json:"installed"`
	Variants  []Variant `json:"variants,omitempty"`
	SourceURL string    `json:"source_url"`
}

type Output struct {
	Groups     map[string][]Candidate `json:"groups"`
	BudgetGB   float64                `json:"budget_gb"`
	Efficiency float64                `json:"efficiency"`
	Skipped    map[string]int         `json:"skipped"`
	SkippedWhy map[string]int         `json:"skipped_why"`
	Warnings   []string               `json:"warnings"`
	RefBWGBs   float64                `json:"canirun_reference_bandwidth_gbs"`
	Sources    []string               `json:"sources"`
}

type Engine struct {
	CanIRun *canirun.Client
	HF      *hf.Client
	Cache   Cache
}

func cached[T any](ctx context.Context, c Cache, key string, fetch func() (T, error)) (T, error) {
	var zero T
	if c != nil {
		if b, at, ok := c.Get(ctx, key); ok && time.Since(at) < cacheTTL {
			var v T
			if json.Unmarshal(b, &v) == nil {
				return v, nil
			}
		}
	}
	v, err := fetch()
	if err != nil {
		// serve stale cache rather than nothing, if we have it
		if c != nil {
			if b, _, ok := c.Get(ctx, key); ok {
				var s T
				if json.Unmarshal(b, &s) == nil {
					return s, nil
				}
			}
		}
		return zero, err
	}
	if c != nil {
		if b, err := json.Marshal(v); err == nil {
			c.Put(ctx, key, b)
		}
	}
	return v, nil
}

func (e *Engine) search(ctx context.Context, o hf.SearchOpts) ([]hf.Repo, error) {
	return cached(ctx, e.Cache, fmt.Sprintf("hf.search|%s|%s|%s|%d", o.Author, o.Search, o.Filter, o.Limit), func() ([]hf.Repo, error) { return e.HF.Search(ctx, o) })
}

func (e *Engine) info(ctx context.Context, id string) (hf.Info, error) {
	return cached(ctx, e.Cache, "hf.info|"+id, func() (hf.Info, error) { return e.HF.Info(ctx, id) })
}

// Estimate predicts generation tok/s from measured bandwidth: tok/s = eff * BW / bytes read per token,
// where eff is calibrated so the benchmark model reproduces its own measured speed.
func Estimate(in Input, sizeBytes int64, activeFrac float64) (tps, eff float64) {
	if in.GPUBandwidthGBs <= 0 || in.MLXGenTPS <= 0 || in.BenchModelBytes <= 0 || sizeBytes <= 0 {
		return 0, 0
	}
	eff = in.MLXGenTPS * float64(in.BenchModelBytes) / (in.GPUBandwidthGBs * 1e9)
	eff = math.Min(1, math.Max(0.1, eff))
	bytesPerTok := float64(sizeBytes) * activeFrac
	return eff * in.GPUBandwidthGBs * 1e9 / bytesPerTok, eff
}

// FitStatus classifies a model against the budget, including KV-cache/runtime headroom.
func FitStatus(sizeBytes, budget int64) string {
	need := float64(sizeBytes) + math.Max(minHeadroomGB*(1<<30), 0.08*float64(sizeBytes))
	switch {
	case need <= fitComfort*float64(budget):
		return "comfortable"
	case need <= float64(budget):
		return "tight"
	}
	return "too_large"
}

func (e *Engine) Recommend(ctx context.Context, in Input) (*Output, error) {
	if in.BudgetBytes <= 0 {
		return nil, errors.New("memory budget unknown")
	}
	_, eff := Estimate(in, in.BenchModelBytes, 1)
	out := &Output{
		Groups: map[string][]Candidate{}, BudgetGB: float64(in.BudgetBytes) / (1 << 30), Efficiency: eff,
		Skipped: map[string]int{}, SkippedWhy: map[string]int{},
		Sources: []string{"https://www.canirun.ai (fit grades)", "https://huggingface.co (MLX builds, sizes, licences)", "aituner benchmark (speed calibration)"},
	}
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 4)
	var firstErr error
	for _, cat := range Categories {
		cat := cat
		wg.Add(1)
		go func() {
			defer wg.Done()
			rr, err := cached(ctx, e.Cache, fmt.Sprintf("canirun|%s|%d|%s", cat.UseCase, in.Hardware.RAMGb, hwName(in.Hardware)), func() (*canirun.RecommendResponse, error) {
				return e.CanIRun.Recommend(ctx, in.Hardware, cat.UseCase, 25)
			})
			if err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = fmt.Errorf("canirun.ai (%s): %w", cat.UseCase, err)
				}
				mu.Unlock()
				return
			}
			mu.Lock()
			out.RefBWGBs = rr.Hardware.MemoryBandwidthGbps
			mu.Unlock()
			var cands []Candidate
			var cw sync.WaitGroup
			var cm sync.Mutex
			skipped := 0
			for _, rec := range rr.Recommendations {
				rec := rec
				cw.Add(1)
				sem <- struct{}{}
				go func() {
					defer cw.Done()
					defer func() { <-sem }()
					c, why := e.resolve(ctx, in, cat.Key, rec)
					cm.Lock()
					defer cm.Unlock()
					if why == "" {
						cands = append(cands, c)
					} else {
						skipped++
						mu.Lock()
						out.SkippedWhy[why]++
						mu.Unlock()
					}
				}()
			}
			cw.Wait()
			sort.SliceStable(cands, func(i, j int) bool {
				if (cands[i].Fit == "comfortable") != (cands[j].Fit == "comfortable") {
					return cands[i].Fit == "comfortable"
				}
				return cands[i].Score > cands[j].Score
			})
			if len(cands) > perCategory {
				cands = cands[:perCategory]
			}
			if in.Unrestricted {
				var vw sync.WaitGroup
				for i := range cands {
					if cands[i].Runtime != "mlx" {
						continue
					}
					i := i
					vw.Add(1)
					sem <- struct{}{}
					go func() {
						defer vw.Done()
						defer func() { <-sem }()
						cands[i].Variants = e.variants(ctx, in, cands[i])
					}()
				}
				vw.Wait()
			}
			mu.Lock()
			out.Groups[cat.Key] = cands
			out.Skipped[cat.Key] = skipped
			mu.Unlock()
		}()
	}
	wg.Wait()
	if firstErr != nil && len(out.Groups) == 0 {
		return nil, firstErr
	}
	if firstErr != nil {
		out.Warnings = append(out.Warnings, firstErr.Error())
	}
	return out, nil
}

func hwName(h canirun.Hardware) string {
	if h.GPU != nil {
		return h.GPU.Name
	}
	return ""
}

func markInstalled(installed []string, base string) bool {
	nb := norm(base)
	for _, n := range installed {
		if nb != "" && strings.Contains(norm(n), nb) {
			return true
		}
	}
	return false
}

// resolve turns a canirun.ai recommendation into a runnable macOS candidate, or reports it unusable.
func (e *Engine) resolve(ctx context.Context, in Input, cat string, rec canirun.Recommendation) (Candidate, string) {
	c := Candidate{
		Category: cat, ModelID: rec.ModelID, Name: rec.Name, Provider: rec.Provider, ParamsB: rec.ParamsBillions,
		Grade: rec.Grade, Score: rec.Score, RefTPS: rec.EstTPS, SourceURL: rec.URL, Installed: markInstalled(in.Installed, BaseName(rec.URL)),
	}
	if cat == "image" {
		return e.resolveImage(in, c, rec)
	}
	base := BaseName(rec.URL)
	if base == "" {
		return c, "no usable model reference"
	}
	repos, err := e.search(ctx, hf.SearchOpts{Author: "mlx-community", Search: base, Limit: 40})
	if err != nil {
		return c, "Hugging Face lookup failed"
	}
	quants := MatchMLX(base, repos)
	if len(quants) == 0 {
		return c, "no MLX build in mlx-community"
	}
	quants = preferQuants(quants, in.BudgetBytes, rec.ParamsBillions)
	active := ActiveParamsBillions(rec.ModelID + " " + rec.Name + " " + base)
	frac := 1.0
	if active > 0 && rec.ParamsBillions > 0 {
		frac = math.Min(1, active/rec.ParamsBillions)
		c.ActiveB = active
	}
	tries := 0
	why := "does not fit the tuned memory budget"
	for _, q := range quants {
		if tries++; tries > 3 {
			break
		}
		inf, err := e.info(ctx, q.Repo.ID)
		if err != nil || inf.PickleOnly() || inf.SafetensorsBytes() == 0 || inf.IsGated() {
			continue
		}
		if !in.supports(inf.Config.ModelType) {
			return c, "architecture not supported by installed mlx-lm"
		}
		size := inf.SafetensorsBytes()
		fit := FitStatus(size, in.BudgetBytes)
		if fit == "too_large" {
			continue
		}
		c.Runtime, c.Repo, c.Bits, c.SizeGB, c.Fit, c.License = "mlx", q.Repo.ID, q.Bits, float64(size)/1e9, fit, inf.License()
		if tps, _ := Estimate(in, size, frac); tps > 0 {
			c.EstTPS = tps
		}
		c.Run = "mlx_lm.chat --model " + q.Repo.ID
		if active > 0 {
			c.Notes = append(c.Notes, "Mixture-of-experts: only a fraction of weights is read per token, so it can be far faster than its size suggests (estimate ignores routing overhead).")
		}
		if c.Installed {
			c.Notes = append(c.Notes, "You already have a build of this model in Ollama.")
		}
		return c, ""
	}
	return c, why
}

func (e *Engine) resolveImage(in Input, c Candidate, rec canirun.Recommendation) (Candidate, string) {
	id := strings.ToLower(rec.ModelID)
	supported := strings.Contains(id, "z-image") || strings.Contains(id, "flux") || strings.Contains(id, "qwen-image")
	if !supported {
		return c, "no verified Apple-Silicon image runtime (mflux) for this model"
	}
	size := int64(rec.VRAMRequiredGb * 1e9)
	fit := FitStatus(size, in.BudgetBytes)
	if fit == "too_large" {
		return c, "does not fit the tuned memory budget"
	}
	c.Runtime, c.SizeGB, c.Fit = "mflux", rec.VRAMRequiredGb, fit
	c.Repo = BaseName(rec.URL)
	c.Run = "uv tool install --upgrade mflux"
	if strings.Contains(id, "z-image-turbo") {
		c.Run += "\nmflux-generate-z-image-turbo --prompt \"...\" --steps 9 -q 8"
	}
	c.Notes = append(c.Notes, "Runs on Apple Silicon via mflux (MLX). See github.com/filipstrand/mflux for this model's command and -q quantisation.")
	if v, ok := licenseNote(rec.ModelID); ok {
		c.Notes = append(c.Notes, v)
	}
	return c, ""
}

// licenseNote flags licences that restrict commercial use where canirun.ai's model list says so.
func licenseNote(id string) (string, bool) {
	if strings.Contains(id, "flux2-dev") || strings.Contains(id, "klein-9b") {
		return "FLUX non-commercial licence: personal use only.", true
	}
	return "", false
}

// variants finds community abliterated/uncensored MLX builds of the same base model that fit the budget.
func (e *Engine) variants(ctx context.Context, in Input, c Candidate) []Variant {
	base := BaseName(c.SourceURL)
	seen := map[string]hf.Repo{}
	for _, term := range []string{"abliterated", "uncensored"} {
		rs, err := e.search(ctx, hf.SearchOpts{Search: base + " " + term, Filter: "mlx", Limit: 30})
		if err != nil {
			continue
		}
		for _, r := range rs {
			if IsUnrestrictedVariant(base, r) {
				seen[r.ID] = r
			}
		}
	}
	list := make([]hf.Repo, 0, len(seen))
	for _, r := range seen {
		list = append(list, r)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Downloads > list[j].Downloads })
	active := c.ActiveB
	frac := 1.0
	if active > 0 && c.ParamsB > 0 {
		frac = math.Min(1, active/c.ParamsB)
	}
	var out []Variant
	for _, r := range list {
		if len(out) >= maxVariants {
			break
		}
		inf, err := e.info(ctx, r.ID)
		if err != nil || inf.PickleOnly() || inf.SafetensorsBytes() == 0 || inf.IsGated() || !in.supports(inf.Config.ModelType) {
			continue
		}
		size := inf.SafetensorsBytes()
		fit := FitStatus(size, in.BudgetBytes)
		if fit == "too_large" {
			continue
		}
		v := Variant{Repo: r.ID, Bits: bitsFromName(r.Name()), SizeGB: float64(size) / 1e9, Downloads: r.Downloads, License: inf.License(),
			LastModified: inf.LastModified, Fit: fit, Run: "mlx_lm.chat --model " + r.ID,
			Warnings: []string{
				"Community-modified weights (safety tuning removed) from " + r.Author() + ": unaudited, use at your own discretion.",
				"Safetensors only; loaded without remote code.",
			}}
		if tps, _ := Estimate(in, size, frac); tps > 0 {
			v.EstTPS = tps
		}
		if strings.Contains(strings.ToLower(r.Name()), "vlm") {
			v.Warnings = append(v.Warnings, "Built for mlx-vlm (vision-language); it may not load with mlx_lm.chat.")
		}
		out = append(out, v)
	}
	return out
}

func (in Input) supports(modelType string) bool {
	if len(in.SupportedModelTypes) == 0 {
		return true
	}
	return in.SupportedModelTypes[modelType]
}

// preferQuants orders quantisations by usefulness on a unified-memory Mac: 4-bit is the speed/quality sweet
// spot; 8-bit is preferred when the model is small enough that memory is not the constraint; other widths
// follow; full-precision bf16 is last because it costs 3-4x the memory and speed for little practical gain.
func preferQuants(qs []MLXQuant, budget int64, paramsB float64) []MLXQuant {
	rank := func(bits int) int {
		switch bits {
		case 4:
			return 1
		case 8:
			return 2
		case 6:
			return 3
		case 5:
			return 4
		case 3:
			return 5
		case 16:
			return 9
		}
		return 10
	}
	// small model (8-bit under 30% of budget): 8-bit first
	if paramsB > 0 && paramsB*1e9 < 0.3*float64(budget) {
		rank8 := rank
		rank = func(bits int) int {
			if bits == 8 {
				return 0
			}
			return rank8(bits)
		}
	}
	out := append([]MLXQuant(nil), qs...)
	sort.SliceStable(out, func(i, j int) bool {
		ri, rj := rank(out[i].Bits), rank(out[j].Bits)
		if ri != rj {
			return ri < rj
		}
		return out[i].Repo.Downloads > out[j].Repo.Downloads
	})
	return out
}
