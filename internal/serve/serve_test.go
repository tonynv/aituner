package serve

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/tonynv/aituner/internal/bench"
	"github.com/tonynv/aituner/internal/download"
	"github.com/tonynv/aituner/internal/hf"
	"github.com/tonynv/aituner/internal/platform"
)

func TestSpecValidationAndArgs(t *testing.T) {
	ok := Spec{Bin: "/x/mlx_lm.server", ModelDir: "/m"}
	if err := ok.validate(); err != nil {
		t.Fatal(err)
	}
	for name, s := range map[string]Spec{
		"no bin": {ModelDir: "/m"}, "no dir": {Bin: "/x"}, "neg tokens": {Bin: "/x", ModelDir: "/m", MaxTokens: -1},
		"huge tokens": {Bin: "/x", ModelDir: "/m", MaxTokens: 1 << 30}, "bad bits": {Bin: "/x", ModelDir: "/m", KVBits: 3},
		"bad group": {Bin: "/x", ModelDir: "/m", KVBits: 8, KVGroupSize: 50}, "neg start": {Bin: "/x", ModelDir: "/m", QuantKVStart: -5},
	} {
		if err := s.validate(); !errors.Is(err, ErrBadSpec) {
			t.Errorf("%s accepted: %v", name, err)
		}
	}
	a := strings.Join(Spec{Bin: "b", ModelDir: "/m d/x", MaxTokens: 4096, KVBits: 8, KVGroupSize: 64, QuantKVStart: 1024}.Args(8123), " ")
	want := "--model /m d/x --host 127.0.0.1 --port 8123 --max-tokens 4096 --kv-bits 8 --kv-group-size 64 --quantized-kv-start 1024"
	if a != want {
		t.Fatalf("\n got %s\nwant %s", a, want)
	}
	for _, args := range [][]string{ok.Args(1), Spec{Bin: "b", ModelDir: "/m", KVBits: 4}.Args(1)} {
		for _, x := range args {
			if x == "--trust-remote-code" || x == "0.0.0.0" {
				t.Fatalf("unsafe argument %q", x)
			}
		}
	}
	if strings.Contains(strings.Join(ok.Args(1), " "), "kv-bits") {
		t.Fatal("KV quantisation must be off unless asked for")
	}
}

func TestLogRingIsBoundedAndSequenced(t *testing.T) {
	m := New()
	for i := 0; i < ringSize+50; i++ {
		m.appendLog("line")
	}
	all := m.Logs(0)
	if len(all) != ringSize || all[0].Seq != 51 || all[len(all)-1].Seq != ringSize+50 {
		t.Fatalf("ring: len=%d first=%d last=%d", len(all), all[0].Seq, all[len(all)-1].Seq)
	}
	if got := m.Logs(ringSize + 49); len(got) != 1 {
		t.Fatalf("after: %v", got)
	}
	m.appendLog("  \n") // blank lines are dropped
	if len(m.Logs(ringSize+50)) != 0 {
		t.Fatal("blank line logged")
	}
}

func TestStartRefusesMissingFolderAndReportsStopped(t *testing.T) {
	m := New()
	if err := m.Start(context.Background(), Spec{Bin: "/bin/true", ModelDir: "/no/such/dir"}); !errors.Is(err, ErrBadSpec) {
		t.Fatalf("%v", err)
	}
	if st := m.Status(); st.State != StateStopped || st.PID != 0 {
		t.Fatalf("%+v", st)
	}
	m.Stop() // stopping something that never ran is a no-op
	if _, ok := m.Backend(); ok {
		t.Fatal("no backend while stopped")
	}
}

