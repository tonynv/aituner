package api

import (
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"time"

	"github.com/tonynv/aituner/internal/platform"
)

var modelIDRe = regexp.MustCompile(`^[A-Za-z]+[0-9]+,[0-9]+$`)

// appIcons maps an integration to the app bundle whose own icon represents it. Icons are read from the installed app
// at run time (nothing is redistributed); the bundle names are constants, never request input.
var appIcons = map[string]string{
	"vscode": "Visual Studio Code.app",
	"claude": "Claude.app",
}

var bundleIconRe = regexp.MustCompile(`^[A-Za-z0-9 ._-]+$`)

// iconPNG converts an .icns to a cached PNG (size px, 0600) in the app data folder, once.
func (s *Server) iconPNG(r *http.Request, icns, name string, size int) (string, error) {
	png := filepath.Join(s.cfg.DataDir, name+".png")
	if _, err := os.Stat(png); err == nil {
		return png, nil
	}
	tmp := png + ".tmp.png"
	if _, err := platform.Run(r.Context(), 20*time.Second, "/usr/bin/sips", "-s", "format", "png", "-Z", strconv.Itoa(size), icns, "--out", tmp); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	_ = os.Chmod(tmp, 0o600)
	return png, os.Rename(tmp, png)
}

// handleAppIcon serves the icon of an installed app that an integration uses (VS Code, Claude); 404 when the app is
// not installed, and the UI shows its own line icon instead.
func (s *Server) handleAppIcon(w http.ResponseWriter, r *http.Request) {
	bundle, ok := appIcons[r.PathValue("id")]
	if !ok {
		http.NotFound(w, r)
		return
	}
	home, _ := os.UserHomeDir()
	for _, root := range []string{"/Applications", filepath.Join(home, "Applications")} {
		app := filepath.Join(root, bundle)
		name, err := platform.Run(r.Context(), 5*time.Second, "/usr/bin/plutil", "-extract", "CFBundleIconFile", "raw", "-o", "-", filepath.Join(app, "Contents", "Info.plist"))
		if err != nil || !bundleIconRe.MatchString(name) {
			continue
		}
		if filepath.Ext(name) == "" {
			name += ".icns"
		}
		icns := filepath.Join(app, "Contents", "Resources", name)
		if _, err := os.Stat(icns); err != nil {
			continue
		}
		png, err := s.iconPNG(r, icns, "appicon-"+r.PathValue("id"), 128)
		if err != nil {
			writeErr(w, 500, "image", "could not convert the app icon")
			return
		}
		w.Header().Set("Cache-Control", "private, max-age=86400")
		http.ServeFile(w, r, png)
		return
	}
	http.NotFound(w, r)
}

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
	png, err := s.iconPNG(r, icns, "device-"+hw.Model.Identifier, 512)
	if err != nil {
		writeErr(w, 500, "image", "could not convert the device picture")
		return
	}
	w.Header().Set("Cache-Control", "private, max-age=86400")
	http.ServeFile(w, r, png)
}
