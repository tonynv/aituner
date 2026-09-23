package canirun

import (
	"context"
	"os"
	"testing"
	"time"
)

// Live contract test against the real service (opt-in: network).
func TestLiveRecommendContract(t *testing.T) {
	if os.Getenv("AITUNER_LIVE") != "1" {
		t.Skip("set AITUNER_LIVE=1")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c := New()
	r, err := c.Recommend(ctx, Hardware{CPU: &CPU{Name: "Apple M1 Max", Cores: 10}, RAMGb: 32, GPU: &GPU{Name: "Apple M1 Max"}}, "code", 5)
	if err != nil {
		t.Fatal(err)
	}
	if r.Hardware.Kind != "apple-silicon" || r.Hardware.MemoryBandwidthGbps <= 0 || len(r.Recommendations) == 0 {
		t.Fatalf("unexpected: %+v", r.Hardware)
	}
	ms, err := c.Models(ctx)
	if err != nil || len(ms) < 50 {
		t.Fatalf("models: %d %v", len(ms), err)
	}
	if _, err := c.Recommend(ctx, Hardware{RAMGb: 32}, "", 99); err == nil {
		t.Fatal("limit>25 must be rejected client-side")
	}
}