func TestUnexpectedExitBecomesAnErrorWithLogTail(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "fake-server.sh")
	os.WriteFile(bin, []byte("#!/bin/sh\necho 'boom: cannot load weights' >&2\nexit 3\n"), 0o755)
	m := New()
	if err := m.Start(context.Background(), Spec{Bin: bin, ModelDir: dir, Repo: "x/y"}); err != nil {
		t.Fatal(err)
	}
	var st Status
	for i := 0; i < 50; i++ {
		if st = m.Status(); st.State == StateError {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if st.State != StateError || !strings.Contains(st.Error, "exited unexpectedly") || !strings.Contains(st.Error, "cannot load weights") {
		t.Fatalf("%+v", st)
	}
	if err := m.Start(context.Background(), Spec{Bin: bin, ModelDir: dir}); err != nil {
		t.Fatalf("a failed server can be started again: %v", err)
	}
	m.Stop()
}

func liveSetup(t *testing.T) (bench.MLX, string) {
	t.Helper()
	if os.Getenv("AITUNER_LIVE") != "1" {
		t.Skip("set AITUNER_LIVE=1 (downloads a 79 MB model and runs a real server)")
	}
	data, err := platform.Current().DataDir()
	if err != nil {
		t.Fatal(err)
	}
	mlx := bench.MLX{Python: filepath.Join(data, "venv", "bin", "python"), Dir: filepath.Join(data, "runtime")}
	root := t.TempDir()
	dm := download.New(hf.New())
	const repo = "mlx-community/SmolLM-135M-Instruct-4bit"
	if _, err := dm.Start(context.Background(), mlx, repo, root); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 300; i++ {
		for _, s := range dm.List(root) {
			if s.Repo == repo && s.State == download.State_Done {
				return mlx, s.Dest
			}
			if s.State == download.State_Error {
				t.Fatal(s.Error)
			}
		}
		time.Sleep(300 * time.Millisecond)
	}
	t.Fatal("download timeout")
	return mlx, ""
}

// Real mlx_lm.server on a real downloaded model: starts, becomes ready, answers a chat request, cannot be made to
// load another model, reports memory, stops cleanly and leaves no process behind.
func TestLiveServeLifecycle(t *testing.T) {
	mlx, dir := liveSetup(t)
	bin := filepath.Join(filepath.Dir(mlx.Python), "mlx_lm.server")
	m := New()
	if err := m.Start(context.Background(), Spec{Bin: bin, ModelDir: dir, Repo: "mlx-community/SmolLM-135M-Instruct-4bit", MaxTokens: 64}); err != nil {
		t.Fatal(err)
	}
	if err := m.Start(context.Background(), Spec{Bin: bin, ModelDir: dir}); !errors.Is(err, ErrRunning) {
		t.Fatalf("second start: %v", err)
	}
	var base string
	for i := 0; i < 240; i++ {
		if u, ok := m.Backend(); ok {
			base = u
			break
		}
		if st := m.Status(); st.State == StateError {
			t.Fatalf("server failed: %s\n%s", st.Error, m.tail(20))
		}
		time.Sleep(500 * time.Millisecond)
	}
	if base == "" {
		t.Fatalf("never ready\n%s", m.tail(20))
	}
	st := m.Status()
	if st.State != StateRunning || st.PID == 0 || st.RSSBytes < 50<<20 || st.ReadyAfterS <= 0 {
		t.Fatalf("%+v", st)
	}
	post := func(body string) (int, string) {
		resp, err := http.Post(base+"/v1/chat/completions", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	code, out := post(`{"messages":[{"role":"user","content":"Say hello."}],"max_tokens":16}`)
	var ok struct {
		Choices []struct{ Message struct{ Content string } }
		Usage   struct{ Prompt_tokens, Completion_tokens int }
	}
	if code != 200 || json.Unmarshal([]byte(out), &ok) != nil || len(ok.Choices) == 0 || ok.Usage.Completion_tokens == 0 {
		t.Fatalf("chat: %d %s", code, out)
	}
	// SAFETY: naming another model must NOT make the server fetch/load it (HF_HUB_OFFLINE=1). It fails instead.
	code, out = post(`{"model":"mlx-community/Llama-3.2-1B-Instruct-4bit","messages":[{"role":"user","content":"hi"}],"max_tokens":4}`)
	if code == 200 {
		t.Fatalf("server loaded a different model on request: %s", out)
	}
	if u, ok := m.Backend(); !ok || u != base {
		t.Fatal("server must remain up and on the same model after the refused request")
	}
	if code, _ = post(`{"messages":[{"role":"user","content":"still there?"}],"max_tokens":4}`); code != 200 {
		t.Fatalf("server unusable after the refused request: %d", code)
	}
	pid := st.PID
	m.Stop()
	if s := m.Status(); s.State != StateStopped || s.PID != 0 {
		t.Fatalf("after stop: %+v", s)
	}
	if err := syscallKill0(pid); err == nil {
		t.Fatalf("process %d still alive after Stop", pid)
	}
	if _, ok := m.Backend(); ok {
		t.Fatal("backend reported after stop")
	}
}

func fakeServer(t *testing.T, name string) (bin, dir string) {
	t.Helper()
	dir = t.TempDir()
	bin = filepath.Join(dir, name)
	// no exec: like the real python server, the process keeps its script name and arguments in its command line
	os.WriteFile(bin, []byte("#!/bin/sh\nwhile true; do sleep 1; done\n"), 0o755)
	return bin, dir
}

func alive(pid int) bool { return syscallKill0(pid) == nil }

func TestServerStopsWhenTheParentContextEnds(t *testing.T) {
	bin, dir := fakeServer(t, "mlx_lm.server")
	ctx, cancel := context.WithCancel(context.Background())
	m := New()
	if err := m.Start(ctx, Spec{Bin: bin, ModelDir: dir}); err != nil {
		t.Fatal(err)
	}
	pid := m.Status().PID
	if pid == 0 || !alive(pid) {
		t.Fatal("server should be running")
	}
	cancel() // aituner quits or receives SIGTERM
	for i := 0; i < 80 && alive(pid); i++ {
		time.Sleep(100 * time.Millisecond)
	}
	if alive(pid) {
		t.Fatalf("model server %d outlived aituner's context: it would keep GPU memory forever", pid)
	}
}

func TestPIDFileIsWrittenAndRemoved(t *testing.T) {
	bin, dir := fakeServer(t, "mlx_lm.server")
	m := New()
	m.PIDFile = filepath.Join(t.TempDir(), "serve.pid")
	if err := m.Start(context.Background(), Spec{Bin: bin, ModelDir: dir}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(m.PIDFile)
	if err != nil || !strings.Contains(string(b), dir) {
		t.Fatalf("pidfile: %s %v", b, err)
	}
	if fi, _ := os.Stat(m.PIDFile); fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", fi.Mode().Perm())
	}
	m.Stop()
	if _, err := os.Stat(m.PIDFile); err == nil {
		t.Fatal("pidfile must be removed once the server stops")
	}
}

// After a hard crash the next launch stops the leftover server, but never a process that merely reuses the pid.
func TestReapStaleStopsOnlyTheRecordedServer(t *testing.T) {
	bin, dir := fakeServer(t, "mlx_lm.server")
	stale := exec.Command(bin, "--model", dir)
	stale.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := stale.Start(); err != nil {
		t.Fatal(err)
	}
	go stale.Wait()
	pf := filepath.Join(t.TempDir(), "serve.pid")
	rec, _ := json.Marshal(pidRecord{PID: stale.Process.Pid, ModelDir: dir})
	os.WriteFile(pf, rec, 0o600)
	if reaped, err := ReapStale(pf); err != nil || !reaped {
		t.Fatalf("reaped=%v err=%v", reaped, err)
	}
	for i := 0; i < 30 && alive(stale.Process.Pid); i++ {
		time.Sleep(100 * time.Millisecond)
	}
	if alive(stale.Process.Pid) {
		t.Fatal("the stale server was not stopped")
	}
	if _, err := os.Stat(pf); err == nil {
		t.Fatal("pidfile must be cleared")
	}

	// an unrelated process that happens to have the recorded pid must survive
	other := exec.Command("sleep", "30")
	if err := other.Start(); err != nil {
		t.Fatal(err)
	}
	defer other.Process.Kill()
	go other.Wait()
	rec, _ = json.Marshal(pidRecord{PID: other.Process.Pid, ModelDir: dir})
	os.WriteFile(pf, rec, 0o600)
	if reaped, _ := ReapStale(pf); reaped || !alive(other.Process.Pid) {
		t.Fatalf("an unrelated process was killed (reaped=%v)", reaped)
	}
	for name, content := range map[string]string{"garbage": "not json", "pid 1": `{"pid":1,"model_dir":"/x"}`, "empty": `{}`} {
		os.WriteFile(pf, []byte(content), 0o600)
		if reaped, err := ReapStale(pf); reaped || err != nil {
			t.Errorf("%s: reaped=%v err=%v", name, reaped, err)
		}
	}
	if reaped, err := ReapStale(filepath.Join(t.TempDir(), "missing")); reaped || err != nil {
		t.Fatal("a missing pidfile is normal")
	}
}

func TestLogFileIsNeverWrittenThroughAPlantedSymlink(t *testing.T) {
	bin, dir := fakeServer(t, "mlx_lm.server")
	victim := filepath.Join(t.TempDir(), "victim.txt")
	os.WriteFile(victim, []byte("precious"), 0o600)
	m := New()
	m.LogFile = filepath.Join(t.TempDir(), "model-server.log")
	os.Symlink(victim, m.LogFile)
	if err := m.Start(context.Background(), Spec{Bin: bin, ModelDir: dir}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	m.Stop()
	if b, _ := os.ReadFile(victim); string(b) != "precious" {
		t.Fatalf("the model server log was written through a symlink: %q", b)
	}
}
