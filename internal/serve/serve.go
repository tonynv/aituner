// Package serve runs and supervises a local model server (mlx_lm.server) for a downloaded model.
//
// The server has no authentication and, by default, will download and load any model a request names, so it is
// only ever started on an internal loopback port with HF_HUB_OFFLINE=1 and is reached exclusively through the
// authenticated gateway (internal/gateway), which pins every request to the loaded model.
package serve

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	StateStopped  = "stopped"
	StateStarting = "starting"
	StateRunning  = "running"
	StateStopping = "stopping"
	StateError    = "error"

	ringSize     = 500
	startTimeout = 240 * time.Second // loading a large model from disk can take a while
)

var (
	ErrRunning = errors.New("a model server is already running; stop it first")
	ErrBadSpec = errors.New("invalid server settings")
)

// Spec is what to run. Zero values mean "use mlx_lm's default".
type Spec struct {
	Bin          string // path to mlx_lm.server
	ModelDir     string // local model folder (already validated by the caller)
	Repo         string // display name, e.g. the Hugging Face repo id
	MaxTokens    int    // default max tokens per response (0 = server default)
	KVBits       int    // quantise the KV cache to this many bits (0 = off; 4 or 8)
	KVGroupSize  int    // 0 = default (64)
	QuantKVStart int    // start quantising the KV cache after this many tokens (0 = default)
}

func (s Spec) validate() error {
	switch {
	case s.Bin == "" || s.ModelDir == "":
		return fmt.Errorf("%w: missing binary or model folder", ErrBadSpec)
	case s.MaxTokens < 0 || s.MaxTokens > 1<<20:
		return fmt.Errorf("%w: max tokens must be between 0 and 1048576", ErrBadSpec)
	case s.KVBits != 0 && s.KVBits != 4 && s.KVBits != 8:
		return fmt.Errorf("%w: KV cache bits must be 0 (off), 4 or 8", ErrBadSpec)
	case s.KVGroupSize != 0 && s.KVGroupSize != 32 && s.KVGroupSize != 64 && s.KVGroupSize != 128:
		return fmt.Errorf("%w: KV group size must be 32, 64 or 128", ErrBadSpec)
	case s.QuantKVStart < 0 || s.QuantKVStart > 1<<20:
		return fmt.Errorf("%w: quantised-KV start out of range", ErrBadSpec)
	}
	return nil
}

// Args builds the argv (never a shell string). --trust-remote-code is never passed.
func (s Spec) Args(port int) []string {
	a := []string{"--model", s.ModelDir, "--host", "127.0.0.1", "--port", strconv.Itoa(port)}
	if s.MaxTokens > 0 {
		a = append(a, "--max-tokens", strconv.Itoa(s.MaxTokens))
	}
	if s.KVBits > 0 {
		a = append(a, "--kv-bits", strconv.Itoa(s.KVBits))
		if s.KVGroupSize > 0 {
			a = append(a, "--kv-group-size", strconv.Itoa(s.KVGroupSize))
		}
		if s.QuantKVStart > 0 {
			a = append(a, "--quantized-kv-start", strconv.Itoa(s.QuantKVStart))
		}
	}
	return a
}

type Status struct {
	State       string  `json:"state"`
	Repo        string  `json:"repo"`
	ModelDir    string  `json:"model_dir"`
	Port        int     `json:"port"` // internal port, never exposed to editors
	PID         int     `json:"pid"`
	StartedAt   int64   `json:"started_at,omitempty"`
	ReadyAfterS float64 `json:"ready_after_s,omitempty"`
	RSSBytes    int64   `json:"rss_bytes"`
	Error       string  `json:"error,omitempty"`
	Spec        Spec    `json:"spec"`
}

type Manager struct {
	// LogFile, when set, receives a copy of the server's output (0600, truncated at each start) so a terminal pane can tail it.
	LogFile string
	// PIDFile, when set, records the running server so a later launch can stop it if this process was killed hard.
	PIDFile string

	mu   sync.Mutex
	st   Status
	cmd  *exec.Cmd
	done chan struct{}

	logMu  sync.Mutex
	outf   *os.File // guarded by logMu
	ring   []LogLine
	seq    int
	client *http.Client
}

type LogLine struct {
	Seq  int    `json:"seq"`
	Time int64  `json:"time"`
	Text string `json:"text"`
}

func New() *Manager {
	return &Manager{st: Status{State: StateStopped}, client: &http.Client{Timeout: 3 * time.Second}}
}

func (m *Manager) logf(format string, a ...any) { m.appendLog(fmt.Sprintf(format, a...)) }

