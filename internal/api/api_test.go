package api

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tonynv/aituner/internal/bench"
	"github.com/tonynv/aituner/internal/canirun"
	"github.com/tonynv/aituner/internal/hf"
	"github.com/tonynv/aituner/internal/platform"
	"github.com/tonynv/aituner/internal/reco"
	"github.com/tonynv/aituner/internal/serve"
	"github.com/tonynv/aituner/internal/store"
	"github.com/tonynv/aituner/internal/tune"
)

const token = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

type env struct {
	s    *Server
	ts   *httptest.Server
	host string
}

// Real store (temp file), real platform detection, real HTTP server. Data dir is a temp dir so the
// runtime is "not installed" and no test can start a real benchmark without confirmation.
func newEnv(t *testing.T) *env {
	t.Helper()
	t.Setenv("AITUNER_DATA_DIR", t.TempDir())
	t.Setenv("HOME", t.TempDir()) // sandbox: the models folder and HF cache lookups resolve inside it
	db, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	s, err := New(ctx, Config{Store: db, Tenant: "local", Platform: platform.Current(), Token: token, DataDir: t.TempDir(),
		CanIRun: canirun.New(), HF: hf.New(), Ollama: bench.NewOllama(), Runner: tune.NewRunner()})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(ts.URL, "http://"))
	p, _ := strconv.Atoi(port)
	s.SetPort(p)
	return &env{s: s, ts: ts, host: "127.0.0.1:" + port}
}

func (e *env) do(t *testing.T, method, path, body string, hdr map[string]string) (*http.Response, []byte) {
	t.Helper()
	req, _ := http.NewRequest(method, e.ts.URL+path, strings.NewReader(body))
	for k, v := range hdr {
		if k == "Host" {
			req.Host = v
			continue
		}
		req.Header.Set(k, v)
	}
	c := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }, Timeout: 10 * time.Second}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, b
}

func (e *env) authed(extra map[string]string) map[string]string {
	h := map[string]string{"Cookie": CookieName + "=" + token, "Origin": "http://" + e.host}
	for k, v := range extra {
		h[k] = v
	}
	return h
}

func TestUnauthenticatedRefused(t *testing.T) {
	e := newEnv(t)
	for _, p := range []string{"/api/v1/state", "/api/v1/health", "/api/v1/recommendations", "/api/v1/tune/plan", "/api/v1/events"} {
		if r, _ := e.do(t, "GET", p, "", nil); r.StatusCode != 401 {
			t.Errorf("%s: %d", p, r.StatusCode)
		}
	}
	if r, _ := e.do(t, "GET", "/api/v1/state", "", map[string]string{"Cookie": CookieName + "=wrong"}); r.StatusCode != 401 {
		t.Fatalf("wrong cookie: %d", r.StatusCode)
	}
	if r, _ := e.do(t, "GET", "/api/v1/state", "", map[string]string{"Authorization": "Bearer " + token}); r.StatusCode != 200 {
		t.Fatalf("bearer: %d", r.StatusCode)
	}
}

func TestHostHeaderAllowList(t *testing.T) { // DNS rebinding defence
	e := newEnv(t)
	for _, h := range []string{"evil.example.com", "evil.example.com:" + strings.Split(e.host, ":")[1], "127.0.0.1:1"} {
		if r, _ := e.do(t, "GET", "/api/v1/state", "", e.authed(map[string]string{"Host": h})); r.StatusCode != 403 {
			t.Errorf("Host %q: %d", h, r.StatusCode)
		}
	}
	if r, _ := e.do(t, "GET", "/", "", map[string]string{"Host": "evil.example.com"}); r.StatusCode != 403 {
		t.Fatal("static must also be host-checked")
	}
}

func TestOriginRequiredOnMutations(t *testing.T) {
	e := newEnv(t)
	cookie := map[string]string{"Cookie": CookieName + "=" + token}
	if r, _ := e.do(t, "POST", "/api/v1/benchmark", "{}", cookie); r.StatusCode != 403 {
		t.Fatalf("no origin: %d", r.StatusCode)
	}
	bad := map[string]string{"Cookie": CookieName + "=" + token, "Origin": "http://evil.example.com"}
	if r, _ := e.do(t, "POST", "/api/v1/benchmark", "{}", bad); r.StatusCode != 403 {
		t.Fatalf("foreign origin: %d", r.StatusCode)
	}
}

