package reco

import (
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/tonynv/aituner/internal/hf"
)

var alnum = regexp.MustCompile(`[^a-z0-9]+`)

func norm(s string) string { return alnum.ReplaceAllString(strings.ToLower(s), "") }

var (
	ggufSuffix = regexp.MustCompile(`(?i)[-_.]?(gguf|mlx)$`)
	// remainder after the base name for an official-style MLX quant: optional instruct/it tag, bits or bf16, optional DWQ
	quantRe = regexp.MustCompile(`^(instruct|it|chat|thinking|fp8)?(\d+)bit(dwq|mixed\w*)?$|^(instruct|it)?(bf16|fp16)$`)
)

// BaseName turns a canirun.ai model URL (a HF repo, usually a GGUF mirror) into the base model name:
// https://huggingface.co/lmstudio-community/Qwen3.6-35B-A3B-GGUF -> Qwen3.6-35B-A3B
func BaseName(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 2 {
		return ""
	}
	return ggufSuffix.ReplaceAllString(parts[len(parts)-1], "")
}

// MLXQuant is an mlx-community repo that is a plain quantisation of the base model.
type MLXQuant struct {
	Repo hf.Repo
	Bits int // 16 for bf16/fp16
}

// MatchMLX keeps only repos that are a straightforward MLX quant of base (no fine-tunes, prunes or merges):
// after the base name the repo name may only carry an instruct tag and a bits marker.
func MatchMLX(base string, repos []hf.Repo) []MLXQuant {
	nb := norm(base)
	if nb == "" {
		return nil
	}
	var out []MLXQuant
	for _, r := range repos {
		if r.Author() != "mlx-community" {
			continue
		}
		n := norm(r.Name())
		rest, ok := strings.CutPrefix(n, nb)
		if !ok {
			continue
		}
		m := quantRe.FindStringSubmatch(rest)
		if m == nil {
			continue
		}
		bits := 16
		if m[2] != "" {
			bits, _ = strconv.Atoi(m[2])
		}
		out = append(out, MLXQuant{Repo: r, Bits: bits})
	}
	return out
}

var variantWords = []string{"abliterated", "uncensored", "heretic", "unrestricted", "josiefied"}

// IsUnrestrictedVariant reports whether a repo is a community abliterated/uncensored build of base: its
// name must contain the base and one of the variant markers.
func IsUnrestrictedVariant(base string, r hf.Repo) bool {
	n := norm(r.Name())
	if !strings.Contains(n, norm(base)) {
		return false
	}
	for _, w := range variantWords {
		if strings.Contains(n, w) {
			return true
		}
	}
	return false
}

// bitsFromName extracts the quantisation width from a repo name: "-4bit", "-8Bit", "mxfp4", "nvfp4",
// "q8", "int2". Returns 0 when the name does not say.
var bitsRes = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(\d+)[-_]?bit`),
	regexp.MustCompile(`(?i)(?:mx|nv)fp(\d+)`),
	regexp.MustCompile(`(?i)(?:^|[-_.])int(\d+)(?:$|[-_.])`),
	regexp.MustCompile(`(?i)(?:^|[-_.])q(\d+)(?:$|[-_.])`),
}

func bitsFromName(name string) int {
	for _, re := range bitsRes {
		if m := re.FindStringSubmatch(name); m != nil {
			if b, _ := strconv.Atoi(m[1]); b > 0 && b <= 16 {
				return b
			}
		}
	}
	return 0
}

var activeRe = regexp.MustCompile(`(?i)[-_ ]a(\d+(?:\.\d+)?)b`)

// ActiveParamsBillions returns the active parameter count for MoE names like "35B-A3B" (3), or 0 if unknown.
func ActiveParamsBillions(name string) float64 {
	if m := activeRe.FindStringSubmatch(name); m != nil {
		v, _ := strconv.ParseFloat(m[1], 64)
		return v
	}
	return 0
}
