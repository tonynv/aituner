// Package detect turns a platform Hardware snapshot into a stable fingerprint. Detection itself lives in
// internal/platform so the OS-specific code stays in one place.
package detect

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/tonynv/aituner/internal/platform"
)

// Fingerprint identifies the machine class without serials or UUIDs (privacy): model, chip, cores, memory.
func Fingerprint(h *platform.Hardware) string {
	s := fmt.Sprintf("%s|%s|%s|%d|%d|%d", h.Platform, h.Model.Identifier, h.CPU.Chip, h.GPU.Cores, h.CPU.Cores, h.Memory.TotalBytes)
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}
