package api

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tonynv/aituner/internal/bench"
	"github.com/tonynv/aituner/internal/canirun"
	"github.com/tonynv/aituner/internal/hf"
	"github.com/tonynv/aituner/internal/platform"
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
	for _, p := range []string{"/api/v1/state", "/api/v1/recommendations", "/api/v1/tune/plan", "/api/v1/events"} {
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

func TestTokenExchangeSetsHardenedCookie(t *testing.T) {
	e := newEnv(t)
	r, _ := e.do(t, "GET", "/?t="+token, "", nil)
	if r.StatusCode != 302 || r.Header.Get("Location") != "/" {
		t.Fatalf("redirect: %d %q", r.StatusCode, r.Header.Get("Location"))
	}
	c := r.Cookies()
	if len(c) != 1 || !c[0].HttpOnly || c[0].SameSite != http.SameSiteStrictMode || c[0].Value != token {
		t.Fatalf("cookie: %+v", c)
	}
	r, _ = e.do(t, "GET", "/?t=wrong", "", nil)
	if len(r.Cookies()) != 0 {
		t.Fatal("wrong token must not set a cookie")
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

func TestStateShowsRealHardwareAndNoRecommendations(t *testing.T) {
	e := newEnv(t)
	st := getState(t, e)
	if !st.Supported || st.Phase != store.PhaseDetected || st.Hardware == nil || st.Hardware.CPU.Chip == "" || st.Hardware.Memory.TotalBytes == 0 {
		t.Fatalf("state: %+v", st)
	}
	if st.Unlocked {
		t.Fatal("recommendations must be locked at the start")
	}
	if st.BenchPlan.Stage != "baseline" || len(st.BenchPlan.Downloads) == 0 {
		t.Fatalf("temp data dir has no runtime, so downloads must be listed: %+v", st.BenchPlan)
	}
}

func TestPhaseGates(t *testing.T) {
	e := newEnv(t)
	for _, c := range []struct{ m, p string }{{"GET", "/api/v1/recommendations"}, {"GET", "/api/v1/tune/plan"}, {"POST", "/api/v1/tune/apply"}} {
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
