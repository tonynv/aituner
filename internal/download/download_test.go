package download

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tonynv/aituner/internal/bench"
	"github.com/tonynv/aituner/internal/hf"
	"github.com/tonynv/aituner/internal/platform"
)

func TestWantedFilesOnly(t *testing.T) {
	for name, want := range map[string]bool{
		"model.safetensors": true, "model-00001-of-00004.safetensors": true, "config.json": true, "tokenizer.json": true,
		"model.safetensors.index.json": true, "tokenizer.model": true, "merges.txt": true, "chat_template.jinja": true,
		"modeling_custom.py": false, "pytorch_model.bin": false, "model.pt": false, "weights.pkl": false, "README.md": false,
		".gitattributes": false, "LICENSE": false, "sub/dir/config.json": true, "sub/evil.py": false, "x.ckpt": false,
	} {
		if Wanted(name) != want {
			t.Errorf("Wanted(%q) = %v, want %v", name, !want, want)
		}
	}
}

func TestPlanForRejectsUnsafeRepos(t *testing.T) {
	mk := func(files map[string]int64, gated any) hf.Info {
		var i hf.Info
		i.Gated = gated
		for n, s := range files {
			i.Siblings = append(i.Siblings, hf.Sibling{Name: n, Size: s})
		}
		return i
	}
	if _, err := PlanFor(mk(map[string]int64{"a.safetensors": 1}, true)); err == nil || !strings.Contains(err.Error(), "gated") {
		t.Fatalf("gated: %v", err)
	}
	if _, err := PlanFor(mk(map[string]int64{"pytorch_model.bin": 9}, false)); err == nil || !strings.Contains(err.Error(), "pickle") {
		t.Fatalf("pickle: %v", err)
	}
	if _, err := PlanFor(mk(map[string]int64{"config.json": 1}, false)); err == nil || !strings.Contains(err.Error(), "safetensors") {
		t.Fatalf("no weights: %v", err)
	}
	p, err := PlanFor(mk(map[string]int64{"m.safetensors": 100, "config.json": 5, "README.md": 999, "code.py": 7}, false))
	if err != nil || p.Total != 105 || len(p.Files) != 2 {
		t.Fatalf("%+v %v", p, err)
	}
}

func TestVerifyChecksEveryFileAndSize(t *testing.T) {
	dir := t.TempDir()
	plan := Plan{Files: []hf.Sibling{{Name: "m.safetensors", Size: 4}, {Name: "sub/config.json", Size: 2}}, Total: 6}
	if err := Verify(dir, plan); err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("missing: %v", err)
	}
	os.WriteFile(filepath.Join(dir, "m.safetensors"), []byte("abcd"), 0o644)
	os.MkdirAll(filepath.Join(dir, "sub"), 0o755)
	os.WriteFile(filepath.Join(dir, "sub", "config.json"), []byte("{"), 0o644) // 1 byte, expected 2
	if err := Verify(dir, plan); err == nil || !strings.Contains(err.Error(), "expected 2") {
		t.Fatalf("truncated: %v", err)
	}
	os.WriteFile(filepath.Join(dir, "sub", "config.json"), []byte("{}"), 0o644)
	if err := Verify(dir, plan); err != nil {
		t.Fatal(err)
	}
}

func TestRequiredSpace(t *testing.T) {
	if Required(100, 100) != 0 || Required(100, 200) != 0 {
		t.Fatal("nothing left to fetch needs no space")
	}
	total := int64(10 << 30)
	if got := Required(total, 0); got != total+total/50+diskSlack {
		t.Fatalf("%d", got)
	}
	if Required(total, total/2) >= Required(total, 0) {
		t.Fatal("partial progress must reduce the requirement")
	}
}

func TestCompleteNeedsMatchingMarker(t *testing.T) {
	d := t.TempDir()
	if _, ok := Complete(d, "a/b"); ok {
		t.Fatal("no marker")
	}
	os.WriteFile(filepath.Join(d, MarkerName), []byte(`{"repo":"a/b","bytes":42}`), 0o644)
	if n, ok := Complete(d, "a/b"); !ok || n != 42 {
		t.Fatalf("%d %v", n, ok)
	}
	if _, ok := Complete(d, "a/other"); ok {
		t.Fatal("marker for another repo must not count")
	}
	os.WriteFile(filepath.Join(d, MarkerName), []byte(`garbage`), 0o644)
	if _, ok := Complete(d, "a/b"); ok {
		t.Fatal("corrupt marker must not count")
	}
}

