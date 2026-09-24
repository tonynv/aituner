package reco

import (
	"regexp"
	"strings"
	"testing"

	"github.com/tonynv/aituner/internal/hf"
)

// Property: whatever a third party names a repo, an accepted name is only ever [A-Za-z0-9._-]/[A-Za-z0-9._-]
// and any command built from it contains no shell metacharacter outside our own quoted path.
func FuzzMLXChatCommand(f *testing.F) {
	for _, s := range []string{"mlx-community/Qwen3-8B-4bit", "a/b; rm -rf ~", "a/b$(id)", "x/\nrm", "../../x", "a/b'c", "\x00/\x00"} {
		f.Add(s)
	}
	safe := regexp.MustCompile("^[A-Za-z0-9._/-]+$")
	f.Fuzz(func(t *testing.T, repo string) {
		cmd := MLXChatCommand("", repo)
		if cmd == "" {
			if ValidRepo(repo) {
				t.Fatalf("valid repo produced no command: %q", repo)
			}
			return
		}
		arg := strings.TrimPrefix(cmd, "mlx_lm.chat --model ")
		if arg != repo || !safe.MatchString(arg) || strings.Contains(arg, "..") {
			t.Fatalf("unsafe repo reached a command: %q -> %q", repo, cmd)
		}
	})
}

func FuzzBaseNameAndMatch(f *testing.F) {
	for _, s := range []string{"https://huggingface.co/a/b-GGUF", "%%%", "https://x/", "", "https://huggingface.co/a/b/c/d"} {
		f.Add(s, "b")
	}
	f.Fuzz(func(t *testing.T, url, base string) {
		_ = BaseName(url) // must not panic
		_ = MatchMLX(base, []hf.Repo{{ID: url}, {ID: "mlx-community/" + base + "-4bit"}})
		_ = IsUnrestrictedVariant(base, hf.Repo{ID: url})
		_ = ActiveParamsBillions(url)
		_ = bitsFromName(url)
	})
}

func FuzzPreferQuantsAndEstimate(f *testing.F) {
	f.Add(int64(1<<30), 8.0, 0.5, int64(1<<20))
	f.Fuzz(func(t *testing.T, budget int64, params, frac float64, size int64) {
		_ = preferQuants([]MLXQuant{{Bits: 4}, {Bits: 8}, {Bits: 16}}, budget, params)
		if tps, eff := Estimate(Input{GPUBandwidthGBs: 300, MLXGenTPS: 100, BenchModelBytes: 1 << 30}, size, frac); tps < 0 || eff < 0 || tps != tps {
			t.Fatalf("bad estimate %v %v for size=%d frac=%v", tps, eff, size, frac)
		}
		_ = FitStatus(size, budget)
	})
}
