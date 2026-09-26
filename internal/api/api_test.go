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
	"sync"
	"testing"
	"time"

	"github.com/tonynv/aituner/internal/bench"
	"github.com/tonynv/aituner/internal/canirun"
	"github.com/tonynv/aituner/internal/download"
	"github.com/tonynv/aituner/internal/hf"
	"github.com/tonynv/aituner/internal/httpx"
	"github.com/tonynv/aituner/internal/platform"
	"github.com/tonynv/aituner/internal/reco"
	"github.com/tonynv/aituner/internal/serve"
	"github.com/tonynv/aituner/internal/store"
	"github.com/tonynv/aituner/internal/tune"
	"github.com/tonynv/aituner/internal/update"
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
	for _, p := range []string{"/api/v1/state", "/api/v1/health", "/api/v1/monitor", "/api/v1/services", "/api/v1/bootstrap", "/api/v1/update", "/api/v1/storage", "/api/v1/reset", "/api/v1/recommendations", "/api/v1/tune/plan", "/api/v1/events"} {
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

func TestMonitorStreamsLiveSamples(t *testing.T) {
	e := newEnv(t)
	var m monitorResp
	var seq int64
	deadline := time.Now().Add(8 * time.Second)
	for len(m.Samples) == 0 && time.Now().Before(deadline) {
		r, b := e.do(t, "GET", "/api/v1/monitor?tools=1", "", e.authed(nil))
		if r.StatusCode != 200 {
			t.Fatalf("%d %s", r.StatusCode, b)
		}
		m = monitorResp{}
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatal(err)
		}
		time.Sleep(200 * time.Millisecond)
	}
	if len(m.Samples) == 0 || m.Source == "" || m.Model.State != serve.StateStopped || len(m.Tools) != 3 {
		t.Fatalf("%+v", m)
	}
	virtual := strings.HasPrefix(e.s.hardware().Model.Identifier, "VirtualMac") // no GPU counters in a VM
	if s := m.Samples[0]; s.RAMTotal <= 0 || (s.GPUPct < 0 && !virtual) {
		t.Fatalf("not a real sample: %+v", s)
	}
	seq = m.Samples[len(m.Samples)-1].Seq
	_, b := e.do(t, "GET", "/api/v1/monitor?since="+strconv.FormatInt(seq, 10), "", e.authed(nil))
	m = monitorResp{}
	json.Unmarshal(b, &m)
	for _, s := range m.Samples {
		if s.Seq <= seq {
			t.Fatalf("since=%d returned seq %d", seq, s.Seq)
		}
	}
	if m.Tools != nil {
		t.Fatal("tools listed without tools=1")
	}
	for _, bad := range []string{"-1", "x"} {
		if r, _ := e.do(t, "GET", "/api/v1/monitor?since="+bad, "", e.authed(nil)); r.StatusCode != 400 {
			t.Errorf("since=%s: %d", bad, r.StatusCode)
		}
	}
}

func TestMonitorToolRequests(t *testing.T) {
	e := newEnv(t)
	for body, want := range map[string]int{
		`{"id":"gpustat","action":"open"}`:      404,
		`{"id":"mactop","action":"install"}`:    400, // needs confirmation
		`{"id":"mactop","action":"uninstall"}`:  400,
		`{"id":"mactop","action":"open","x":1}`: 400, // unknown fields are refused
	} {
		if r, b := e.do(t, "POST", "/api/v1/monitor/tool", body, e.authed(nil)); r.StatusCode != want {
			t.Errorf("%s: %d %s", body, r.StatusCode, b)
		}
	}
	if r, _ := e.do(t, "POST", "/api/v1/monitor/tool", `{"id":"mactop","action":"open"}`, map[string]string{"Cookie": CookieName + "=" + token}); r.StatusCode != 403 {
		t.Errorf("no Origin on a mutation: %d", r.StatusCode)
	}
}