func (m *Manager) appendLog(text string) {
	text = strings.TrimRight(text, " \t\r\n") // keeps leading indentation (tracebacks); drops blank and trailing space
	if text == "" {
		return
	}
	m.logMu.Lock()
	defer m.logMu.Unlock()
	if m.outf != nil {
		fmt.Fprintln(m.outf, text)
	}
	m.seq++
	m.ring = append(m.ring, LogLine{Seq: m.seq, Time: time.Now().UnixMilli(), Text: text})
	if len(m.ring) > ringSize {
		m.ring = m.ring[len(m.ring)-ringSize:]
	}
}

// Logs returns log lines after the given sequence number.
func (m *Manager) Logs(after int) []LogLine {
	m.logMu.Lock()
	defer m.logMu.Unlock()
	var out []LogLine
	for _, l := range m.ring {
		if l.Seq > after {
			out = append(out, l)
		}
	}
	return out
}

func (m *Manager) tail(n int) string {
	m.logMu.Lock()
	defer m.logMu.Unlock()
	start := max(0, len(m.ring)-n)
	var b strings.Builder
	for _, l := range m.ring[start:] {
		b.WriteString(l.Text + "\n")
	}
	return strings.TrimSpace(b.String())
}

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

// Status returns the current state, refreshing memory use of a running server.
func (m *Manager) Status() Status {
	m.mu.Lock()
	st := m.st
	m.mu.Unlock()
	if st.PID > 0 && (st.State == StateRunning || st.State == StateStarting) {
		st.RSSBytes = rssBytes(st.PID)
	}
	return st
}

// Backend returns the internal base URL when the server is ready to take requests.
func (m *Manager) Backend() (string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.st.State != StateRunning {
		return "", false
	}
	return "http://127.0.0.1:" + strconv.Itoa(m.st.Port), true
}

func rssBytes(pid int) int64 {
	out, err := exec.Command("/bin/ps", "-o", "rss=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return 0
	}
	kb, _ := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
	return kb * 1024
}

// Start launches the server and returns immediately; poll Status for readiness.
func (m *Manager) Start(parent context.Context, spec Spec) error {
	if err := spec.validate(); err != nil {
		return err
	}
	if fi, err := os.Stat(spec.ModelDir); err != nil || !fi.IsDir() {
		return fmt.Errorf("%w: model folder not found", ErrBadSpec)
	}
	port, err := freePort()
	if err != nil {
		return err
	}
	m.mu.Lock()
	if s := m.st.State; s == StateStarting || s == StateRunning || s == StateStopping {
		m.mu.Unlock()
		return ErrRunning
	}
	cmd := exec.Command(spec.Bin, spec.Args(port)...)
	cmd.Env = append(os.Environ(), "PYTHONUNBUFFERED=1", "HF_HUB_OFFLINE=1", "HF_HUB_DISABLE_TELEMETRY=1", "TOKENIZERS_PARALLELISM=false")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} // own process group so Stop can take down any children
	stdout, _ := cmd.StdoutPipe()
	stderr, _ := cmd.StderrPipe()
	if err := cmd.Start(); err != nil {
		m.mu.Unlock()
		return fmt.Errorf("cannot start the model server: %w", err)
	}
	m.cmd, m.done = cmd, make(chan struct{})
	m.st = Status{State: StateStarting, Repo: spec.Repo, ModelDir: spec.ModelDir, Port: port, PID: cmd.Process.Pid, StartedAt: time.Now().UnixMilli(), Spec: spec}
	done := m.done
	m.mu.Unlock()

	m.writePID(cmd.Process.Pid, spec.ModelDir)
	m.openLogFile()
	// tie the server's life to ours: when aituner's context ends (quit, SIGTERM) the model server stops with it
	go func() {
		select {
		case <-parent.Done():
			m.Stop()
		case <-done:
		}
	}()

	m.appendLog(fmt.Sprintf("--- starting %s on internal port %d", spec.Repo, port))
	for _, r := range []io.Reader{stdout, stderr} {
		go func(r io.Reader) {
			sc := bufio.NewScanner(r)
			sc.Buffer(make([]byte, 64<<10), 1<<20)
			for sc.Scan() {
				m.appendLog(sc.Text())
			}
		}(r)
	}
	go func() { // exit watcher
		err := cmd.Wait()
		m.mu.Lock()
		expected := m.st.State == StateStopping
		switch {
		case expected:
			m.st.State, m.st.PID = StateStopped, 0
		default:
			m.st.State, m.st.PID = StateError, 0
			m.st.Error = "the model server exited unexpectedly"
			if err != nil {
				m.st.Error += ": " + err.Error()
			}
			if t := m.tailLocked(6); t != "" {
				m.st.Error += "\n" + t
			}
		}
		m.mu.Unlock()
		m.removePID()
		m.closeLogFile()
		close(done)
	}()
	go m.waitReady(parent, port, done)
	return nil
}

