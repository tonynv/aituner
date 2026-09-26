package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tonynv/aituner/internal/httpx"
)

func TestCurrentAndNewer(t *testing.T) {
	for in, want := range map[string]string{"v0.1.0": "0.1.0", "v12.3.40": "12.3.40", "dev": "", "v0.1.0-3-gabc1234": "", "v0.1.0-dirty": "", "0.1.0": ""} {
		if got, ok := Current(in); got != want || ok != (want != "") {
			t.Errorf("Current(%q) = %q %v", in, got, ok)
		}
	}
	for _, c := range []struct {
		a, b  string
		newer bool
	}{{"0.2.0", "0.1.9", true}, {"0.10.0", "0.9.0", true}, {"1.0.0", "0.99.99", true}, {"0.1.0", "0.1.0", false}, {"0.1.0", "0.2.0", false}, {"x", "0.1.0", false}} {
		if Newer(c.a, c.b) != c.newer {
			t.Errorf("Newer(%s, %s) != %v", c.a, c.b, c.newer)
		}
	}
}

// The field names follow GitHub's REST API as observed on a real release (cli/cli, 2026-09-25): tag_name, draft,
// prerelease, body, html_url, published_at, assets[].name/browser_download_url/size/digest ("sha256:<hex>").
func ghJSON(tag string, assets ...string) string {
	var as []string
	for _, a := range assets {
		as = append(as, fmt.Sprintf(`{"name":%q,"browser_download_url":"https://github.com/tonynv/aituner/releases/download/%s/%s","size":10,"digest":"sha256:%s"}`, a, tag, a, strings.Repeat("a", 64)))
	}
	return fmt.Sprintf(`{"tag_name":%q,"draft":false,"prerelease":false,"body":"notes","html_url":"https://github.com/tonynv/aituner/releases/tag/%s","published_at":"2026-09-26T00:00:00Z","assets":[%s]}`, tag, tag, strings.Join(as, ","))
}

func TestFromGitHub(t *testing.T) {
	var gr ghRelease
	json.Unmarshal([]byte(ghJSON("v0.2.0", "aituner-0.2.0.dmg", "aituner-0.2.0.zip", "SHA256SUMS")), &gr)
	r, err := fromGitHub(gr)
	if err != nil || r.Version != "0.2.0" || r.Zip.Name != "aituner-0.2.0.zip" || r.Zip.SHA256 != strings.Repeat("a", 64) || r.Sums.Name != "SHA256SUMS" {
		t.Fatalf("%+v %v", r, err)
	}
	json.Unmarshal([]byte(ghJSON("v0.2.0", "aituner-0.2.0.dmg")), &gr)
	if _, err := fromGitHub(gr); err == nil {
		t.Fatal("a release without the zip and SHA256SUMS must be refused")
	}
	for _, bad := range []string{`{"tag_name":"v0.2.0-beta"}`, `{"tag_name":"v0.2.0","draft":true}`, `{"tag_name":"v0.2.0","prerelease":true}`} {
		var g ghRelease
		json.Unmarshal([]byte(bad), &g)
		if _, err := fromGitHub(g); err == nil {
			t.Errorf("accepted %s", bad)
		}
	}
}

// tlsClient points an httpx client at a local TLS server that stands in for GitHub.
func tlsClient(t *testing.T, h http.Handler) (*httpx.Client, string) {
	t.Helper()
	ts := httptest.NewTLSServer(h)
	t.Cleanup(ts.Close)
	host, _, _ := net.SplitHostPort(strings.TrimPrefix(ts.URL, "https://"))
	c := httpx.New(host)
	c.HTTP.Transport = ts.Client().Transport
	return c, ts.URL
}

func TestLatestNoReleaseIsNotAnError(t *testing.T) {
	c, base := tlsClient(t, http.NotFoundHandler())
	old := APIBase
	APIBase = base
	t.Cleanup(func() { APIBase = old })
	if r, err := Latest(context.Background(), c); r != nil || err != nil {
		t.Fatalf("%v %v", r, err)
	}
}

