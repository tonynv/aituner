// Package api is the local HTTP API and static host for the web UI. It binds to loopback only and is
// protected by a per-launch token, a Host allow-list (DNS-rebinding defence) and Origin checks (SPEC §10).
package api

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/tonynv/aituner/internal/bench"
	"github.com/tonynv/aituner/internal/canirun"
	"github.com/tonynv/aituner/internal/download"
	"github.com/tonynv/aituner/internal/hf"
	"github.com/tonynv/aituner/internal/platform"
	"github.com/tonynv/aituner/internal/reco"
	"github.com/tonynv/aituner/internal/store"
	"github.com/tonynv/aituner/internal/tune"
	"github.com/tonynv/aituner/internal/webui"
)

const (
	CookieName = "aituner_session"
	maxBody    = 64 << 10
)

type Config struct {
	Store    *store.DB
	Tenant   string
	Platform platform.Platform
	Token    string
	DataDir  string
	CanIRun  *canirun.Client
	HF       *hf.Client
	Ollama   *bench.Ollama
	Runner   tune.Runner
	Log      func(string)
}

type Server struct {
	cfg     Config
	ctx     context.Context
	tn      *store.Tenant
	jobs    *jobs
	engine  *reco.Engine
	dl      *download.Manager
	allowed map[string]bool // repos the recommender has offered: the only ones downloads may fetch (guarded by mu)

	mu          sync.Mutex           // guards hw, unsupported, hosts, launch
	launch      map[string]time.Time // single-use launch nonces -> expiry
	launchTTL   time.Duration
	hw          *platform.Hardware
	unsupported string
	hosts       map[string]bool
}

func New(ctx context.Context, cfg Config) (*Server, error) {
	if len(cfg.Token) < 32 {
		return nil, errors.New("token too short")
	}
	id, err := cfg.Store.EnsureTenant(ctx, cfg.Tenant)
	if err != nil {
		return nil, err
	}
	if cfg.Log == nil {
		cfg.Log = func(string) {}
	}
	s := &Server{cfg: cfg, ctx: ctx, tn: cfg.Store.ForTenant(id), jobs: newJobs(), hosts: map[string]bool{}, allowed: map[string]bool{}, dl: download.New(cfg.HF), launch: map[string]time.Time{}, launchTTL: 15 * time.Minute}
	s.engine = &reco.Engine{CanIRun: cfg.CanIRun, HF: cfg.HF, Cache: storeCache{s.tn}}
	if err := s.refreshHW(ctx); err != nil && !errors.Is(err, platform.ErrUnsupported) {
		return nil, err
	}
	if s.hw != nil {
		if err := s.ensureRun(ctx, false); err != nil {
			return nil, err
		}
		s.recoverInterrupted(ctx)
	}
	return s, nil
}

// recoverInterrupted un-sticks a run left in a *_running phase by a crash or kill: nothing is running now.
func (s *Server) recoverInterrupted(ctx context.Context) {
	r, err := s.tn.LatestRun(ctx)
	if err != nil {
		return
	}
	switch r.Phase {
	case store.PhaseBaselineRunning:
		_ = s.tn.SetPhase(ctx, r.ID, r.Phase, store.PhaseDetected, "previous benchmark was interrupted")
	case store.PhaseTunedRunning:
		_ = s.tn.SetPhase(ctx, r.ID, r.Phase, store.PhaseTuneReviewed, "previous benchmark was interrupted")
	}
}

// Status is a cheap in-process snapshot for the TUI.
func (s *Server) Status() (phase string, job *JobInfo) {
	if r, err := s.tn.LatestRun(s.ctx); err == nil {
		phase = r.Phase
	}
	return phase, s.jobs.info()
}

// Subscribe streams job events to in-process consumers (the TUI).
func (s *Server) Subscribe() (replay []SSEEvent, ch <-chan SSEEvent, unsub func()) {
	r, c, u := s.jobs.subscribe(0)
	return r, c, u
}

