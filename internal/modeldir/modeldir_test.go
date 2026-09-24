package modeldir

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A throwaway HOME so every path here is real (real symlinks, real permissions) but inside the sandbox.
func home(t *testing.T) string {
	t.Helper()
	h := t.TempDir()
	t.Setenv("HOME", h)
	real, _ := filepath.EvalSymlinks(h) // macOS temp dirs live behind /var -> /private/var
	return real
}

func TestResolveAcceptsFoldersInHomeAndVolumes(t *testing.T) {
	h := home(t)
	for in, want := range map[string]string{
		"~/Models":                 filepath.Join(h, "Models"),
		"~/Models/llm/mlx":         filepath.Join(h, "Models", "llm", "mlx"),
		filepath.Join(h, "Docs/M"): filepath.Join(h, "Docs", "M"),
		"  ~/Models  ":             filepath.Join(h, "Models"),
		"/Volumes/Fast/Models":     "/Volumes/Fast/Models",
	} {
		got, err := Resolve(in)
		if err != nil || got != want {
			t.Errorf("%q -> %q, %v (want %q)", in, got, err, want)
		}
	}
}

func TestResolveRejectsDangerousOrSillyPaths(t *testing.T) {
	h := home(t)
	if err := os.Symlink("/etc", filepath.Join(h, "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(h, "Library"), 0o755); err != nil {
		t.Fatal(err)
	}
	long := "~/" + strings.Repeat("a", 1100)
	for name, in := range map[string]string{
		"empty":           "",
		"blank":           "   ",
		"home itself":     "~",
		"relative":        "Models/x",
		"dot relative":    "./Models",
		"system":          "/etc",
		"tmp":             "/tmp/models",
		"other user":      "/Users/someone-else/Models",
		"root":            "/",
		"traversal":       "~/Models/../../../etc",
		"traversal abs":   h + "/Models/../../etc",
		"symlink to /etc": "~/link/models",
		"hidden":          "~/.ssh",
		"hidden nested":   "~/Models/.cache",
		"library":         "~/Library",
		"library nested":  "~/Library/Application Support/x",
		"volumes root":    "/Volumes",
		"drive root":      "/Volumes/Fast",
		"volumes hidden":  "/Volumes/Fast/.Trashes",
		"nul":             "~/Models\x00/x",
		"newline":         "~/Models\n/x",
		"too long":        long,
		"prefix trick":    h + "-evil/Models", // shares a string prefix with HOME but is a sibling directory
	} {
		if got, err := Resolve(in); err == nil {
			t.Errorf("%s: %q accepted as %q", name, in, got)
		}
	}
}

func TestResolveFollowsSymlinksInsideHomeToTheirRealTarget(t *testing.T) {
	h := home(t)
	if err := os.MkdirAll(filepath.Join(h, "Real"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(h, "Real"), filepath.Join(h, "Alias")); err != nil {
		t.Fatal(err)
	}
	got, err := Resolve("~/Alias/models")
	if err != nil || got != filepath.Join(h, "Real", "models") {
		t.Fatalf("%q %v", got, err)
	}
}

func TestEnsureCreatesAndProvesWritable(t *testing.T) {
	h := home(t)
	p, _ := Resolve("~/Models/new")
	if i := Stat(p); i.Exists || i.FreeBytes == 0 || !i.Writable {
		t.Fatalf("before: %+v", i) // does not exist yet, but its parent is writable and the disk has space
	}
	if err := Ensure(p); err != nil {
		t.Fatal(err)
	}
	if i := Stat(p); !i.Exists || !i.Writable {
		t.Fatalf("after: %+v", i)
	}
	if ents, _ := os.ReadDir(p); len(ents) != 0 {
		t.Fatalf("write probe left files behind: %v", ents)
	}
	// read-only parent
	ro := filepath.Join(h, "ro")
	os.MkdirAll(ro, 0o500)
	if err := Ensure(filepath.Join(ro, "sub")); err == nil {
		t.Fatal("must fail in a read-only folder")
	}
}

func TestDestStaysInsideRoot(t *testing.T) {
	root := "/Users/x/Models"
	if d, err := Dest(root, "mlx-community/Qwen3-8B-4bit"); err != nil || d != root+"/mlx-community/Qwen3-8B-4bit" {
		t.Fatalf("%q %v", d, err)
	}
	for _, bad := range []string{"", "a", "../x/y", "a/../../b", "a/b; rm -rf ~", "/abs/path", "a/b/c", "x/.."} {
		if d, err := Dest(root, bad); err == nil {
			t.Errorf("%q accepted as %q", bad, d)
		}
	}
}

func TestDefaultIsInHome(t *testing.T) {
	home(t)
	if d, err := Default(); err != nil || d != filepath.Join(os.Getenv("HOME"), "Models") {
		t.Fatalf("%q %v", d, err)
	}
	if _, err := Resolve("~/Models"); err != nil {
		t.Fatalf("the default must satisfy its own policy: %v", err)
	}
}
