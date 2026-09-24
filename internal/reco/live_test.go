package reco

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tonynv/aituner/internal/bench"
	"github.com/tonynv/aituner/internal/canirun"
	"github.com/tonynv/aituner/internal/hf"
	"github.com/tonynv/aituner/internal/platform"
)

// Live run against canirun.ai + Hugging Face with this machine's measured numbers. Opt-in: AITUNER_LIVE=1.
func TestLiveRecommend(t *testing.T) {
	if os.Getenv("AITUNER_LIVE") != "1" {
		t.Skip("set AITUNER_LIVE=1")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	dir, _ := platform.Current().DataDir()
	pr, err := bench.MLX{Python: filepath.Join(dir, "venv", "bin", "python"), Dir: filepath.Join(dir, "runtime")}.Probe(ctx)
	if err != nil {
		t.Fatal(err)
	}
	sup := map[string]bool{}
	for _, m := range pr.SupportedModelTypes {
		sup[m] = true
	}
	e := &Engine{CanIRun: canirun.New(), HF: hf.New()}
	out, err := e.Recommend(ctx, Input{
		Hardware:        canirun.Hardware{CPU: &canirun.CPU{Name: "Apple M1 Max", Cores: 10}, RAMGb: 32, GPU: &canirun.GPU{Name: "Apple M1 Max"}},
		BudgetBytes:     int64(27648) << 20,
		GPUBandwidthGBs: 355, MLXGenTPS: 129.6, BenchModelBytes: 1_824_825_759,
		SupportedModelTypes: sup, Unrestricted: true, Installed: []string{"qwen2.5-coder-unrestricted:latest"},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("budget=%.1fGB eff=%.2f refBW=%.0f skipped=%v why=%v warnings=%v", out.BudgetGB, out.Efficiency, out.RefBWGBs, out.Skipped, out.SkippedWhy, out.Warnings)
	for _, cat := range Categories {
		for _, c := range out.Groups[cat.Key] {
			t.Logf("[%s] %-28s %s %-3.0f %-11s %-6s %4.1fGB est=%5.1f tok/s  %s", cat.Key, c.Name, c.Grade, c.Score, c.Fit, c.Runtime, c.SizeGB, c.EstTPS, c.Repo)
			for _, v := range c.Variants {
				t.Logf("      variant: %s %dbit %.1fGB dl=%d fit=%s est=%.1f", v.Repo, v.Bits, v.SizeGB, v.Downloads, v.Fit, v.EstTPS)
			}
		}
	}
	if len(out.Groups["code"]) == 0 {
		t.Fatal("no coding recommendations")
	}
}

func TestLiveKVForRecommendations(t *testing.T) {
	if os.Getenv("AITUNER_LIVE") != "1" {
		t.Skip("set AITUNER_LIVE=1")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	e := &Engine{CanIRun: canirun.New(), HF: hf.New()}
	out, err := e.Recommend(ctx, Input{
		Hardware:    canirun.Hardware{CPU: &canirun.CPU{Name: "Apple M1 Max", Cores: 10}, RAMGb: 32, GPU: &canirun.GPU{Name: "Apple M1 Max"}},
		BudgetBytes: int64(25) << 30, GPUBandwidthGBs: 355, MLXGenTPS: 129.6, BenchModelBytes: 1_824_825_759, Unrestricted: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	known, unknown := 0, 0
	for _, cat := range []string{"code", "chat"} {
		for _, c := range out.Groups[cat] {
			if c.KV == nil {
				t.Fatalf("%s has no KV fit", c.Name)
			}
			if c.KV.Known {
				known++
			} else {
				unknown++
			}
			t.Logf("%-26s weights %.1fGB room %.1fGB  fp16 %7d tok  8bit %7d tok  (model max %d) known=%v %s", c.Name, c.KV.WeightsGB, c.KV.RoomGB, c.KV.TokensF16, c.KV.Tokens8Bit, c.KV.MaxContext, c.KV.Known, c.KV.Reason)
		}
	}
	if known == 0 {
		t.Fatal("no model produced a KV estimate")
	}
	t.Logf("known=%d unknown=%d", known, unknown)
}
