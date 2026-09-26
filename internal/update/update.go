// Package update finds a newer aituner release on GitHub, downloads it, proves it is genuine and swaps it in.
//
// A release is installed only if every check passes: the zip's SHA-256 equals both GitHub's asset digest and the
// release's SHA256SUMS; the app inside is signed (strict, deep) by aituner's Developer ID team, accepted by Gatekeeper
// (notarized), and is ai.aituner.app at exactly the advertised version. The swap happens after aituner quits, in a
// small helper that keeps the old app until the new one is in place and puts it back if the copy fails.
package update

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/tonynv/aituner/internal/httpx"
)

const (
	// Repo is where releases are published.
	Repo = "tonynv/aituner"
	// TeamID is the Developer ID team that signs releases (public: it is embedded in every signature).
	TeamID = "KZWKV6U343"
	// BundleID identifies the app.
	BundleID = "ai.aituner.app"

	maxZip  = 512 << 20
	maxSums = 64 << 10
)

// Hosts are the only hosts the updater talks to: the API, the download URLs, and where GitHub redirects downloads.
var Hosts = []string{"api.github.com", "github.com", "release-assets.githubusercontent.com"}

// APIBase is GitHub's API root (a variable for tests).
var APIBase = "https://api.github.com"

// Asset is one downloadable file of a release.
type Asset struct {
	Name   string `json:"name"`
	URL    string `json:"url"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256,omitempty"` // from GitHub's asset digest, when present
}

// Release is a published, non-draft, non-prerelease version.
type Release struct {
	Version   string `json:"version"` // "0.2.0"
	Tag       string `json:"tag"`     // "v0.2.0"
	Notes     string `json:"notes"`
	Page      string `json:"page"`
	Published string `json:"published"`
	Zip       Asset  `json:"zip"`
	Sums      Asset  `json:"sums"`
}