// LaunchToken returns a fresh single-use, expiring nonce for the browser hand-off URL. The session token itself
// never appears in a URL, argv or terminal: a snooper who sees the link (ps, history) gets a value that is
// spent the moment the real browser uses it.
func (s *Server) LaunchToken() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand failure is unrecoverable
	}
	n := hex.EncodeToString(b)
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for k, exp := range s.launch { // drop expired, and cap outstanding links
		if now.After(exp) {
			delete(s.launch, k)
		}
	}
	for len(s.launch) >= 8 {
		var oldest string
		var oe time.Time
		for k, exp := range s.launch {
			if oldest == "" || exp.Before(oe) {
				oldest, oe = k, exp
			}
		}
		delete(s.launch, oldest)
	}
	s.launch[n] = now.Add(s.launchTTL)
	return n
}

// consumeLaunch spends a nonce; it returns true at most once per nonce and never after expiry.
func (s *Server) consumeLaunch(n string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	exp, ok := s.launch[n]
	delete(s.launch, n)
	return ok && time.Now().Before(exp)
}

// SetPort registers the listening port so the Host allow-list can be enforced.
func (s *Server) SetPort(port int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, h := range []string{"127.0.0.1", "localhost", "[::1]"} {
		s.hosts[fmt.Sprintf("%s:%d", h, port)] = true
	}
}

func (s *Server) refreshHW(ctx context.Context) error {
	hw, err := s.cfg.Platform.Detect(ctx)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		if errors.Is(err, platform.ErrUnsupported) {
			s.unsupported = fmt.Sprintf("%s is not supported yet. Only macOS on Apple Silicon is implemented.", s.cfg.Platform.Name())
		}
		return err
	}
	s.hw = hw
	return nil
}

func (s *Server) hardware() *platform.Hardware {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hw
}

// ensureRun keeps a run for the detected machine; force starts a fresh one.
func (s *Server) ensureRun(ctx context.Context, force bool) error {
	hw := s.hardware()
	snap, _ := json.Marshal(hw)
	m, err := s.tn.UpsertMachine(ctx, fingerprint(hw), snap)
	if err != nil {
		return err
	}
	if !force {
		if r, err := s.tn.LatestRun(ctx); err == nil && r.MachineID == m.ID {
			return nil
		}
	}
	_, err = s.tn.CreateRun(ctx, m.ID)
	return err
}

// Handler builds the full HTTP handler.
func (s *Server) Handler() http.Handler {
	api := http.NewServeMux()
	api.HandleFunc("GET /api/v1/state", s.handleState)
	api.HandleFunc("GET /api/v1/events", s.handleEvents)
	api.HandleFunc("POST /api/v1/detect", s.handleDetect)
	api.HandleFunc("POST /api/v1/runs", s.handleNewRun)
	api.HandleFunc("POST /api/v1/benchmark", s.handleBenchmark)
	api.HandleFunc("POST /api/v1/job/cancel", s.handleCancel)
	api.HandleFunc("GET /api/v1/tune/plan", s.handleTunePlan)
	api.HandleFunc("POST /api/v1/tune/apply", s.handleTuneApply)
	api.HandleFunc("POST /api/v1/tune/revert", s.handleTuneRevert)
	api.HandleFunc("GET /api/v1/recommendations", s.handleRecommendations)
	api.HandleFunc("GET /api/v1/settings", s.handleGetSettings)
	api.HandleFunc("PUT /api/v1/settings", s.handlePutSettings)
	api.HandleFunc("GET /api/v1/downloads", s.handleListDownloads)
	api.HandleFunc("POST /api/v1/downloads", s.handleStartDownload)
	api.HandleFunc("POST /api/v1/downloads/cancel", s.handleCancelDownload)

	root := http.NewServeMux()
	root.Handle("/api/", s.origin(s.auth(s.audit(api))))
	root.HandleFunc("/", s.handleStatic)
	return s.headers(s.hostCheck(root))
}