// signedApp builds a minimal real app bundle and signs it (ad hoc, which still seals every file in the bundle).
func signedApp(t *testing.T, dir string) string {
	t.Helper()
	app := filepath.Join(dir, "aituner.app")
	os.MkdirAll(filepath.Join(app, "Contents", "MacOS"), 0o755)
	os.MkdirAll(filepath.Join(app, "Contents", "Resources"), 0o755)
	plist := `<?xml version="1.0" encoding="UTF-8"?><!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd"><plist version="1.0"><dict><key>CFBundleExecutable</key><string>t</string><key>CFBundleIdentifier</key><string>ai.aituner.test</string><key>CFBundleShortVersionString</key><string>0.2.0</string></dict></plist>`
	os.WriteFile(filepath.Join(app, "Contents", "Info.plist"), []byte(plist), 0o644)
	os.WriteFile(filepath.Join(app, "Contents", "Resources", "r"), []byte("resource"), 0o644)
	if out, err := exec.Command("/bin/cp", "/usr/bin/true", filepath.Join(app, "Contents", "MacOS", "t")).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	if out, err := exec.Command("/usr/bin/codesign", "--force", "--sign", "-", app).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	return app
}

// zipOf packs a signed app the way the release workflow packs aituner.
func zipOf(t *testing.T) []byte {
	t.Helper()
	dir := t.TempDir()
	signedApp(t, dir)
	z := filepath.Join(dir, "a.zip")
	if out, err := exec.Command("/usr/bin/ditto", "-c", "-k", "--keepParent", filepath.Join(dir, "aituner.app"), z).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	b, _ := os.ReadFile(z)
	return b
}

func TestFetchChecksBothSums(t *testing.T) {
	zip := zipOf(t)
	h := sha256.Sum256(zip)
	good := hex.EncodeToString(h[:])
	sums := good + "  aituner-0.2.0.zip\n"
	mux := http.NewServeMux()
	mux.HandleFunc("/zip", func(w http.ResponseWriter, r *http.Request) { w.Write(zip) })
	mux.HandleFunc("/sums", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(sums)) })
	c, base := tlsClient(t, mux)
	rel := func(digest string) *Release {
		return &Release{Version: "0.2.0", Zip: Asset{Name: "aituner-0.2.0.zip", URL: base + "/zip", SHA256: digest}, Sums: Asset{Name: "SHA256SUMS", URL: base + "/sums"}}
	}
	app, err := Fetch(context.Background(), c, ExecRunner, rel(good), t.TempDir())
	if err != nil || filepath.Base(app) != "aituner.app" {
		t.Fatalf("%q %v", app, err)
	}
	if _, err := Fetch(context.Background(), c, ExecRunner, rel(strings.Repeat("b", 64)), t.TempDir()); err == nil || !strings.Contains(err.Error(), "disagree") {
		t.Fatalf("GitHub digest mismatch must refuse: %v", err)
	}
	sums = strings.Repeat("c", 64) + "  aituner-0.2.0.zip\n"
	if _, err := Fetch(context.Background(), c, ExecRunner, rel(""), t.TempDir()); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("a zip that does not match SHA256SUMS must refuse: %v", err)
	}
}

// Live: Verify against a real Developer ID signed, notarized app on this Mac (VS Code, Microsoft's team).
func TestVerifyRealSignatures(t *testing.T) {
	const vsc = "/Applications/Visual Studio Code.app"
	if _, err := os.Stat(vsc); err != nil {
		t.Skip("VS Code is not installed")
	}
	ctx := context.Background()
	ver, _ := exec.Command("/usr/bin/plutil", "-extract", "CFBundleShortVersionString", "raw", "-o", "-", vsc+"/Contents/Info.plist").Output()
	v := strings.TrimSpace(string(ver))
	if err := Verify(ctx, ExecRunner, vsc, "com.microsoft.VSCode", v, "UBF8T346G9"); err != nil {
		t.Fatalf("a genuine signed, notarized app must pass: %v", err)
	}
	if err := Verify(ctx, ExecRunner, vsc, "com.microsoft.VSCode", v, TeamID); err == nil || !strings.Contains(err.Error(), "team") {
		t.Fatalf("another developer's app must be refused: %v", err)
	}
	if err := Verify(ctx, ExecRunner, vsc, "com.microsoft.VSCode", "0.0.1", "UBF8T346G9"); err == nil || !strings.Contains(err.Error(), "version") {
		t.Fatalf("a version mismatch must be refused: %v", err)
	}
	if err := Verify(ctx, ExecRunner, vsc, BundleID, v, "UBF8T346G9"); err == nil || !strings.Contains(err.Error(), "identifier") {
		t.Fatalf("a different app must be refused: %v", err)
	}
	// a signed app passes the signature step (then fails on team: ad hoc has none); changed after signing, it fails
	// the signature check itself
	app := signedApp(t, t.TempDir())
	if err := Verify(ctx, ExecRunner, app, "ai.aituner.test", "0.2.0", TeamID); err == nil || !strings.Contains(err.Error(), "team") {
		t.Fatalf("an ad hoc signed app must fail on the team: %v", err)
	}
	os.WriteFile(filepath.Join(app, "Contents", "Resources", "r"), []byte("tampered"), 0o644)
	if err := Verify(ctx, ExecRunner, app, "ai.aituner.test", "0.2.0", TeamID); err == nil || !strings.Contains(err.Error(), "signature") {
		t.Fatalf("a modified app must be refused: %v", err)
	}
}