func TestDetectStreamsProbesThenState(t *testing.T) {
	e := newEnv(t)
	r, b := e.do(t, "POST", "/api/v1/detect", "{}", e.authed(map[string]string{"Accept": "application/x-ndjson"}))
	if r.StatusCode != 200 || r.Header.Get("Content-Type") != "application/x-ndjson" {
		t.Fatalf("%d %s", r.StatusCode, r.Header.Get("Content-Type"))
	}
	var probes, health, states int
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	for i, l := range lines {
		var m map[string]json.RawMessage
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Fatalf("line %d: %v", i, err)
		}
		switch {
		case m["probe"] != nil:
			var p platform.Probe
			json.Unmarshal(m["probe"], &p)
			if p.Cmd == "" {
				t.Fatalf("empty probe: %s", l)
			}
			probes++
		case m["health"] != nil:
			health++
		case m["state"] != nil:
			var st StateResp
			if err := json.Unmarshal(m["state"], &st); err != nil || !st.Supported || st.Hardware == nil {
				t.Fatalf("state: %v %s", err, l)
			}
			states++
			if i != len(lines)-1 {
				t.Fatal("state is not the last line")
			}
		default:
			t.Fatalf("unexpected line %s", l)
		}
	}
	if probes < 5 || health != 1 || states != 1 {
		t.Fatalf("probes=%d health=%d states=%d", probes, health, states)
	}
	// without the Accept header it is the plain JSON state, as before
	r, b = e.do(t, "POST", "/api/v1/detect", "{}", e.authed(nil))
	var st StateResp
	if r.StatusCode != 200 || json.Unmarshal(b, &st) != nil || !st.Supported {
		t.Fatalf("%d %s", r.StatusCode, b)
	}
}

func TestClearModelsAndReports(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	prev := e.json(t, "GET", "/api/v1/reset", "", 200)
	root := prev["models_dir"].(string)
	// one real aituner download (marker), one folder the user made themselves (no marker), and a symlinked "model"
	mk := func(repo string, marked bool) string {
		d := filepath.Join(root, filepath.FromSlash(repo))
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		os.WriteFile(filepath.Join(d, "model.safetensors"), make([]byte, 1000), 0o644)
		if marked {
			os.WriteFile(filepath.Join(d, download.MarkerName), []byte(`{"repo":"`+repo+`","bytes":1000,"files":1}`), 0o644)
		}
		return d
	}
	ours := mk("mlx-community/Tiny-4bit", true)
	mine := mk("me/my-own-model", false)
	outside := t.TempDir()
	os.WriteFile(filepath.Join(outside, download.MarkerName), []byte(`{"repo":"evil/link","bytes":1,"files":1}`), 0o644)
	os.MkdirAll(filepath.Join(root, "evil"), 0o755)
	os.Symlink(outside, filepath.Join(root, "evil", "link"))

	r1, _ := e.s.tn.LatestRun(ctx)
	seed(t, e, r1.ID, "baseline", 100)

	prev = e.json(t, "GET", "/api/v1/reset", "", 200)
	models := prev["models"].([]any)
	if len(models) != 1 || models[0].(map[string]any)["repo"] != "mlx-community/Tiny-4bit" || prev["measured_runs"].(float64) != 1 {
		t.Fatalf("preview must list only aituner's own download: %v", prev)
	}
	for body, want := range map[string]int{`{"models":true}`: 400, `{"confirm":true}`: 400} {
		if r, b := e.do(t, "POST", "/api/v1/reset", body, e.authed(nil)); r.StatusCode != want {
			t.Errorf("%s: %d %s", body, r.StatusCode, b)
		}
	}
	// an applied tuning change blocks clearing reports (its record is what reverts it)
	id, _ := e.s.tn.AddTuneChange(ctx, r1.ID, "k", "0", "1")
	if r, _ := e.do(t, "POST", "/api/v1/reset", `{"reports":true,"confirm":true}`, e.authed(nil)); r.StatusCode != 409 {
		t.Fatalf("cleared reports with an applied change: %d", r.StatusCode)
	}
	e.s.tn.MarkReverted(ctx, id)

	res := e.json(t, "POST", "/api/v1/reset", `{"models":true,"reports":true,"confirm":true}`, 200)
	if d := res["models_deleted"].([]any); len(d) != 1 || res["runs_deleted"].(float64) < 1 {
		t.Fatalf("%v", res)
	}
	if _, err := os.Stat(ours); !os.IsNotExist(err) {
		t.Fatal("aituner's model is still there")
	}
	if _, err := os.Stat(filepath.Join(mine, "model.safetensors")); err != nil {
		t.Fatal("deleted a folder aituner did not download")
	}
	if _, err := os.Stat(filepath.Join(outside, download.MarkerName)); err != nil {
		t.Fatal("followed a symlink out of the models folder")
	}
	st := getState(t, e)
	if st.Run == nil || st.Phase != store.PhaseDetected {
		t.Fatalf("a fresh run must exist after clearing: %+v", st.Run)
	}
	if def, b := e.do(t, "GET", "/api/v1/report", "", e.authed(nil)); def.StatusCode != 409 || !strings.Contains(string(b), "no_results") {
		t.Fatalf("reports must be empty: %d %s", def.StatusCode, b)
	}
}

