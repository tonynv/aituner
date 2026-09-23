package api

import (
	"github.com/tonynv/aituner/internal/detect"
	"github.com/tonynv/aituner/internal/platform"
)

func fingerprint(h *platform.Hardware) string { return detect.Fingerprint(h) }