func (s *Server) headers(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		h.Set("Cross-Origin-Resource-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}

// hostCheck rejects any request whose Host is not our loopback address, defeating DNS rebinding.
func (s *Server) hostCheck(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		ok := s.hosts[r.Host]
		s.mu.Unlock()
		if !ok {
			writeErr(w, http.StatusForbidden, "bad_host", "unexpected Host header")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// origin enforces same-origin on state-changing requests (defence in depth on top of SameSite=Strict).
func (s *Server) origin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions {
			o := r.Header.Get("Origin")
			if o == "" {
				writeErr(w, http.StatusForbidden, "bad_origin", "Origin header required")
				return
			}
			u, err := url.Parse(o)
			s.mu.Lock()
			ok := err == nil && u.Scheme == "http" && s.hosts[u.Host]
			s.mu.Unlock()
			if !ok {
				writeErr(w, http.StatusForbidden, "bad_origin", "cross-origin request refused")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) tokenOK(v string) bool {
	return subtle.ConstantTimeCompare([]byte(v), []byte(s.cfg.Token)) == 1
}

func (s *Server) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if c, err := r.Cookie(CookieName); err == nil && s.tokenOK(c.Value) {
			next.ServeHTTP(w, r)
			return
		}
		if b, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok && s.tokenOK(b) {
			next.ServeHTTP(w, r)
			return
		}
		writeErr(w, http.StatusUnauthorized, "unauthorized", "missing or invalid session")
	})
}

// audit logs every state-changing request after it has passed authentication, so any action taken through
// the API (start a benchmark, apply tuning, download a model, change the folder) can be attributed later.
func (s *Server) audit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions {
			ua := r.UserAgent()
			if len(ua) > 80 {
				ua = ua[:80]
			}
			s.cfg.Log(fmt.Sprintf("audit: %s %s from %s ua=%q referer=%q", r.Method, r.URL.Path, r.RemoteAddr, ua, r.Header.Get("Referer")))
		}
		next.ServeHTTP(w, r)
	})
}

// handleStatic serves the SPA. A valid single-use ?t= launch nonce is exchanged for the session cookie and stripped from the URL.
func (s *Server) handleStatic(w http.ResponseWriter, r *http.Request) {
	if t := r.URL.Query().Get("t"); t != "" {
		if s.consumeLaunch(t) {
			http.SetCookie(w, &http.Cookie{Name: CookieName, Value: s.cfg.Token, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode})
		}
		q := r.URL.Query()
		q.Del("t")
		r.URL.RawQuery = q.Encode()
		http.Redirect(w, r, r.URL.RequestURI(), http.StatusFound)
		return
	}
	sub, built := webui.FS()
	if !built {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusServiceUnavailable)
		io.WriteString(w, "aituner: web UI not built. Run ./run_aituner.sh (it builds web/ into internal/webui/dist).\n")
		return
	}
	p := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	if p == "" {
		p = "index.html"
	}
	if f, err := sub.Open(p); err == nil {
		st, _ := f.Stat()
		f.Close()
		if !st.IsDir() {
			if p == "sw.js" || p == "index.html" {
				w.Header().Set("Cache-Control", "no-cache")
			}
			http.ServeFileFS(w, r, sub, p)
			return
		}
	}
	if strings.Contains(path.Base(p), ".") { // a missing asset is a 404, not the SPA
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeFileFS(w, r, fs.FS(sub), "index.html")
}

type apiError struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, kind, msg string) {
	writeJSON(w, code, apiError{Error: kind, Message: msg})
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBody)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil && !errors.Is(err, io.EOF) {
		writeErr(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return false
	}
	return true
}

// storeCache adapts the tenant-scoped datastore to reco.Cache.
type storeCache struct{ tn *store.Tenant }

func (c storeCache) Get(ctx context.Context, key string) ([]byte, time.Time, bool) {
	e, err := c.tn.CacheGet(ctx, "reco", key)
	if err != nil {
		return nil, time.Time{}, false
	}
	return e.Body, time.UnixMilli(e.FetchedAt), true
}

func (c storeCache) Put(ctx context.Context, key string, body []byte) {
	_ = c.tn.CachePut(ctx, "reco", key, body)
}