func TestStorageFoldersSaveAndReveal(t *testing.T) {
	e := newEnv(t)
	var opened []string
	e.s.cfg.Open = func(p string) error { opened = append(opened, p); return nil }
	st := e.json(t, "GET", "/api/v1/storage", "", 200)
	rep := st["reports"].(map[string]any)
	kb := st["knowledge"].(map[string]any)
	if rep["is_default"] != true || filepath.Base(rep["dir"].(string)) != "Reports" || filepath.Base(kb["dir"].(string)) != "KnowledgeBase" || st["data"].(map[string]any)["dir"] == "" {
		t.Fatalf("%v", st)
	}
	if rep["info"].(map[string]any)["exists"] != false {
		t.Fatal("looking at Storage must not create folders")
	}
	if r, _ := e.do(t, "POST", "/api/v1/storage/reveal", `{"which":"knowledge"}`, e.authed(nil)); r.StatusCode != 409 {
		t.Fatalf("revealed a folder that does not exist: %d", r.StatusCode)
	}
	if r, _ := e.do(t, "PUT", "/api/v1/storage/secrets", `{"dir":"~/x"}`, e.authed(nil)); r.StatusCode != 404 {
		t.Fatalf("unknown folder kind: %d", r.StatusCode)
	}
	if kd := e.json(t, "PUT", "/api/v1/storage/knowledge", `{"dir":"~/KB-x"}`, 200)["knowledge"].(map[string]any); filepath.Base(kd["dir"].(string)) != "KB-x" || kd["info"].(map[string]any)["exists"] != true {
		t.Fatalf("knowledge folder: %v", kd)
	}
	home, _ := os.UserHomeDir()
	for _, bad := range []string{"/etc", "~/Library/x", "~/.hidden", "relative/path"} {
		if r, _ := e.do(t, "PUT", "/api/v1/storage/reports", `{"dir":"`+bad+`"}`, e.authed(nil)); r.StatusCode != 400 {
			t.Errorf("accepted %s: %d", bad, r.StatusCode)
		}
	}
	st = e.json(t, "PUT", "/api/v1/storage/reports", `{"dir":"~/Reports-x"}`, 200)
	want, _ := filepath.EvalSymlinks(home)
	if d := st["reports"].(map[string]any)["dir"].(string); d != filepath.Join(want, "Reports-x") {
		t.Fatalf("saved %s", d)
	}
	// saving needs results; then it writes all three formats into the folder
	if r, _ := e.do(t, "POST", "/api/v1/report/save", `{}`, e.authed(nil)); r.StatusCode != 409 {
		t.Fatalf("saved an empty report: %d", r.StatusCode)
	}
	run, _ := e.s.tn.LatestRun(context.Background())
	seed(t, e, run.ID, "baseline", 100)
	out := e.json(t, "POST", "/api/v1/report/save", `{}`, 200)
	files := out["files"].([]any)
	if len(files) != 3 {
		t.Fatalf("%v", out)
	}
	for _, f := range files {
		if fi, err := os.Stat(f.(string)); err != nil || fi.Size() == 0 || filepath.Dir(f.(string)) != filepath.Join(want, "Reports-x") {
			t.Fatalf("%v %v", f, err)
		}
	}
	if r, _ := e.do(t, "POST", "/api/v1/report/save", `{"run":"../x"}`, e.authed(nil)); r.StatusCode != 400 {
		t.Fatal("accepted a bad run id")
	}
	e.json(t, "POST", "/api/v1/storage/reveal", `{"which":"reports"}`, 200)
	if len(opened) != 1 || opened[0] != filepath.Join(want, "Reports-x") {
		t.Fatalf("opened %v", opened)
	}
	if r, _ := e.do(t, "POST", "/api/v1/storage/reveal", `{"which":"/etc"}`, e.authed(nil)); r.StatusCode != 400 {
		t.Fatal("revealed an arbitrary path")
	}
	st = e.json(t, "PUT", "/api/v1/storage/reports", `{"dir":""}`, 200)
	if st["reports"].(map[string]any)["is_default"] != true {
		t.Fatal("empty did not reset to the default")
	}
}

