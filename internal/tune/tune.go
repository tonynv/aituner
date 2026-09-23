// Package tune plans, applies and reverts system changes that can improve local-AI performance (SPEC §7).
//
// Security property: callers (the HTTP layer) may only name changes by key. Every value that reaches a
// privileged command is computed here from measured facts, and privileged scripts are built only from
// constants and integers (see admin_darwin.go).
package tune

import (
	"context"
	"errors"
	"fmt"
)

var ErrUnsupported = errors.New("tuning not supported on this platform")

// Env is what planning needs to know. It is measured, never assumed.
type Env struct {
	MemTotalBytes        int64
	WiredLimitMB         int64 // current iogpu.wired_limit_mb, 0 = macOS default policy
	MetalWorkingSetBytes int64 // Metal max_recommended_working_set_size at the current limit (from MLX probe)
	OllamaApp            bool
	OllamaRunning        bool
	OllamaFlashAttention string // current launchctl value, "" if unset
	OllamaKVCache        string
	PersistDaemon        bool // ai.aituner.wiredlimit LaunchDaemon present
}

// Change is one proposed modification, shown to the user as a diff before anything is applied.
type Change struct {
	Key        string `json:"key"`
	Title      string `json:"title"`
	Why        string `json:"why"`
	Effect     string `json:"effect"` // honest expectation, including when it will NOT speed anything up
	Before     string `json:"before"`
	After      string `json:"after"`
	NeedsAdmin bool   `json:"needs_admin"`
	Persistent string `json:"persistence"`
	Requires   string `json:"requires,omitempty"` // key of a change this depends on
	Diff       string `json:"diff"`
	value      int64  // internal target (never from the network)
}

// NotOffered records why a tunable is absent, so the UI can say so rather than hide it.
type NotOffered struct {
	Key    string `json:"key"`
	Title  string `json:"title"`
	Reason string `json:"reason"`
}

type Plan struct {
	Changes    []Change     `json:"changes"`
	NotOffered []NotOffered `json:"not_offered"`
}

func (p Plan) Find(key string) (Change, bool) {
	for _, c := range p.Changes {
		if c.Key == key {
			return c, true
		}
	}
	return Change{}, false
}

func diff(key, before, after string) string {
	return fmt.Sprintf("- %s = %s\n+ %s = %s", key, before, key, after)
}

const (
	KeyWiredLimit = "gpu.wired_limit"
	KeyPersist    = "gpu.wired_limit.persist"
	KeyOllamaEnv  = "ollama.env"
)

func gib(bytes int64) string { return fmt.Sprintf("%.1f GiB", float64(bytes)/(1<<30)) }

func mibToGiB(mb int64) string { return gib(mb << 20) }

// BuildPlan proposes changes from the measured environment. A tunable that would not matter on this
// machine is reported in NotOffered with the measured reason instead of being pushed on the user.
func BuildPlan(env Env) Plan {
	var p Plan
	p.planWiredLimit(env)
	p.planOllama(env)
	return p
}

// minGainMiB: a wired-limit change smaller than this is not worth asking for admin rights.
const minGainMiB = 1024

