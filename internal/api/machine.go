package api

import (
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/tonynv/aituner/internal/platform"
)

var modelIDRe = regexp.MustCompile(`^[A-Za-z]+[0-9]+,[0-9]+$`)

// handleMachineImage serves macOS's own picture of this Mac (from CoreTypes), converted to PNG once and cached in the
// app data folder. 404 when macOS has no picture for this model; the UI then shows a generic machine.
func (s *Server) handleMachineImage(w http.ResponseWriter, r *http.Request) {
	hw := s.hardware()
	if hw == nil || !modelIDRe.MatchString(hw.Model.Identifier) {
		http.NotFound(w, r)
		return
	}
	icns, ok := platform.DeviceIcon(r.Context(), hw.Model.Identifier)
	if !ok {
		http.NotFound(w, r)
		return
	}
	png := filepath.Join(s.cfg.DataDir, "device-"+hw.Model.Identifier+".png")
	if _, err := os.Stat(png); err != nil {
		tmp := png + ".tmp.png"
		if _, err := platform.Run(r.Context(), 20*time.Second, "/usr/bin/sips", "-s", "format", "png", "-Z", "512", icns, "--out", tmp); err != nil {
			_ = os.Remove(tmp)
			writeErr(w, 500, "image", "could not convert the device picture")
			return
		}
		_ = os.Chmod(tmp, 0o600)
		if err := os.Rename(tmp, png); err != nil {
			writeErr(w, 500, "image", err.Error())
			return
		}
	}
	w.Header().Set("Cache-Control", "private, max-age=86400")
	http.ServeFile(w, r, png)
}