func TestServicesAndBootstrapPlan(t *testing.T) {
	e := newEnv(t)
	sv := e.json(t, "GET", "/api/v1/services", "", 200)["services"].([]any)
	ids := map[string]map[string]any{}
	for _, x := range sv {
		m := x.(map[string]any)
		ids[m["id"].(string)] = m
	}
	for _, id := range []string{"mlx", "model", "gateway", "macmon", "ollama"} {
		if ids[id] == nil {
			t.Fatalf("missing %s: %v", id, sv)
		}
	}
	if ids["model"]["active"] != false || ids["gateway"]["active"] != false {
		t.Fatalf("nothing is served in a test: %v", sv)
	}
	// a fresh home has none of the folders and the temp data dir has no MLX: bootstrap lists the folders it will
	// create (by path, for the user to confirm) and offers to install MLX
	plan := e.json(t, "GET", "/api/v1/bootstrap", "", 200)
	steps := plan["steps"].([]any)
	if len(steps) != 3 || steps[0].(map[string]any)["id"] != "folders" || steps[1].(map[string]any)["id"] != "mlx" || steps[1].(map[string]any)["action"] != "install" {
		t.Fatalf("%v", plan)
	}
	items := steps[0].(map[string]any)["items"].([]any)
	if len(items) != 3 {
		t.Fatalf("folders step must list each folder: %v", steps[0])
	}
	for i, name := range []string{"Models", "Reports", "KnowledgeBase"} {
		if filepath.Base(items[i].(string)) != name {
			t.Fatalf("folder %d: %v", i, items[i])
		}
	}
	if r, _ := e.do(t, "POST", "/api/v1/bootstrap", `{}`, e.authed(nil)); r.StatusCode != 400 {
		t.Fatalf("bootstrap without confirmation: %d", r.StatusCode)
	}
	if r, _ := e.do(t, "POST", "/api/v1/bootstrap", `{"confirm":true,"x":1}`, e.authed(nil)); r.StatusCode != 400 {
		t.Fatalf("unknown field accepted: %d", r.StatusCode)
	}
}

func TestMachineImageIsThisMacsOwnPicture(t *testing.T) {
	e := newEnv(t)
	if _, ok := platform.DeviceIcon(context.Background(), e.s.hardware().Model.Identifier); !ok {
		// e.g. a virtual Mac: macOS has no picture for it, so the UI draws a generic machine
		if r, _ := e.do(t, "GET", "/api/v1/machine/image", "", e.authed(nil)); r.StatusCode != 404 {
			t.Fatalf("no device picture must be a 404: %d", r.StatusCode)
		}
		return
	}
	r, b := e.do(t, "GET", "/api/v1/machine/image", "", e.authed(nil))
	if r.StatusCode != 200 || r.Header.Get("Content-Type") != "image/png" || len(b) < 1000 || string(b[1:4]) != "PNG" {
		t.Fatalf("%d %s %d bytes", r.StatusCode, r.Header.Get("Content-Type"), len(b))
	}
	// cached: the second request serves the same file without converting again
	fi, err := os.Stat(filepath.Join(e.s.cfg.DataDir, "device-"+e.s.hardware().Model.Identifier+".png"))
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("cache: %v %v", err, fi)
	}
	if r2, b2 := e.do(t, "GET", "/api/v1/machine/image", "", e.authed(nil)); r2.StatusCode != 200 || len(b2) != len(b) {
		t.Fatal("cached image differs")
	}
	if r, _ := e.do(t, "GET", "/api/v1/machine/image", "", nil); r.StatusCode != 401 {
		t.Fatalf("unauthenticated: %d", r.StatusCode)
	}
}