var (
	tagRe    = regexp.MustCompile(`^v(\d+)\.(\d+)\.(\d+)$`)
	digestRe = regexp.MustCompile(`^sha256:([0-9a-f]{64})$`)
	hexRe    = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// Current returns the release version this binary was built as ("v0.1.0" -> "0.1.0"). Development builds (untagged,
// commits after a tag, a dirty tree) are not releases: ok is false and they are never replaced automatically.
func Current(buildVersion string) (string, bool) {
	if m := tagRe.FindStringSubmatch(buildVersion); m != nil {
		return strings.TrimPrefix(buildVersion, "v"), true
	}
	return "", false
}

func parts(v string) ([3]int, bool) {
	m := tagRe.FindStringSubmatch("v" + v)
	if m == nil {
		return [3]int{}, false
	}
	var p [3]int
	for i := range p {
		p[i], _ = strconv.Atoi(m[i+1])
	}
	return p, true
}

// Newer reports whether version a is newer than b (both "X.Y.Z").
func Newer(a, b string) bool {
	pa, ok1 := parts(a)
	pb, ok2 := parts(b)
	if !ok1 || !ok2 {
		return false
	}
	for i := range pa {
		if pa[i] != pb[i] {
			return pa[i] > pb[i]
		}
	}
	return false
}

type ghRelease struct {
	Tag        string `json:"tag_name"`
	Draft      bool   `json:"draft"`
	Prerelease bool   `json:"prerelease"`
	Body       string `json:"body"`
	HTMLURL    string `json:"html_url"`
	Published  string `json:"published_at"`
	Assets     []struct {
		Name   string `json:"name"`
		URL    string `json:"browser_download_url"`
		Size   int64  `json:"size"`
		Digest string `json:"digest"`
	} `json:"assets"`
}

// Latest returns the latest release, or nil when the repository has none yet.
func Latest(ctx context.Context, c *httpx.Client) (*Release, error) {
	var gr ghRelease
	err := c.JSON(ctx, "GET", APIBase+"/repos/"+Repo+"/releases/latest", nil, &gr)
	var se *httpx.StatusError
	if errors.As(err, &se) && se.Code == 404 {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return fromGitHub(gr)
}

func fromGitHub(gr ghRelease) (*Release, error) {
	m := tagRe.FindStringSubmatch(gr.Tag)
	if m == nil || gr.Draft || gr.Prerelease {
		return nil, fmt.Errorf("latest release %q is not a vX.Y.Z release", gr.Tag)
	}
	r := &Release{Version: strings.TrimPrefix(gr.Tag, "v"), Tag: gr.Tag, Notes: clip(gr.Body, 4000), Page: gr.HTMLURL, Published: gr.Published}
	for _, a := range gr.Assets {
		as := Asset{Name: a.Name, URL: a.URL, Size: a.Size}
		if d := digestRe.FindStringSubmatch(a.Digest); d != nil {
			as.SHA256 = d[1]
		}
		switch a.Name {
		case "aituner-" + r.Version + ".zip":
			r.Zip = as
		case "SHA256SUMS":
			r.Sums = as
		}
	}
	if r.Zip.URL == "" || r.Sums.URL == "" {
		return nil, fmt.Errorf("release %s is missing aituner-%s.zip or SHA256SUMS", r.Tag, r.Version)
	}
	return r, nil
}

func clip(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// sumFor finds name's SHA-256 in a SHA256SUMS file ("<hex>  <name>" lines).
func sumFor(sums, name string) (string, bool) {
	for _, l := range strings.Split(sums, "\n") {
		f := strings.Fields(l)
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == name && hexRe.MatchString(f[0]) {
			return f[0], true
		}
	}
	return "", false
}

// Runner runs a command and returns its combined output (a field so tests can observe calls).
type Runner func(ctx context.Context, name string, args ...string) ([]byte, error)

// ExecRunner runs real commands with a timeout.
func ExecRunner(ctx context.Context, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

// Fetch downloads the release into dir (a fresh private folder), checks the zip against GitHub's digest and
// SHA256SUMS, and unpacks it. It returns the path of the unpacked aituner.app; Verify must pass before it is used.
func Fetch(ctx context.Context, c *httpx.Client, run Runner, r *Release, dir string) (string, error) {
	sumsPath := filepath.Join(dir, "SHA256SUMS")
	if _, _, err := c.Download(ctx, r.Sums.URL, sumsPath, maxSums); err != nil {
		return "", fmt.Errorf("download SHA256SUMS: %w", err)
	}
	sums, err := os.ReadFile(sumsPath)
	if err != nil {
		return "", err
	}
	want, ok := sumFor(string(sums), r.Zip.Name)
	if !ok {
		return "", fmt.Errorf("SHA256SUMS has no entry for %s", r.Zip.Name)
	}
	if r.Zip.SHA256 != "" && r.Zip.SHA256 != want {
		return "", errors.New("GitHub's digest and SHA256SUMS disagree: refusing the update")
	}
	zipPath := filepath.Join(dir, r.Zip.Name)
	got, _, err := c.Download(ctx, r.Zip.URL, zipPath, maxZip)
	if err != nil {
		return "", fmt.Errorf("download %s: %w", r.Zip.Name, err)
	}
	if got != want {
		return "", fmt.Errorf("%s does not match its checksum: refusing the update", r.Zip.Name)
	}
	out := filepath.Join(dir, "app")
	if b, err := run(ctx, "/usr/bin/ditto", "-x", "-k", zipPath, out); err != nil {
		return "", fmt.Errorf("unpack: %v: %s", err, b)
	}
	app := filepath.Join(out, "aituner.app")
	if fi, err := os.Lstat(app); err != nil || !fi.IsDir() {
		return "", errors.New("the release does not contain aituner.app")
	}
	return app, nil
}

var teamRe = regexp.MustCompile(`(?m)^TeamIdentifier=(\S+)$`)

// Verify proves an unpacked app is genuine: valid strict deep signature by team, accepted by Gatekeeper (notarized
// Developer ID), and the expected bundle identifier and version. aituner calls it with BundleID and TeamID.
func Verify(ctx context.Context, run Runner, app, bundleID, version, team string) error {
	if b, err := run(ctx, "/usr/bin/codesign", "--verify", "--deep", "--strict", app); err != nil {
		return fmt.Errorf("signature is not valid: %s", strings.TrimSpace(string(b)))
	}
	b, _ := run(ctx, "/usr/bin/codesign", "-dv", app)
	m := teamRe.FindSubmatch(b)
	if m == nil || string(m[1]) != team {
		return fmt.Errorf("not signed by aituner's developer (team %s)", team)
	}
	if b, err := run(ctx, "/usr/sbin/spctl", "--assess", "--type", "execute", app); err != nil {
		return fmt.Errorf("Gatekeeper rejects it (not notarized): %s", strings.TrimSpace(string(b)))
	}
	plist := filepath.Join(app, "Contents", "Info.plist")
	id, _ := run(ctx, "/usr/bin/plutil", "-extract", "CFBundleIdentifier", "raw", "-o", "-", plist)
	if strings.TrimSpace(string(id)) != bundleID {
		return fmt.Errorf("unexpected bundle identifier %q", strings.TrimSpace(string(id)))
	}
	v, _ := run(ctx, "/usr/bin/plutil", "-extract", "CFBundleShortVersionString", "raw", "-o", "-", plist)
	if strings.TrimSpace(string(v)) != version {
		return fmt.Errorf("the app inside is version %q, not %s", strings.TrimSpace(string(v)), version)
	}
	return nil
}

// BundleOf returns the .app bundle that contains executable exe (…/aituner.app/Contents/MacOS/aituner-server).
func BundleOf(exe string) (string, bool) {
	dir := filepath.Dir(exe)
	if filepath.Base(dir) != "MacOS" || filepath.Base(filepath.Dir(dir)) != "Contents" {
		return "", false
	}
	b := filepath.Dir(filepath.Dir(dir))
	return b, strings.HasSuffix(b, ".app")
}

// Homebrew reports whether this bundle was installed by the Homebrew cask (so brew, not aituner, should update it).
func Homebrew(bundle string) bool {
	if bundle != "/Applications/aituner.app" {
		return false
	}
	for _, p := range []string{"/opt/homebrew/Caskroom/aituner", "/usr/local/Caskroom/aituner"} {
		if fi, err := os.Stat(p); err == nil && fi.IsDir() {
			return true
		}
	}
	return false
}

// Writable reports whether the bundle's folder accepts a replacement (it is created and removed to be sure).
func Writable(bundle string) bool {
	f, err := os.CreateTemp(filepath.Dir(bundle), ".aituner-update-*")
	if err != nil {
		return false
	}
	name := f.Name()
	f.Close()
	return os.Remove(name) == nil
}

func shq(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// Script is the helper that runs after aituner quits: wait for the app process to exit (up to a minute), move the old
// bundle aside, copy the new one in, put the old one back if that fails, and relaunch. open is the launcher
// (/usr/bin/open; a parameter for tests).
func Script(appPID int, newApp, target, open string) string {
	return fmt.Sprintf(`#!/bin/bash
# aituner update helper (written by aituner)
set -u
pid=%d new=%s target=%s backup=%s
for _ in $(seq 1 600); do kill -0 "$pid" 2>/dev/null || break; sleep 0.1; done
if kill -0 "$pid" 2>/dev/null; then echo "aituner did not quit; update not applied"; exit 1; fi
rm -rf "$backup"
mv "$target" "$backup" || { echo "could not move the old app aside"; exit 1; }
if /usr/bin/ditto "$new" "$target"; then
  rm -rf "$backup"
  echo "updated"
else
  rm -rf "$target"; mv "$backup" "$target"
  echo "copy failed; the previous version was restored"
fi
exec %s "$target"
`, appPID, shq(newApp), shq(target), shq(target+".previous"), shq(open))
}