func TestListFindsCompletedOnDiskAndIgnoresJunk(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "org", "model"), 0o755)
	os.WriteFile(filepath.Join(root, "org", "model", MarkerName), []byte(`{"repo":"org/model","bytes":9}`), 0o644)
	os.MkdirAll(filepath.Join(root, "org", "partial"), 0o755) // no marker: not complete
	os.WriteFile(filepath.Join(root, "stray.txt"), []byte("x"), 0o644)
	got := New(hf.New()).List(root)
	if len(got) != 1 || got[0].Repo != "org/model" || got[0].State != State_Done || got[0].BytesDone != 9 {
		t.Fatalf("%+v", got)
	}
	if l := New(hf.New()).List(filepath.Join(root, "does-not-exist")); len(l) != 0 {
		t.Fatalf("missing root: %v", l)
	}
}

func TestChatCommandQuotesPaths(t *testing.T) {
	got := ChatCommand("/Users/o'b/Library/Application Support/aituner/venv/bin", "/Users/o'b/Models/org/m")
	want := `'/Users/o'\''b/Library/Application Support/aituner/venv/bin/mlx_lm.chat' --model '/Users/o'\''b/Models/org/m'`
	if got != want {
		t.Fatalf("\n got %s\nwant %s", got, want)
	}
}

func liveMLX(t *testing.T) bench.MLX {
	t.Helper()
	if os.Getenv("AITUNER_LIVE") != "1" {
		t.Skip("set AITUNER_LIVE=1 (downloads real models)")
	}
	dir, err := platform.Current().DataDir()
	if err != nil {
		t.Fatal(err)
	}
	return bench.MLX{Python: filepath.Join(dir, "venv", "bin", "python"), Dir: filepath.Join(dir, "runtime")}
}