func TestLaunchLinkIsSingleUseAndSetsHardenedCookie(t *testing.T) {
	e := newEnv(t)
	n := e.s.LaunchToken()
	if n == token || len(n) < 32 {
		t.Fatalf("launch nonce must be distinct from the session token and long: %q", n)
	}
	r, _ := e.do(t, "GET", "/?t="+n, "", nil)
	if r.StatusCode != 302 || r.Header.Get("Location") != "/" {
		t.Fatalf("redirect: %d %q", r.StatusCode, r.Header.Get("Location"))
	}
	c := r.Cookies()
	if len(c) != 1 || !c[0].HttpOnly || c[0].SameSite != http.SameSiteStrictMode || c[0].Value != token {
		t.Fatalf("cookie: %+v", c)
	}
	// the nonce is spent: replaying it must not mint a session
	if r, _ = e.do(t, "GET", "/?t="+n, "", nil); len(r.Cookies()) != 0 {
		t.Fatal("a launch link must work exactly once")
	}
}

func TestSessionTokenIsNotAcceptedInURL(t *testing.T) {
	e := newEnv(t)
	if r, _ := e.do(t, "GET", "/?t="+token, "", nil); len(r.Cookies()) != 0 {
		t.Fatal("the session token must never be usable (or expected) in a URL")
	}
	if r, _ := e.do(t, "GET", "/?t=wrong", "", nil); len(r.Cookies()) != 0 {
		t.Fatal("wrong nonce must not set a cookie")
	}
}

func TestLaunchLinkExpires(t *testing.T) {
	e := newEnv(t)
	e.s.mu.Lock()
	e.s.launchTTL = time.Millisecond
	e.s.mu.Unlock()
	n := e.s.LaunchToken()
	time.Sleep(20 * time.Millisecond)
	if r, _ := e.do(t, "GET", "/?t="+n, "", nil); len(r.Cookies()) != 0 {
		t.Fatal("expired link accepted")
	}
}

func TestOutstandingLaunchLinksAreCapped(t *testing.T) {
	e := newEnv(t)
	first := e.s.LaunchToken()
	for i := 0; i < 20; i++ {
		e.s.LaunchToken()
	}
	e.s.mu.Lock()
	n := len(e.s.launch)
	e.s.mu.Unlock()
	if n > 8 {
		t.Fatalf("outstanding links unbounded: %d", n)
	}
	if r, _ := e.do(t, "GET", "/?t="+first, "", nil); len(r.Cookies()) != 0 {
		t.Fatal("evicted link still accepted")
	}
}

func TestSecurityHeaders(t *testing.T) {
	e := newEnv(t)
	r, _ := e.do(t, "GET", "/api/v1/state", "", e.authed(nil))
	for _, h := range []string{"Content-Security-Policy", "X-Content-Type-Options", "X-Frame-Options", "Referrer-Policy"} {
		if r.Header.Get(h) == "" {
			t.Errorf("missing %s", h)
		}
	}
	if strings.Contains(r.Header.Get("Access-Control-Allow-Origin"), "*") {
		t.Fatal("CORS must not be enabled")
	}
}

func getState(t *testing.T, e *env) StateResp {
	t.Helper()
	r, b := e.do(t, "GET", "/api/v1/state", "", e.authed(nil))
	if r.StatusCode != 200 {
		t.Fatalf("state %d %s", r.StatusCode, b)
	}
	var st StateResp
	if err := json.Unmarshal(b, &st); err != nil {
		t.Fatal(err)
	}
	return st
}

func TestStateShowsRealHardware(t *testing.T) {
	e := newEnv(t)
	st := getState(t, e)
	if !st.Supported || st.Phase != store.PhaseDetected || st.Hardware == nil || st.Hardware.CPU.Chip == "" || st.Hardware.Memory.TotalBytes == 0 {
		t.Fatalf("state: %+v", st)
	}
	if st.BenchPlan.Stage != "baseline" || len(st.BenchPlan.Downloads) == 0 {
		t.Fatalf("temp data dir has no runtime, so downloads must be listed: %+v", st.BenchPlan)
	}
}

func TestHealthIsLiveAndShared(t *testing.T) {
	e := newEnv(t)
	get := func() platform.Health {
		r, b := e.do(t, "GET", "/api/v1/health", "", e.authed(nil))
		if r.StatusCode != 200 {
			t.Fatalf("%d %s", r.StatusCode, b)
		}
		var h platform.Health
		if err := json.Unmarshal(b, &h); err != nil {
			t.Fatal(err)
		}
		return h
	}
	h := get()
	if h.Cores <= 0 || h.SpeedLimitPct <= 0 {
		t.Fatalf("not a real sample: %+v", h)
	}
	at := e.s.healthAt
	get()
	if !e.s.healthAt.Equal(at) {
		t.Fatal("a second request inside healthTTL sampled again")
	}
}

