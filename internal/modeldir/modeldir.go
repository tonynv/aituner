// Package modeldir validates and manages the folder where downloaded models are saved. The folder is
// user-configurable, so it is checked against an explicit policy: the download endpoint writes many
// gigabytes there, so it must never be a system location, a hidden config directory, or a symlink away from them.
package modeldir

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/tonynv/aituner/internal/reco"
)

const maxPath = 1024

// Default is ~/Models.
func Default() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Models"), nil
}

func hasControl(s string) bool {
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}

// Resolve expands a leading ~, cleans the path and enforces the policy. It returns the canonical path
// (symlinks in the existing part resolved) that downloads will be written under.
//
// Policy: absolute; no control characters; inside the user's home directory or under /Volumes (external
// drives); not the home directory or /Volumes itself; no hidden (dot) component below the root; not inside ~/Library.
func Resolve(input string) (string, error) {
	input = strings.TrimSpace(input)
	if input == "" || len(input) > maxPath || hasControl(input) {
		return "", errors.New("enter a folder path (up to 1024 characters, no control characters)")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	homeReal, err := filepath.EvalSymlinks(home)
	if err != nil {
		homeReal = home
	}
	switch {
	case input == "~":
		return "", errors.New("choose a folder inside your home directory, not the home directory itself")
	case strings.HasPrefix(input, "~/"):
		input = filepath.Join(home, input[2:])
	}
	if !filepath.IsAbs(input) {
		return "", errors.New("the path must be absolute (or start with ~/)")
	}
	p := filepath.Clean(input)
	// resolve symlinks in the longest existing prefix so a link cannot smuggle the path out of the allowed roots
	existing, rest := p, ""
	for {
		if _, err := os.Lstat(existing); err == nil {
			break
		}
		parent := filepath.Dir(existing)
		if parent == existing {
			break
		}
		rest = filepath.Join(filepath.Base(existing), rest)
		existing = parent
	}
	if real, err := filepath.EvalSymlinks(existing); err == nil {
		existing = real
	}
	p = filepath.Join(existing, rest)

	underHome := strings.HasPrefix(p, homeReal+string(filepath.Separator))
	underVolumes := strings.HasPrefix(p, "/Volumes/") && p != "/Volumes"
	if !underHome && !underVolumes {
		return "", errors.New("the folder must be inside your home directory or on an external drive (/Volumes)")
	}
	base := homeReal
	if underVolumes {
		base = "/Volumes"
	}
	rel, _ := filepath.Rel(base, p)
	for _, comp := range strings.Split(rel, string(filepath.Separator)) {
		if strings.HasPrefix(comp, ".") {
			return "", errors.New("hidden folders (starting with a dot) are not allowed")
		}
	}
	if underHome && (rel == "Library" || strings.HasPrefix(rel, "Library"+string(filepath.Separator))) {
		return "", errors.New("~/Library is managed by macOS; choose another folder")
	}
	if underVolumes && !strings.Contains(rel, string(filepath.Separator)) {
		// /Volumes/<drive> itself: allowed only as a parent, require a subfolder
		return "", errors.New("choose a folder on the drive, not the drive itself")
	}
	return p, nil
}

// Info describes a folder for the UI.
type Info struct {
	Path       string `json:"path"`
	Exists     bool   `json:"exists"`
	Writable   bool   `json:"writable"`
	FreeBytes  int64  `json:"free_bytes"`
	TotalBytes int64  `json:"total_bytes"`
}

// Stat reports on a resolved path without creating anything.
func Stat(p string) Info {
	i := Info{Path: p}
	if fi, err := os.Stat(p); err == nil && fi.IsDir() {
		i.Exists = true
	}
	probe := p
	for !i.Exists {
		parent := filepath.Dir(probe)
		if parent == probe {
			break
		}
		probe = parent
		if fi, err := os.Stat(probe); err == nil && fi.IsDir() {
			break
		}
	}
	var st syscall.Statfs_t
	if err := syscall.Statfs(probe, &st); err == nil {
		i.FreeBytes = int64(st.Bavail) * int64(st.Bsize)
		i.TotalBytes = int64(st.Blocks) * int64(st.Bsize)
	}
	if err := syscall.Access(probe, 2 /* W_OK */); err == nil {
		i.Writable = true
	}
	return i
}

// Ensure creates the folder (0755) and proves it is writable by creating and removing a file.
func Ensure(p string) error {
	if err := os.MkdirAll(p, 0o755); err != nil {
		return fmt.Errorf("cannot create %s: %w", p, err)
	}
	f, err := os.CreateTemp(p, ".aituner-write-test-*")
	if err != nil {
		return fmt.Errorf("%s is not writable: %w", p, err)
	}
	name := f.Name()
	f.Close()
	return os.Remove(name)
}

// Dest returns <root>/<org>/<name> for a validated repo id and proves it stays inside root.
func Dest(root, repo string) (string, error) {
	if !reco.ValidRepo(repo) {
		return "", errors.New("invalid repository id")
	}
	d := filepath.Join(root, filepath.FromSlash(repo))
	if rel, err := filepath.Rel(root, d); err != nil || strings.HasPrefix(rel, "..") {
		return "", errors.New("destination escapes the models folder")
	}
	return d, nil
}