func TestScriptSwapsRestoresAndRelaunches(t *testing.T) {
	dir := t.TempDir()
	mk := func(p, content string) {
		os.MkdirAll(filepath.Join(p, "Contents"), 0o755)
		os.WriteFile(filepath.Join(p, "Contents", "v"), []byte(content), 0o644)
	}
	target, newApp := filepath.Join(dir, "aituner.app"), filepath.Join(dir, "new", "aituner.app")
	mk(target, "old")
	mk(newApp, "new")
	opened := filepath.Join(dir, "opened")
	open := filepath.Join(dir, "open")
	os.WriteFile(open, []byte("#!/bin/sh\necho \"$1\" > "+opened+"\n"), 0o755)

	app := exec.Command("/bin/sleep", "0.5") // stands for the app process the helper waits for
	app.Start()
	go app.Wait()
	script := filepath.Join(dir, "update.sh")
	os.WriteFile(script, []byte(Script(app.Process.Pid, newApp, target, open)), 0o700)
	out, err := exec.Command("/bin/bash", script).CombinedOutput()
	if err != nil || !strings.Contains(string(out), "updated") {
		t.Fatalf("%v %s", err, out)
	}
	if b, _ := os.ReadFile(filepath.Join(target, "Contents", "v")); string(b) != "new" {
		t.Fatalf("target holds %q", b)
	}
	if _, err := os.Stat(target + ".previous"); !os.IsNotExist(err) {
		t.Fatal("backup left behind")
	}
	if b, _ := os.ReadFile(opened); strings.TrimSpace(string(b)) != target {
		t.Fatalf("relaunched %q", b)
	}

	// the copy fails (new app missing): the old app must be back in place
	mk(target, "old")
	os.WriteFile(script, []byte(Script(999999, filepath.Join(dir, "missing.app"), target, open)), 0o700)
	out, _ = exec.Command("/bin/bash", script).CombinedOutput()
	if b, _ := os.ReadFile(filepath.Join(target, "Contents", "v")); string(b) != "old" || !strings.Contains(string(out), "restored") {
		t.Fatalf("not restored: %q %s", b, out)
	}
}

func TestBundleOfAndPaths(t *testing.T) {
	if b, ok := BundleOf("/Applications/aituner.app/Contents/MacOS/aituner-server"); !ok || b != "/Applications/aituner.app" {
		t.Fatal(b)
	}
	if _, ok := BundleOf("/Users/x/aituner/bin/aituner"); ok {
		t.Fatal("a terminal build is not a bundle")
	}
	if !strings.Contains(Script(1, "/a b/n'x.app", "/Applications/aituner.app", "/usr/bin/open"), `'/a b/n'\''x.app'`) {
		t.Fatal("paths must be shell-quoted")
	}
}

// Live: the real GitHub API answers for aituner's repository (no release yet is a valid answer).
func TestLatestLive(t *testing.T) {
	if os.Getenv("AITUNER_LIVE") == "" {
		t.Skip("set AITUNER_LIVE=1")
	}
	r, err := Latest(context.Background(), httpx.New(Hosts...))
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("latest: %+v", r)
}