func TestPhaseGates(t *testing.T) {
	e := newEnv(t)
	for _, c := range []struct{ m, p string }{{"GET", "/api/v1/tune/plan"}, {"POST", "/api/v1/tune/apply"}} {
		body := ""
		if c.m == "POST" {
			body = `{"keys":[]}`
		}
		r, b := e.do(t, c.m, c.p, body, e.authed(nil))
		if r.StatusCode != 409 || !strings.Contains(string(b), "wrong_phase") {
			t.Errorf("%s %s: %d %s", c.m, c.p, r.StatusCode, b)
		}
	}
}

func TestBenchmarkNeedsConsentForDownloads(t *testing.T) {
	e := newEnv(t)
	r, b := e.do(t, "POST", "/api/v1/benchmark", `{}`, e.authed(nil))
	if r.StatusCode != 400 || !strings.Contains(string(b), "needs_confirmation") || !strings.Contains(string(b), "mlx") {
		t.Fatalf("%d %s", r.StatusCode, b)
	}
	if st := getState(t, e); st.Phase != store.PhaseDetected {
		t.Fatalf("refused request must not change phase: %s", st.Phase)
	}
	if r, _ := e.do(t, "POST", "/api/v1/benchmark", `{"bogus":1}`, e.authed(nil)); r.StatusCode != 400 {
		t.Fatal("unknown fields must be rejected")
	}
}

// Walk the real state machine to baseline_done, then exercise tuning selection without applying anything.
func TestTuneSelectionIsKeysOnlyAndValidated(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	run, _ := e.s.tn.LatestRun(ctx)
	for _, s := range [][2]string{{store.PhaseDetected, store.PhaseBaselineRunning}, {store.PhaseBaselineRunning, store.PhaseBaselineDone}} {
		if err := e.s.tn.SetPhase(ctx, run.ID, s[0], s[1], ""); err != nil {
			t.Fatal(err)
		}
	}
	r, b := e.do(t, "GET", "/api/v1/tune/plan", "", e.authed(nil))
	if r.StatusCode != 200 {
		t.Fatalf("plan: %d %s", r.StatusCode, b)
	}
	// hostile / malformed selections
	for _, body := range []string{
		`{"keys":["rm -rf /"]}`,
		`{"keys":["gpu.wired_limit.persist"]}`,       // dependency (and maybe unavailable)
		`{"keys":["gpu.wired_limit"],"value":99999}`, // values from the client are not accepted
		`{"keys":"gpu.wired_limit"}`,
	} {
		r, b := e.do(t, "POST", "/api/v1/tune/apply", body, e.authed(nil))
		if r.StatusCode != 400 {
			t.Errorf("%s -> %d %s", body, r.StatusCode, b)
		}
	}
	if st := getState(t, e); st.Phase != store.PhaseBaselineDone || len(st.Changes) != 0 {
		t.Fatalf("rejected requests must change nothing: %+v", st.Phase)
	}
	// declining everything is valid and advances to tune_reviewed
	r, b = e.do(t, "POST", "/api/v1/tune/apply", `{"keys":[]}`, e.authed(nil))
	if r.StatusCode != 200 || getState(t, e).Phase != store.PhaseTuneReviewed {
		t.Fatalf("no-change path: %d %s", r.StatusCode, b)
	}
	// recommendations are still locked until the re-run completes
	if r, _ := e.do(t, "GET", "/api/v1/recommendations", "", e.authed(nil)); r.StatusCode != 409 {
		t.Fatal("recommendations unlocked too early")
	}
}

func TestEventsStreamReplaysHistory(t *testing.T) {
	e := newEnv(t)
	e.s.jobs.emit("benchmark", bench.Event{Level: "info", Message: "hello from the job"})
	req, _ := http.NewRequest("GET", e.ts.URL+"/api/v1/events", nil)
	req.Header.Set("Cookie", CookieName+"="+token)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	resp, err := http.DefaultClient.Do(req.WithContext(ctx))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content-type %q", ct)
	}
	sc := bufio.NewScanner(resp.Body)
	found := false
	for sc.Scan() {
		if strings.Contains(sc.Text(), "hello from the job") {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("history not replayed")
	}
}