func TestAppIconsComeFromInstalledApps(t *testing.T) {
	e := newEnv(t)
	for id, bundle := range appIcons {
		r, b := e.do(t, "GET", "/api/v1/appicon/"+id, "", e.authed(nil))
		if _, err := os.Stat(filepath.Join("/Applications", bundle)); err != nil {
			if r.StatusCode != 404 {
				t.Errorf("%s not installed, want 404: %d", id, r.StatusCode)
			}
			continue
		}
		if r.StatusCode != 200 || string(b[1:4]) != "PNG" {
			t.Errorf("%s: %d %s", id, r.StatusCode, r.Header.Get("Content-Type"))
		}
	}
	for _, bad := range []string{"../etc", "terminal", "%2e%2e"} { // unknown ids 404; ".." is cleaned away (redirect)
		if r, _ := e.do(t, "GET", "/api/v1/appicon/"+bad, "", e.authed(nil)); r.StatusCode == 200 {
			t.Errorf("%s served an icon", bad)
		}
	}
}

// githubStandIn serves a latest-release answer shaped like GitHub's (or 404 when tag is empty) and points the updater
// at it.
func githubStandIn(t *testing.T, e *env, tag *string) {
	t.Helper()
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if *tag == "" {
			http.NotFound(w, r)
			return
		}
		v := strings.TrimPrefix(*tag, "v")
		fmt.Fprintf(w, `{"tag_name":%q,"draft":false,"prerelease":false,"body":"notes","html_url":"https://github.com/tonynv/aituner/releases/tag/%s","assets":[{"name":"aituner-%s.zip","browser_download_url":"https://github.com/x/aituner-%s.zip","size":1},{"name":"SHA256SUMS","browser_download_url":"https://github.com/x/SHA256SUMS","size":1}]}`, *tag, *tag, v, v)
	}))
	t.Cleanup(ts.Close)
	host, _, _ := net.SplitHostPort(strings.TrimPrefix(ts.URL, "https://"))
	c := httpx.New(host)
	c.HTTP.Transport = ts.Client().Transport
	e.s.upd.client = c
	old := update.APIBase
	update.APIBase = ts.URL
	t.Cleanup(func() { update.APIBase = old })
}

func TestUpdateCheckAnnounceSkipAndManual(t *testing.T) {
	e := newEnv(t)
	var mu sync.Mutex
	var lines []string
	e.s.cfg.Notify = func(l string) { mu.Lock(); lines = append(lines, l); mu.Unlock() }
	e.s.cfg.Version = "v0.1.0"
	tag := "v0.2.0"
	githubStandIn(t, e, &tag)
	got := func() []string { mu.Lock(); defer mu.Unlock(); return append([]string(nil), lines...) }

	st := e.json(t, "POST", "/api/v1/update/check", "{}", 200)
	if st["available"] != true || st["current"] != "0.1.0" || st["latest"].(map[string]any)["version"] != "0.2.0" || st["method"] != "manual" {
		t.Fatalf("%v", st)
	}
	e.json(t, "POST", "/api/v1/update/check", "{}", 200) // a second automatic check does not announce again
	if l := got(); len(l) != 1 || l[0] != "update 0.2.0" {
		t.Fatalf("announcements %v", l)
	}
	e.json(t, "PUT", "/api/v1/update/settings", `{"skip":"0.2.0","auto":false}`, 200)
	tag = "v0.2.0"
	e.s.upd.announced = ""
	e.s.CheckForUpdate(context.Background(), false) // skipped: not announced
	if l := got(); len(l) != 1 {
		t.Fatalf("a skipped version was announced: %v", l)
	}
	e.s.CheckForUpdate(context.Background(), true) // a manual check always answers
	if l := got(); len(l) != 2 || l[1] != "update 0.2.0" {
		t.Fatalf("%v", l)
	}
	if st := e.json(t, "GET", "/api/v1/update", "", 200); st["auto"] != false || st["skipped"] != "0.2.0" {
		t.Fatalf("%v", st)
	}
	tag = "" // no release on GitHub
	e.s.CheckForUpdate(context.Background(), true)
	if l := got(); l[len(l)-1] != "uptodate 0.1.0" {
		t.Fatalf("%v", l)
	}
	if r, _ := e.do(t, "PUT", "/api/v1/update/settings", `{"skip":"../x"}`, e.authed(nil)); r.StatusCode != 400 {
		t.Fatalf("bad skip version: %d", r.StatusCode)
	}
}