func (p *Plan) planWiredLimit(env Env) {
	title := "GPU wired memory limit"
	if env.MemTotalBytes == 0 {
		p.NotOffered = append(p.NotOffered, NotOffered{KeyWiredLimit, title, "total memory unknown"})
		return
	}
	totalMiB := env.MemTotalBytes >> 20
	reserve := int64(5 << 10) // keep at least 5 GiB for macOS and apps...
	if r := totalMiB / 8; r > reserve {
		reserve = r // ...or 12.5% of RAM on bigger machines
	}
	target := totalMiB - reserve

	var current int64 // effective GPU-usable MiB right now
	before := "0 (macOS default)"
	switch {
	case env.WiredLimitMB > 0:
		current = env.WiredLimitMB
		before = fmt.Sprintf("%d", env.WiredLimitMB)
	case env.MetalWorkingSetBytes > 0:
		current = env.MetalWorkingSetBytes >> 20
		before = fmt.Sprintf("0 (macOS default, Metal allows %s)", gib(env.MetalWorkingSetBytes))
	default:
		p.NotOffered = append(p.NotOffered, NotOffered{KeyWiredLimit, title,
			"the current GPU limit cannot be measured until the MLX runtime is installed"})
		return
	}
	if target-current < minGainMiB {
		p.NotOffered = append(p.NotOffered, NotOffered{KeyWiredLimit, title, fmt.Sprintf(
			"GPU can already use %s of %s; a safe higher limit (%s) would add under 1 GiB",
			mibToGiB(current), gib(env.MemTotalBytes), mibToGiB(target))})
		return
	}
	after := fmt.Sprintf("%d", target)
	p.Changes = append(p.Changes, Change{
		Key:   KeyWiredLimit,
		Title: title,
		Why:   "macOS caps how much unified memory the GPU may wire. Raising it lets larger models (and longer contexts) load fully on the GPU.",
		Effect: fmt.Sprintf("Lets the GPU use %s instead of %s. Models that already fit will NOT get faster; it only widens what can run. Leaves %s for macOS.",
			mibToGiB(target), mibToGiB(current), mibToGiB(reserve)),
		Before: before, After: after, NeedsAdmin: true,
		Persistent: "until reboot (enable the persistence option to keep it)",
		Diff:       diff("iogpu.wired_limit_mb", before, after),
		value:      target,
	})
	if !env.PersistDaemon {
		p.Changes = append(p.Changes, Change{
			Key:    KeyPersist,
			Title:  "Keep the GPU memory limit across reboots",
			Why:    "The sysctl resets at reboot. This installs a small root LaunchDaemon that re-applies it at boot.",
			Effect: "No performance effect by itself. Installs /Library/LaunchDaemons/ai.aituner.wiredlimit.plist; reverting removes it.",
			Before: "not installed", After: "installed", NeedsAdmin: true, Persistent: "yes", Requires: KeyWiredLimit,
			Diff:  diff("/Library/LaunchDaemons/ai.aituner.wiredlimit.plist", "absent", fmt.Sprintf("RunAtLoad: sysctl iogpu.wired_limit_mb=%d", target)),
			value: target,
		})
	}
}

func (p *Plan) planOllama(env Env) {
	title := "Ollama: flash attention + q8 KV cache"
	if !env.OllamaApp {
		p.NotOffered = append(p.NotOffered, NotOffered{KeyOllamaEnv, title, "Ollama.app is not installed"})
		return
	}
	if env.OllamaFlashAttention == "1" && env.OllamaKVCache == "q8_0" {
		p.NotOffered = append(p.NotOffered, NotOffered{KeyOllamaEnv, title, "already configured"})
		return
	}
	show := func(v string) string {
		if v == "" {
			return "unset"
		}
		return v
	}
	before := fmt.Sprintf("OLLAMA_FLASH_ATTENTION=%s, OLLAMA_KV_CACHE_TYPE=%s", show(env.OllamaFlashAttention), show(env.OllamaKVCache))
	after := "OLLAMA_FLASH_ATTENTION=1, OLLAMA_KV_CACHE_TYPE=q8_0"
	p.Changes = append(p.Changes, Change{
		Key:    KeyOllamaEnv,
		Title:  title,
		Why:    "Documented Ollama server settings (docs.ollama.com/faq). Flash attention is faster on long contexts; q8_0 KV cache halves context memory.",
		Effect: "May speed up long-prompt processing and free memory for larger contexts. q8_0 KV cache has a small quality cost. The re-run benchmark shows whether it helped; short prompts often see no change. Restarts Ollama.app.",
		Before: before, After: after, NeedsAdmin: false,
		Persistent: "until logout/reboot (launchctl setenv, as Ollama documents for macOS)",
		Diff: diff("OLLAMA_FLASH_ATTENTION", show(env.OllamaFlashAttention), "1") + "\n" +
			diff("OLLAMA_KV_CACHE_TYPE", show(env.OllamaKVCache), "q8_0"),
	})
}

// Keys returns the change keys in application order.
func (p Plan) Keys() []string {
	ks := make([]string, 0, len(p.Changes))
	for _, c := range p.Changes {
		ks = append(ks, c.Key)
	}
	return ks
}

// ValidateSelection checks a user's opt-in set against the plan: only planned keys, dependencies satisfied.
func (p Plan) ValidateSelection(keys []string) error {
	sel := map[string]bool{}
	for _, k := range keys {
		if _, ok := p.Find(k); !ok {
			return fmt.Errorf("unknown or unavailable change %q", k)
		}
		sel[k] = true
	}
	for k := range sel {
		c, _ := p.Find(k)
		if c.Requires != "" && !sel[c.Requires] {
			return fmt.Errorf("%q requires %q", k, c.Requires)
		}
	}
	return nil
}

// Runner applies changes. Split from planning so plan logic is testable without touching the system.
type Runner interface {
	Apply(ctx context.Context, c Change) error
	Revert(ctx context.Context, c Change) error
}