func TestSingleJobAtATime(t *testing.T) {
	j := newJobs()
	block := make(chan struct{})
	if err := j.start(context.Background(), "a", func(ctx context.Context, emit bench.Emit) error { <-block; return nil }, nil); err != nil {
		t.Fatal(err)
	}
	if err := j.start(context.Background(), "b", func(ctx context.Context, emit bench.Emit) error { return nil }, nil); err != ErrBusy {
		t.Fatalf("second job: %v", err)
	}
	if !j.stop() {
		t.Fatal("stop")
	}
	close(block)
	time.Sleep(50 * time.Millisecond)
	if err := j.start(context.Background(), "c", func(ctx context.Context, emit bench.Emit) error { return nil }, nil); err != nil {
		t.Fatalf("after finish: %v", err)
	}
}

func TestStaticUIMissingBuildIsExplicit(t *testing.T) {
	e := newEnv(t)
	r, b := e.do(t, "GET", "/", "", nil)
	// either a real build is embedded (200) or we get the explicit not-built notice; never a blank page
	if r.StatusCode == 503 && !strings.Contains(string(b), "not built") {
		t.Fatalf("%d %s", r.StatusCode, b)
	}
}

func TestInterruptedRunIsRecoveredOnStart(t *testing.T) {
	t.Setenv("AITUNER_DATA_DIR", t.TempDir())
	db, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mk := func() *Server {
		s, err := New(context.Background(), Config{Store: db, Tenant: "local", Platform: platform.Current(), Token: token, DataDir: t.TempDir(),
			CanIRun: canirun.New(), HF: hf.New(), Ollama: bench.NewOllama(), Runner: tune.NewRunner()})
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	s := mk()
	ctx := context.Background()
	r, _ := s.tn.LatestRun(ctx)
	if err := s.tn.SetPhase(ctx, r.ID, store.PhaseDetected, store.PhaseBaselineRunning, ""); err != nil { // simulate a kill mid-benchmark
		t.Fatal(err)
	}
	s2 := mk()
	if ph, _ := s2.Status(); ph != store.PhaseDetected {
		t.Fatalf("still stuck: %s", ph)
	}
}

func (e *env) json(t *testing.T, method, path, body string, want int) map[string]any {
	t.Helper()
	r, b := e.do(t, method, path, body, e.authed(nil))
	if r.StatusCode != want {
		t.Fatalf("%s %s -> %d, want %d: %s", method, path, r.StatusCode, want, b)
	}
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	return m
}

func TestSettingsDefaultValidationAndReset(t *testing.T) {
	e := newEnv(t)
	home, _ := filepath.EvalSymlinks(os.Getenv("HOME")) // the API reports canonical (symlink-resolved) paths
	m := e.json(t, "GET", "/api/v1/settings", "", 200)
	if m["is_default"] != true || m["models_dir"] != filepath.Join(home, "Models") {
		t.Fatalf("default: %v", m)
	}
	info := m["info"].(map[string]any)
	if info["writable"] != true || info["free_bytes"].(float64) <= 0 {
		t.Fatalf("info: %v", info)
	}
	for _, bad := range []string{"/etc", "/tmp/x", "~/.ssh", "~", "relative", "~/Library/x", "../../etc", "~/Models/.hidden"} {
		body, _ := json.Marshal(map[string]string{"models_dir": bad})
		r, b := e.do(t, "PUT", "/api/v1/settings", string(body), e.authed(nil))
		if r.StatusCode != 400 || !strings.Contains(string(b), "bad_folder") {
			t.Errorf("%q -> %d %s", bad, r.StatusCode, b)
		}
	}
	m = e.json(t, "PUT", "/api/v1/settings", `{"models_dir":"~/LLMs/mlx"}`, 200)
	if m["is_default"] != false || !strings.HasSuffix(m["models_dir"].(string), "/LLMs/mlx") {
		t.Fatalf("saved: %v", m)
	}
	if fi, err := os.Stat(m["models_dir"].(string)); err != nil || !fi.IsDir() {
		t.Fatal("the folder must be created and proven writable on save")
	}
	if g := e.json(t, "GET", "/api/v1/settings", "", 200); g["models_dir"] != m["models_dir"] {
		t.Fatalf("not persisted: %v", g)
	}
	if m = e.json(t, "PUT", "/api/v1/settings", `{"models_dir":""}`, 200); m["is_default"] != true {
		t.Fatalf("reset: %v", m)
	}
	e.json(t, "PUT", "/api/v1/settings", `{"models_dir":"~/x","extra":1}`, 400) // unknown fields refused
	if r, _ := e.do(t, "PUT", "/api/v1/settings", `{"models_dir":"~/x"}`, map[string]string{"Cookie": CookieName + "=" + token}); r.StatusCode != 403 {
		t.Fatalf("Origin required on PUT: %d", r.StatusCode)
	}
}

func TestDownloadsAreRestrictedToOfferedRepos(t *testing.T) {
	e := newEnv(t)
	post := func(repo string, want int) map[string]any {
		body, _ := json.Marshal(map[string]string{"repo": repo})
		return e.json(t, "POST", "/api/v1/downloads", string(body), want)
	}
	for _, bad := range []string{"", "noslash", "a/b; rm -rf ~", "../../etc/passwd", "a/b c"} {
		post(bad, 400) // never reaches any lookup
	}
	if m := post("mlx-community/Qwen3-8B-4bit", 403); m["error"] != "not_offered" { // never recommended
		t.Fatalf("%v", m)
	}
	e.s.rememberOffered(&reco.Output{Groups: map[string][]reco.Candidate{"code": {{Runtime: "mlx", Repo: "mlx-community/Qwen3-8B-4bit",
		Variants: []reco.Variant{{Repo: "someone/Qwen3-8B-abliterated-4bit"}}}, {Runtime: "mflux", Repo: "Z-Image-Turbo"}}}})
	if m := post("mlx-community/Qwen3-8B-4bit", 409); m["error"] != "no_runtime" { // offered, but the temp data dir has no MLX runtime
		t.Fatalf("%v", m)
	}
	if m := post("someone/Qwen3-8B-abliterated-4bit", 409); m["error"] != "no_runtime" {
		t.Fatalf("variants are offered too: %v", m)
	}
	post("Z-Image-Turbo", 400) // not a repo id at all; image models are not downloadable through this path
	l := e.json(t, "GET", "/api/v1/downloads", "", 200)
	if l["active"] != false || len(l["items"].([]any)) != 0 || l["models_dir"] == "" {
		t.Fatalf("%v", l)
	}
	e.json(t, "POST", "/api/v1/downloads/cancel", `{"repo":"a/b"}`, 200)
}

// seed puts real stored results on a run, exactly as a finished benchmark would.
func seed(t *testing.T, e *env, runID, stage string, gen float64) {
	t.Helper()
	ctx := context.Background()
	for _, r := range []store.Result{
		{Stage: stage, Suite: "llm", Engine: "mlx", Metric: "generation_tps", Value: gen, Unit: "tok/s", Trials: json.RawMessage(fmt.Sprintf("[%v,%v,%v]", gen-1, gen, gen+1))},
		{Stage: stage, Suite: "gpu", Engine: "mlx", Metric: "mem_bandwidth", Value: 355, Unit: "GB/s", Trials: json.RawMessage("[354,355,356]")},
		{Stage: stage, Suite: "gpu", Engine: "mlx", Metric: "sustained_matmul_fp16", Value: 7, Unit: "TFLOPS", Trials: json.RawMessage("[7,7,7,7,7,7,7,7,7]")},
	} {
		if err := e.s.tn.AddResult(ctx, runID, r); err != nil {
			t.Fatal(err)
		}
	}
	meta, _ := json.Marshal(benchMeta{Warnings: []string{"seeded warning"}})
	if err := e.s.tn.CachePut(ctx, "bench_meta", runID+"|"+stage, meta); err != nil {
		t.Fatal(err)
	}
}

func TestReportHistoryCompareAndExports(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	r1, _ := e.s.tn.LatestRun(ctx)
	if _, b := e.do(t, "GET", "/api/v1/report", "", e.authed(nil)); !strings.Contains(string(b), "no_results") {
		t.Fatalf("a run without results must say so: %s", b)
	}
	seed(t, e, r1.ID, "baseline", 130)
	seed(t, e, r1.ID, "tuned", 140)
	if err := e.s.ensureRun(ctx, true); err != nil { // a second run
		t.Fatal(err)
	}
	r2, _ := e.s.tn.LatestRun(ctx)
	seed(t, e, r2.ID, "baseline", 120)

	runs := e.json(t, "GET", "/api/v1/runs", "", 200)["runs"].([]any)
	if len(runs) != 2 || runs[0].(map[string]any)["id"] != r2.ID {
		t.Fatalf("history newest first: %v", runs)
	}
	h := runs[1].(map[string]any)
	if h["stage"] != "tuned" || h["headline"].(map[string]any)["mlx_generation_tps"].(float64) != 140 || !strings.Contains(h["machine"].(string), ",") {
		t.Fatalf("summary of the tuned run: %v", h)
	}

	rep := e.json(t, "GET", "/api/v1/report?run="+r1.ID, "", 200)
	stages := rep["stages"].(map[string]any)
	if len(stages) != 2 || len(rep["compare"].([]any)) == 0 {
		t.Fatalf("report: %v", rep)
	}
	base := stages["baseline"].(map[string]any)
	if w := base["warnings"].([]any); len(w) != 1 || w[0] != "seeded warning" {
		t.Fatalf("warnings must travel with the report: %v", base["warnings"])
	}
	derived := map[string]bool{}
	for _, d := range base["derived"].([]any) {
		derived[d.(map[string]any)["key"].(string)] = true
	}
	if !derived["sustained_throttle_pct"] {
		t.Fatalf("derived insight missing: %v", derived)
	}
	first := base["metrics"].([]any)[0].(map[string]any)
	if first["label"] == "" || first["label"] == nil {
		t.Fatalf("metrics must be labelled by the server: %v", first)
	}

	r, b := e.do(t, "GET", "/api/v1/report?run="+r1.ID+"&format=md&download=1", "", e.authed(nil))
	if r.StatusCode != 200 || !strings.HasPrefix(r.Header.Get("Content-Type"), "text/markdown") || !strings.Contains(r.Header.Get("Content-Disposition"), "aituner-report-"+r1.ID[:8]+".md") || !strings.Contains(string(b), "## Before and after") {
		t.Fatalf("markdown: %d %v\n%s", r.StatusCode, r.Header, b)
	}
	r, b = e.do(t, "GET", "/api/v1/report?run="+r1.ID+"&format=csv", "", e.authed(nil))
	if r.StatusCode != 200 || !strings.HasPrefix(string(b), "stage,suite,engine,metric") || strings.Contains(r.Header.Get("Content-Disposition"), "attachment") {
		t.Fatalf("csv: %d %s", r.StatusCode, b)
	}

	cmp := e.json(t, "GET", "/api/v1/compare?a="+r1.ID+"&b="+r2.ID, "", 200)
	rows := cmp["rows"].([]any)
	var gen map[string]any
	for _, x := range rows {
		if x.(map[string]any)["metric"] == "generation_tps" {
			gen = x.(map[string]any)
		}
	}
	if cmp["a_stage"] != "tuned" || cmp["b_stage"] != "baseline" || gen == nil || gen["verdict"] != "slower" || gen["label"] == "" {
		t.Fatalf("compare (tuned 140 -> baseline 120 must read slower): %v", cmp)
	}
}

func TestReportEndpointsRejectBadInput(t *testing.T) {
	e := newEnv(t)
	for _, q := range []string{"/api/v1/report?run=../../etc", "/api/v1/report?run=zzzz", "/api/v1/report?run=' OR 1=1--", "/api/v1/compare?a=nothex&b=nothex", "/api/v1/compare?a=" + strings.Repeat("a", 32)} {
		if r, b := e.do(t, "GET", q, "", e.authed(nil)); r.StatusCode != 400 && r.StatusCode != 404 {
			t.Errorf("%s -> %d %s", q, r.StatusCode, b)
		}
	}
	ctx := context.Background()
	run, _ := e.s.tn.LatestRun(ctx)
	seed(t, e, run.ID, "baseline", 100)
	if r, _ := e.do(t, "GET", "/api/v1/report?run="+run.ID+"&format=exe", "", e.authed(nil)); r.StatusCode != 400 {
		t.Fatalf("unknown format: %d", r.StatusCode)
	}
	if r, _ := e.do(t, "GET", "/api/v1/report", "", nil); r.StatusCode != 401 {
		t.Fatalf("reports need auth: %d", r.StatusCode)
	}
	// another tenant's run id is simply not found
	other := e.s.cfg.Store.ForTenant("someone-else")
	if _, err := other.GetRun(ctx, run.ID); err == nil {
		t.Fatal("tenant isolation broken")
	}
	// state carries the pinned-bar headline
	st := getState(t, e)
	if st.Headline["baseline"].MLXGen != 100 || st.Headline["baseline"].GPUBW != 355 {
		t.Fatalf("headline: %+v", st.Headline)
	}
}

func TestServeEndpointsAreGatedAndNeverServeArbitraryPaths(t *testing.T) {
	e := newEnv(t)
	post := func(path, body string, want int) map[string]any { return e.json(t, "POST", path, body, want) }
	st := e.json(t, "GET", "/api/v1/serve", "", 200)
	if st["server"].(map[string]any)["state"] != "stopped" || st["runtime"].(map[string]any)["ready"] != false || st["gateway"] != nil || len(st["models"].([]any)) != 0 {
		t.Fatalf("idle state: %v", st)
	}
	if hint := st["key_hint"].(string); !strings.HasPrefix(hint, "…") || len(hint) > 8 {
		t.Fatalf("polled state must carry only a hint of the key, never the key: %q", hint)
	}
	if strings.Contains(fmt.Sprint(st), e.s.gwKey) {
		t.Fatal("the gateway key leaked into the polled state")
	}
	key := e.json(t, "GET", "/api/v1/serve/key", "", 200)["key"].(string)
	if key != e.s.gwKey || !strings.HasPrefix(key, "aituner-") {
		t.Fatalf("key: %q", key)
	}
	if r, _ := e.do(t, "GET", "/api/v1/serve/key", "", nil); r.StatusCode != 401 {
		t.Fatalf("the key needs auth: %d", r.StatusCode)
	}
	for _, bad := range []string{`{"repo":""}`, `{"repo":"a/b; rm -rf ~"}`, `{"repo":"../../etc/passwd"}`, `{"repo":"/etc"}`} {
		post("/api/v1/serve/start", bad, 400)
	}
	post("/api/v1/serve/start", `{"repo":"mlx-community/Qwen3-8B-4bit","model_dir":"/etc"}`, 400) // unknown fields refused
	if m := post("/api/v1/serve/start", `{"repo":"mlx-community/Qwen3-8B-4bit"}`, 409); m["error"] != "no_runtime" {
		t.Fatalf("no MLX runtime in the temp data dir: %v", m)
	}
	if lines := e.json(t, "GET", "/api/v1/serve/logs", "", 200)["lines"]; lines == nil {
		t.Fatal("logs must be an array")
	}
	post("/api/v1/serve/stop", `{}`, 200) // stopping nothing is fine
	e.json(t, "POST", "/api/v1/serve/runtime", `{"x":1}`, 400)
}

// A loaded model competes for the GPU and would corrupt benchmark numbers, so the benchmark is refused meanwhile.
func TestBenchmarkIsRefusedWhileAModelIsLoaded(t *testing.T) {
	e := newEnv(t)
	dir := t.TempDir()
	bin := filepath.Join(dir, "mlx_lm.server")
	os.WriteFile(bin, []byte("#!/bin/sh\nexec sleep 300\n"), 0o755) // a real process that never becomes ready: state stays "starting"
	if err := e.s.serve.Start(context.Background(), serve.Spec{Bin: bin, ModelDir: dir, Repo: "x/y"}); err != nil {
		t.Fatal(err)
	}
	defer e.s.Close()
	m := e.json(t, "POST", "/api/v1/benchmark", `{"confirm_downloads":true}`, 409)
	if m["error"] != "model_running" {
		t.Fatalf("%v", m)
	}
	if st := getState(t, e); st.Serving == nil || st.Serving.Repo != "x/y" || st.Phase != store.PhaseDetected {
		t.Fatalf("serving must show in state and the run must be untouched: %+v phase=%s", st.Serving, st.Phase)
	}
	e.s.serve.Stop()
	if st := getState(t, e); st.Serving != nil {
		t.Fatalf("stopped: %+v", st.Serving)
	}
}

func TestConnectEndpointsAreGatedAndPlansAreComplete(t *testing.T) {
	e := newEnv(t)
	c := e.json(t, "GET", "/api/v1/connect", "", 200)
	items := c["items"].([]any)
	if len(items) != 5 || c["model_running"] != false {
		t.Fatalf("%v", c)
	}
	ids := map[string]bool{}
	for _, it := range items {
		m := it.(map[string]any)
		ids[m["id"].(string)] = true
		plan := m["plan"].(map[string]any)
		if plan["title"] == "" || len(plan["steps"].([]any)) == 0 || len(plan["will_not_touch"].([]any)) == 0 {
			t.Errorf("incomplete plan: %v", plan)
		}
	}
	for _, id := range []string{"claude", "vscode", "neovim", "vim-tmux", "openai"} {
		if !ids[id] {
			t.Errorf("missing integration %s", id)
		}
	}
	if strings.Contains(fmt.Sprint(c), e.s.gwKey) {
		t.Fatal("the gateway key must not appear in the connect listing")
	}
	post := func(path, body string, want int) map[string]any { return e.json(t, "POST", path, body, want) }
	post("/api/v1/connect/setup", `{"id":"claude"}`, 400)                  // must confirm
	post("/api/v1/connect/setup", `{"id":"claude","confirm":true}`, 409)   // no model is being served
	post("/api/v1/connect/setup", `{"id":"rm -rf /","confirm":true}`, 404) // only known integrations
	post("/api/v1/connect/setup", `{"id":"claude","confirm":true,"x":1}`, 400)
	post("/api/v1/connect/launch", `{"id":"claude","project":"/etc"}`, 409) // no model first
	if m := post("/api/v1/connect/setup", `{"id":"claude","confirm":true}`, 409); m["error"] != "no_model" {
		t.Fatalf("%v", m)
	}
	if r, _ := e.do(t, "POST", "/api/v1/connect/setup", `{"id":"claude","confirm":true}`, map[string]string{"Cookie": CookieName + "=" + token}); r.StatusCode != 403 {
		t.Fatalf("Origin required: %d", r.StatusCode)
	}
}

// Models are offered right after detection (no benchmark needed); without an MLX runtime the API says so
// rather than pretending, and a download of a repo that was never offered is refused.
func TestRecommendationsAndDownloadsNeedRuntimeNotBenchmark(t *testing.T) {
	e := newEnv(t)
	r, b := e.do(t, "GET", "/api/v1/recommendations", "", e.authed(nil))
	if r.StatusCode != 409 || !strings.Contains(string(b), "no_runtime") {
		t.Fatalf("recommendations: %d %s", r.StatusCode, b)
	}
	r, b = e.do(t, "POST", "/api/v1/downloads", `{"repo":"mlx-community/never-offered"}`, e.authed(nil))
	if r.StatusCode != 403 || !strings.Contains(string(b), "not_offered") {
		t.Fatalf("download: %d %s", r.StatusCode, b)
	}
}

// Every launch starts a fresh run, so the pinned numbers fall back to the newest earlier run that was measured
// (and say so) instead of going blank; once the new run has its own measurements they take over.
func TestPinnedStatsFallBackToPreviousRun(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	r1, _ := e.s.tn.LatestRun(ctx)
	seed(t, e, r1.ID, "baseline", 100)
	if err := e.s.ensureRun(ctx, true); err != nil {
		t.Fatal(err)
	}
	st := getState(t, e)
	if st.Phase != store.PhaseDetected || st.Headline["baseline"].MLXGen != 100 || st.HeadlineFrom == 0 {
		t.Fatalf("fallback: phase=%s %+v from=%d", st.Phase, st.Headline, st.HeadlineFrom)
	}
	r2, _ := e.s.tn.LatestRun(ctx)
	seed(t, e, r2.ID, "baseline", 120)
	if st = getState(t, e); st.Headline["baseline"].MLXGen != 120 || st.HeadlineFrom != 0 {
		t.Fatalf("own measurements must win: %+v from=%d", st.Headline, st.HeadlineFrom)
	}
}

// A fresh run has no benchmark, but the model's usable context (what the Claude launcher reports) still needs the
// GPU budget: it must come from Metal directly rather than being 0.
func TestBudgetWithoutBenchmarkComesFromMetal(t *testing.T) {
	e := newEnv(t)
	hw := e.s.hardware()
	if !hw.Software.MLX.Ready {
		t.Skip("no MLX runtime on this machine")
	}
	run, _ := e.s.tn.LatestRun(context.Background())
	if b := e.s.budgetGB(context.Background(), run.ID, hw); b < 1 {
		t.Fatalf("budget %v GB with no benchmark", b)
	}
}

func TestModelBenchNeedsRuntimeAndListsDownloads(t *testing.T) {
	e := newEnv(t)
	r, b := e.do(t, "POST", "/api/v1/modelbench", `{}`, e.authed(nil))
	if r.StatusCode != 409 || !strings.Contains(string(b), "no_runtime") {
		t.Fatalf("start: %d %s", r.StatusCode, b)
	}
	l := e.json(t, "GET", "/api/v1/modelbench", "", 200)
	if items, ok := l["items"].([]any); !ok || len(items) != 0 {
		t.Fatalf("list: %v", l)
	}
}

// Restarting aituner must not pile up empty runs: an untouched run is reused, a used one is not.
func TestLaunchReusesAnUntouchedRun(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	r1, _ := e.s.tn.LatestRun(ctx)
	for i := 0; i < 3; i++ {
		if err := e.s.ensureLaunchRun(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if r, _ := e.s.tn.LatestRun(ctx); r.ID != r1.ID {
		t.Fatal("an untouched run was not reused")
	}
	seed(t, e, r1.ID, "baseline", 100)
	if err := e.s.ensureLaunchRun(ctx); err != nil {
		t.Fatal(err)
	}
	if r, _ := e.s.tn.LatestRun(ctx); r.ID == r1.ID {
		t.Fatal("a measured run must be followed by a fresh one")
	}
}
