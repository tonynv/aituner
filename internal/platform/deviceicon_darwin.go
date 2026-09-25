//go:build darwin

package platform

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// coreTypes is where macOS keeps its own device pictures and the table mapping model identifiers (Mac13,1) to them.
const coreTypes = "/System/Library/CoreServices/CoreTypes.bundle/Contents"

var icnsName = regexp.MustCompile(`^[A-Za-z0-9._-]+\.icns$`)

type utDecl struct {
	ID       string          `json:"UTTypeIdentifier"`
	Conforms json.RawMessage `json:"UTTypeConformsTo"`
	Icons    struct {
		File string `json:"UTTypeIconFile"`
	} `json:"UTTypeIcons"`
	IconFile string                     `json:"UTTypeIconFile"`
	TagSpecs map[string]json.RawMessage `json:"UTTypeTagSpecification"`
}

func stringsOf(raw json.RawMessage) []string {
	var many []string
	if json.Unmarshal(raw, &many) == nil {
		return many
	}
	var one string
	if json.Unmarshal(raw, &one) == nil {
		return []string{one}
	}
	return nil
}

var (
	iconOnce sync.Once
	iconByID map[string]string // model identifier -> icns path
)

// deviceIcons parses CoreTypes' type table once: each model code maps to its type's icon, or the nearest parent type's.
// It uses its own context: a cancelled request must not leave the table empty for the rest of the run.
func deviceIcons() map[string]string {
	iconOnce.Do(func() {
		iconByID = map[string]string{}
		out, err := Run(context.Background(), 10*time.Second, "/usr/bin/plutil", "-convert", "json", "-o", "-", filepath.Join(coreTypes, "Info.plist"))
		if err != nil {
			return
		}
		var doc struct {
			Decls []utDecl `json:"UTExportedTypeDeclarations"`
		}
		if json.Unmarshal([]byte(out), &doc) != nil {
			return
		}
		byType := map[string]utDecl{}
		for _, d := range doc.Decls {
			byType[d.ID] = d
		}
		iconOf := func(id string) string {
			for depth := 0; depth < 8 && id != ""; depth++ {
				d, ok := byType[id]
				if !ok {
					return ""
				}
				f := d.Icons.File
				if f == "" {
					f = d.IconFile
				}
				if icnsName.MatchString(f) {
					p := filepath.Join(coreTypes, "Resources", f)
					if _, err := os.Stat(p); err == nil {
						return p
					}
				}
				parents := stringsOf(d.Conforms)
				id = ""
				for _, p := range parents { // follow the Mac branch, never the generic "public.computer" leaves
					if strings.HasPrefix(p, "com.apple.") {
						id = p
						break
					}
				}
			}
			return ""
		}
		for _, d := range doc.Decls {
			for _, code := range stringsOf(d.TagSpecs["com.apple.device-model-code"]) {
				code, _, _ = strings.Cut(code, "@") // "MacPro7,1@ECOLOR=..." is a colour variant of MacPro7,1
				if code == "" || code == "Mac" || code == "Macintosh" {
					continue
				}
				if _, seen := iconByID[code]; !seen {
					if p := iconOf(d.ID); p != "" {
						iconByID[code] = p
					}
				}
			}
		}
	})
	return iconByID
}

// DeviceIcon returns macOS's own picture (.icns) for a model identifier such as "Mac13,1" (Mac Studio).
func DeviceIcon(_ context.Context, identifier string) (string, bool) {
	p, ok := deviceIcons()[identifier]
	return p, ok
}
