package tune

import (
	"strings"
	"testing"
)

const gib32 = 32 << 30

// The reference machine: 32 GiB, macOS default, Metal allows 26800603136 bytes (measured via MLX).
func refEnv() Env {
	return Env{MemTotalBytes: gib32, MetalWorkingSetBytes: 26800603136, OllamaApp: true}
}

func TestWiredLimitProposalOnReferenceMachine(t *testing.T) {
	p := BuildPlan(refEnv())
	c, ok := p.Find(KeyWiredLimit)
	if !ok {
		t.Fatalf("expected wired limit change; not offered: %+v", p.NotOffered)
	}
	// 32768 MiB - max(5120, 4096) = 27648
	if c.After != "27648" || c.value != 27648 {
		t.Fatalf("after=%s value=%d", c.After, c.value)
	}
	if !strings.Contains(c.Diff, "- iogpu.wired_limit_mb = 0") || !strings.Contains(c.Diff, "+ iogpu.wired_limit_mb = 27648") {
		t.Fatalf("diff:\n%s", c.Diff)
	}
	if !c.NeedsAdmin || !strings.Contains(c.Effect, "will NOT get faster") {
		t.Fatalf("must be honest about effect: %+v", c)
	}
	if per, ok := p.Find(KeyPersist); !ok || per.Requires != KeyWiredLimit {
		t.Fatal("persist option missing or not dependent on the limit")
	}
}

func TestWiredLimitSkippedWhenGainIsTiny(t *testing.T) {
	e := refEnv()
	e.MetalWorkingSetBytes = 27 << 30 // already ~27 GiB
	p := BuildPlan(e)
	if _, ok := p.Find(KeyWiredLimit); ok {
		t.Fatal("must not ask for admin rights for <1 GiB")
	}
	if len(p.NotOffered) == 0 || !strings.Contains(p.NotOffered[0].Reason, "already use") {
		t.Fatalf("reason: %+v", p.NotOffered)
	}
}

func TestWiredLimitSkippedWhenUnmeasurable(t *testing.T) {
	e := refEnv()
	e.MetalWorkingSetBytes = 0
	p := BuildPlan(e)
	if _, ok := p.Find(KeyWiredLimit); ok {
		t.Fatal("must not guess the current limit")
	}
}

func TestWiredLimitLargeMachineReserve(t *testing.T) {
	e := Env{MemTotalBytes: 128 << 30, MetalWorkingSetBytes: 96 << 30}
	c, ok := BuildPlan(e).Find(KeyWiredLimit)
	if !ok || c.value != 128*1024-16*1024 { // 12.5% reserve = 16 GiB
		t.Fatalf("%+v ok=%v", c, ok)
	}
}

func TestExplicitLimitUsedAsCurrent(t *testing.T) {
	e := refEnv()
	e.WiredLimitMB = 27648
	if _, ok := BuildPlan(e).Find(KeyWiredLimit); ok {
		t.Fatal("already at target")
	}
}

func TestPersistNotOfferedWhenInstalled(t *testing.T) {
	e := refEnv()
	e.PersistDaemon = true
	if _, ok := BuildPlan(e).Find(KeyPersist); ok {
		t.Fatal("persist already installed")
	}
}

func TestOllamaGating(t *testing.T) {
	e := refEnv()
	e.OllamaApp = false
	if _, ok := BuildPlan(e).Find(KeyOllamaEnv); ok {
		t.Fatal("no Ollama.app: must not offer")
	}
	e = refEnv()
	e.OllamaFlashAttention, e.OllamaKVCache = "1", "q8_0"
	if _, ok := BuildPlan(e).Find(KeyOllamaEnv); ok {
		t.Fatal("already configured")
	}
	c, ok := BuildPlan(refEnv()).Find(KeyOllamaEnv)
	if !ok || c.NeedsAdmin || !strings.Contains(c.Diff, "+ OLLAMA_KV_CACHE_TYPE = q8_0") {
		t.Fatalf("%+v", c)
	}
}

func TestValidateSelection(t *testing.T) {
	p := BuildPlan(refEnv())
	if err := p.ValidateSelection([]string{KeyWiredLimit, KeyPersist}); err != nil {
		t.Fatal(err)
	}
	if err := p.ValidateSelection([]string{KeyPersist}); err == nil {
		t.Fatal("dependency not enforced")
	}
	if err := p.ValidateSelection([]string{"rm -rf /"}); err == nil {
		t.Fatal("unknown key accepted")
	}
	if err := p.ValidateSelection(nil); err != nil {
		t.Fatalf("empty selection must be allowed: %v", err)
	}
}

func TestOllamaPersistOfferedAfterEnvAndDependsOnIt(t *testing.T) {
	p := BuildPlan(refEnv())
	env, ok1 := p.Find(KeyOllamaEnv)
	per, ok2 := p.Find(KeyOllamaPersist)
	if !ok1 || !ok2 || per.Requires != KeyOllamaEnv || per.NeedsAdmin {
		t.Fatalf("env=%v persist=%+v", env.Key, per)
	}
	// order: env is applied before the agent that re-applies it
	if p.Keys()[len(p.Keys())-1] != KeyOllamaPersist {
		t.Fatalf("order: %v", p.Keys())
	}
	if err := p.ValidateSelection([]string{KeyOllamaPersist}); err == nil {
		t.Fatal("persist without env must be rejected")
	}
}

func TestOllamaPersistStandaloneWhenEnvAlreadySet(t *testing.T) {
	e := refEnv()
	e.OllamaFlashAttention, e.OllamaKVCache = "1", "q8_0"
	p := BuildPlan(e)
	per, ok := p.Find(KeyOllamaPersist)
	if !ok || per.Requires != "" {
		t.Fatalf("persist should stand alone: %+v ok=%v", per, ok)
	}
	e.OllamaAgent = true
	if _, ok := BuildPlan(e).Find(KeyOllamaPersist); ok {
		t.Fatal("already installed")
	}
}