func waitState(t *testing.T, m *Manager, root, repo string, want string, within time.Duration) Status {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		for _, s := range m.List(root) {
			if s.Repo == repo && s.State == want {
				return s
			}
			if s.Repo == repo && s.State == State_Error {
				t.Fatalf("download failed: %s", s.Error)
			}
		}
		time.Sleep(300 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %s", want)
	return Status{}
}

// Real download of a real 79 MB model into a temp folder; verifies files, marker, persistence and safety.
func TestLiveDownloadTinyModel(t *testing.T) {
	mlx := liveMLX(t)
	root := t.TempDir()
	m := New(hf.New())
	const repo = "mlx-community/SmolLM-135M-Instruct-4bit"
	st, err := m.Start(context.Background(), mlx, repo, root)
	if err != nil || st.State != State_Running || st.BytesTotal < 70<<20 {
		t.Fatalf("%+v %v", st, err)
	}
	if _, err := m.Start(context.Background(), mlx, "mlx-community/SmolLM-135M-4bit", root); err != ErrBusy {
		t.Fatalf("a second download must be refused while one runs: %v", err)
	}
	done := waitState(t, m, root, repo, State_Done, 3*time.Minute)
	dest := filepath.Join(root, "mlx-community", "SmolLM-135M-Instruct-4bit")
	if done.Dest != dest || done.BytesTotal != done.BytesDone {
		t.Fatalf("%+v", done)
	}
	for _, f := range []string{"model.safetensors", "config.json", "tokenizer.json", MarkerName} {
		if _, err := os.Stat(filepath.Join(dest, f)); err != nil {
			t.Errorf("missing %s", f)
		}
	}
	for _, bad := range []string{"README.md", ".gitattributes"} {
		if _, err := os.Stat(filepath.Join(dest, bad)); err == nil {
			t.Errorf("unwanted file downloaded: %s", bad)
		}
	}
	// a fresh manager (as after a restart) still knows it is done, and Start is an instant no-op
	m2 := New(hf.New())
	if l := m2.List(root); len(l) != 1 || l[0].State != State_Done {
		t.Fatalf("after restart: %+v", l)
	}
	if st, err := m2.Start(context.Background(), mlx, repo, root); err != nil || st.State != State_Done {
		t.Fatalf("re-download must be a no-op: %+v %v", st, err)
	}
	// the downloaded folder really loads and generates with mlx-lm (not just "files exist")
	out, err := platform.Run(context.Background(), 2*time.Minute, mlx.Python, "-c",
		"import sys; from mlx_lm import load, generate; m,t=load(sys.argv[1]); print(generate(m,t,prompt='Hello',max_tokens=8))", dest)
	if err != nil || strings.TrimSpace(out) == "" {
		t.Fatalf("model does not load from the downloaded folder: %v %q", err, out)
	}
}

// Cancel mid-download keeps partial data; a second attempt resumes and completes with verified files.
func TestLiveCancelAndResume(t *testing.T) {
	mlx := liveMLX(t)
	root := t.TempDir()
	m := New(hf.New())
	const repo = "mlx-community/Llama-3.2-3B-Instruct-4bit"
	if _, err := m.Start(context.Background(), mlx, repo, root); err != nil {
		t.Fatal(err)
	}
	var partial int64
	for i := 0; i < 100 && partial < 100<<20; i++ { // wait until at least 100 MB is on disk
		time.Sleep(200 * time.Millisecond)
		for _, s := range m.List(root) {
			partial = s.BytesDone
		}
	}
	if !m.Cancel(repo) {
		t.Fatal("cancel found nothing running")
	}
	c := waitState(t, m, root, repo, State_Cancel, 30*time.Second)
	if c.BytesDone <= 0 || c.BytesDone >= c.BytesTotal {
		t.Fatalf("cancel should leave partial data: %+v", c)
	}
	if m.Active() {
		t.Fatal("still active after cancel")
	}
	dest := filepath.Join(root, "mlx-community", "Llama-3.2-3B-Instruct-4bit")
	if _, ok := Complete(dest, repo); ok {
		t.Fatal("cancelled download must not be marked complete")
	}
	if _, err := m.Start(context.Background(), mlx, repo, root); err != nil {
		t.Fatalf("resume: %v", err)
	}
	d := waitState(t, m, root, repo, State_Done, 5*time.Minute)
	if d.BytesDone != d.BytesTotal {
		t.Fatalf("%+v", d)
	}
}

// The queue runs one download at a time, in order, skips a failing start and lets a waiting item be removed.
func TestQueueOrderFailureAndCancel(t *testing.T) {
	root := t.TempDir()
	m := New(hf.New())
	var mu sync.Mutex
	var started []string
	m.start = func(_ context.Context, _ bench.MLX, repo, _ string) (Status, error) {
		mu.Lock()
		started = append(started, repo)
		mu.Unlock()
		if repo == "org/bad" {
			return Status{}, errors.New("no such model")
		}
		// become active like the real Start; the test ends it via finish
		r := &run{cancel: func() {}, st: Status{Repo: repo, State: State_Running}}
		m.mu.Lock()
		m.active = r
		m.mu.Unlock()
		return r.st, nil
	}
	ctx := context.Background()
	for _, repo := range []string{"org/a", "org/bad", "org/c", "org/d"} {
		st, err := m.Enqueue(ctx, bench.MLX{}, repo, root)
		if err != nil || st.State != State_Queued {
			t.Fatalf("%s: %+v %v", repo, st, err)
		}
	}
	if st, _ := m.Enqueue(ctx, bench.MLX{}, "org/c", root); st.Position == 0 {
		t.Fatal("duplicate must report its existing position")
	}
	wait := func(cond func() bool) {
		t.Helper()
		for i := 0; i < 200; i++ {
			if cond() {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatal("timed out")
	}
	wait(func() bool { m.mu.Lock(); defer m.mu.Unlock(); return m.active != nil && m.active.st.Repo == "org/a" })
	if !m.Cancel("org/d") { // removed while waiting
		t.Fatal("waiting item not cancellable")
	}
	m.mu.Lock()
	a := m.active
	m.mu.Unlock()
	m.finish(a, State_Done, "") // a ends: bad fails and is skipped, c starts
	wait(func() bool { m.mu.Lock(); defer m.mu.Unlock(); return m.active != nil && m.active.st.Repo == "org/c" })
	got := map[string]Status{}
	for _, s := range m.List(root) {
		got[s.Repo] = s
	}
	if got["org/bad"].State != State_Error || got["org/d"].State != State_Cancel || got["org/c"].State != State_Running {
		t.Fatalf("%+v", got)
	}
	mu.Lock()
	defer mu.Unlock()
	if strings.Join(started, ",") != "org/a,org/bad,org/c" {
		t.Fatalf("order: %v", started)
	}
}