// tailLocked is tail without taking mu (it only takes logMu, which is independent).
func (m *Manager) tailLocked(n int) string { return m.tail(n) }

func (m *Manager) waitReady(ctx context.Context, port int, done chan struct{}) {
	start := time.Now()
	url := "http://127.0.0.1:" + strconv.Itoa(port) + "/health"
	deadline := time.After(startTimeout)
	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-done:
			return
		case <-ctx.Done():
			m.Stop()
			return
		case <-deadline:
			m.mu.Lock()
			if m.st.State == StateStarting {
				m.st.Error = "the model did not become ready in time"
			}
			m.mu.Unlock()
			m.Stop()
			return
		case <-tick.C:
			req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
			resp, err := m.client.Do(req)
			if err != nil {
				continue
			}
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				m.mu.Lock()
				if m.st.State == StateStarting {
					m.st.State, m.st.ReadyAfterS = StateRunning, time.Since(start).Seconds()
				}
				m.mu.Unlock()
				m.logf("--- ready after %.1fs", time.Since(start).Seconds())
				return
			}
		}
	}
}

// Stop terminates the server (SIGTERM to its process group, SIGKILL after 10 s) and waits for it to exit.
func (m *Manager) Stop() {
	m.mu.Lock()
	cmd, done := m.cmd, m.done
	if cmd == nil || m.st.PID == 0 { // nothing running (never started, already stopped, or already exited)
		m.mu.Unlock()
		return
	}
	if m.st.State != StateStopping {
		keep := m.st.Error
		m.st.State = StateStopping
		if keep != "" {
			m.st.Error = keep
		}
	}
	pid := cmd.Process.Pid
	m.mu.Unlock()

	m.appendLog("--- stopping")
	_ = syscall.Kill(-pid, syscall.SIGTERM)
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		_ = syscall.Kill(-pid, syscall.SIGKILL)
		<-done
	}
}

type pidRecord struct {
	PID      int    `json:"pid"`
	ModelDir string `json:"model_dir"`
}

func (m *Manager) writePID(pid int, dir string) {
	if m.PIDFile == "" {
		return
	}
	b, _ := json.Marshal(pidRecord{PID: pid, ModelDir: dir})
	_ = os.WriteFile(m.PIDFile, b, 0o600)
}

func (m *Manager) removePID() {
	if m.PIDFile != "" {
		_ = os.Remove(m.PIDFile)
	}
}

// ReapStale stops a model server left behind by a previous aituner that was killed before it could stop it (such a server
// keeps gigabytes of GPU memory). It only acts on the process recorded in the pidfile, and only if that process is still
// running, is an mlx_lm.server, and was started for the recorded model folder, so a reused pid is never touched.
func ReapStale(pidfile string) (reaped bool, err error) {
	b, err := os.ReadFile(pidfile)
	if err != nil {
		return false, nil // nothing recorded
	}
	defer os.Remove(pidfile)
	var rec pidRecord
	if json.Unmarshal(b, &rec) != nil || rec.PID <= 1 || rec.ModelDir == "" {
		return false, nil
	}
	if syscall.Kill(rec.PID, 0) != nil {
		return false, nil // already gone
	}
	out, err := exec.Command("/bin/ps", "-o", "command=", "-p", strconv.Itoa(rec.PID)).Output()
	if err != nil {
		return false, nil
	}
	cmdline := string(out)
	if !strings.Contains(cmdline, "mlx_lm.server") || !strings.Contains(cmdline, rec.ModelDir) {
		return false, nil // the pid now belongs to something else: leave it alone
	}
	_ = syscall.Kill(-rec.PID, syscall.SIGTERM)
	for i := 0; i < 50; i++ {
		if syscall.Kill(rec.PID, 0) != nil {
			return true, nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	_ = syscall.Kill(-rec.PID, syscall.SIGKILL)
	return true, nil
}

func (m *Manager) openLogFile() {
	if m.LogFile == "" {
		return
	}
	f, err := os.OpenFile(m.LogFile, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return
	}
	m.logMu.Lock()
	m.outf = f
	m.logMu.Unlock()
}

func (m *Manager) closeLogFile() {
	m.logMu.Lock()
	defer m.logMu.Unlock()
	if m.outf != nil {
		m.outf.Close()
		m.outf = nil
	}
}