func TestUpdateInstallRefusedOutsideTheApp(t *testing.T) {
	e := newEnv(t)
	e.s.cfg.Version = "v0.1.0"
	tag := "v0.2.0"
	githubStandIn(t, e, &tag)
	e.s.CheckForUpdate(context.Background(), false)
	if r, _ := e.do(t, "POST", "/api/v1/update/install", `{}`, e.authed(nil)); r.StatusCode != 400 {
		t.Fatalf("install without confirmation: %d", r.StatusCode)
	}
	r, b := e.do(t, "POST", "/api/v1/update/install", `{"confirm":true}`, e.authed(nil))
	if r.StatusCode != 409 || !strings.Contains(string(b), "terminal build") {
		t.Fatalf("a terminal build must never replace itself: %d %s", r.StatusCode, b)
	}
	e.s.cfg.Version = "v0.1.0-4-gabcdef"
	if st := e.json(t, "GET", "/api/v1/update", "", 200); st["available"] != false || st["release"] != false {
		t.Fatalf("a development build is not offered updates: %v", st)
	}
}

// Details and guards only: this must never actually quit or remove the real Ollama on the machine running the tests.
func TestServiceDetailsAndGuards(t *testing.T) {
	e := newEnv(t)
	d := e.json(t, "GET", "/api/v1/services/ollama", "", 200)
	if !strings.Contains(fmt.Sprint(d["info"]), "never installs Ollama") {
		t.Fatalf("%v", d)
	}
	if r, _ := e.do(t, "GET", "/api/v1/services/nope", "", e.authed(nil)); r.StatusCode != 404 {
		t.Fatalf("unknown service: %d", r.StatusCode)
	}
	// no model is served: stopping it is not offered, so it is refused
	if r, _ := e.do(t, "POST", "/api/v1/services/model/stop", `{"confirm":true}`, e.authed(nil)); r.StatusCode != 409 {
		t.Fatalf("an action that is not offered must be refused: %d", r.StatusCode)
	}
	// the temp data dir has no MLX: removing it is not offered either
	if r, _ := e.do(t, "POST", "/api/v1/services/mlx/remove", `{"confirm":true}`, e.authed(nil)); r.StatusCode != 409 {
		t.Fatalf("%d", r.StatusCode)
	}
	for _, a := range d["actions"].([]any) { // whatever is offered for Ollama still needs confirmation
		id := a.(map[string]any)["id"].(string)
		if r, _ := e.do(t, "POST", "/api/v1/services/ollama/"+id, `{}`, e.authed(nil)); r.StatusCode != 400 {
			t.Fatalf("ollama %s without confirmation: %d", id, r.StatusCode)
		}
	}
	if r, _ := e.do(t, "POST", "/api/v1/services/ollama/remove", `{"confirm":true}`, map[string]string{"Cookie": CookieName + "=" + token}); r.StatusCode != 403 {
		t.Fatalf("no Origin on a mutation: %d", r.StatusCode)
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

	// a fresh launch starts an empty run: the default report is still the newest run with results, not an error
	if err := e.s.ensureRun(ctx, true); err != nil {
		t.Fatal(err)
	}
	if def := e.json(t, "GET", "/api/v1/report", "", 200); def["run"].(map[string]any)["id"] != r2.ID {
		t.Fatalf("default report must be the newest measured run %s: %v", r2.ID, def["run"])
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
