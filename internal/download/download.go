// Package download saves recommended models into the user's models folder. It only ever downloads weights,
// config and tokenizer files (never *.py or pickle), verifies every file against Hugging Face's listing, resumes
// partial downloads, and records completion in a marker so "downloaded" survives restarts.
package download

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/tonynv/aituner/internal/bench"
	"github.com/tonynv/aituner/internal/hf"
	"github.com/tonynv/aituner/internal/modeldir"
)

// Allow/Ignore are the single source of truth for which repo files are fetched (passed to the Python side).
var (
	Allow  = []string{"*.json", "*.safetensors", "*.model", "*.tiktoken", "*.txt", "*.jinja", "*.jsonl"}
	Ignore = []string{"*.py", "*.bin", "*.pt", "*.pth", "*.pkl", "*.pickle", "*.ckpt"}
)

const (
	MarkerName    = ".aituner-model.json"
	diskSlack     = 512 << 20
	State_Running = "running"
	State_Done    = "done"
	State_Error   = "error"
	State_Cancel  = "cancelled"
)

var (
	ErrBusy       = errors.New("a download is already running")
	ErrNotAllowed = errors.New("this model is not available to download")
)

func match(patterns []string, name string) bool {
	for _, p := range patterns {
		if ok, _ := path.Match(p, path.Base(name)); ok {
			return true
		}
	}
	return false
}

// Wanted reports whether a repo file is part of the download.
func Wanted(name string) bool { return match(Allow, name) && !match(Ignore, name) }

type Status struct {
	Repo       string  `json:"repo"`
	Dest       string  `json:"dest"`
	State      string  `json:"state"`
	BytesDone  int64   `json:"bytes_done"`
	BytesTotal int64   `json:"bytes_total"`
	SpeedBps   float64 `json:"speed_bps"`
	Error      string  `json:"error,omitempty"`
	StartedAt  int64   `json:"started_at,omitempty"`
	FinishedAt int64   `json:"finished_at,omitempty"`
}

type marker struct {
	Repo        string `json:"repo"`
	Bytes       int64  `json:"bytes"`
	Files       int    `json:"files"`
	CompletedAt int64  `json:"completed_at"`
}

type Manager struct {
	HF *hf.Client

	mu     sync.Mutex
	active *run
	last   map[string]Status // finished this session (errors, cancels)
}

type run struct {
	cancel context.CancelFunc
	st     Status
}

func New(c *hf.Client) *Manager { return &Manager{HF: c, last: map[string]Status{}} }

// Plan is what a download will do, computed before anything is written.
type Plan struct {
	Files []hf.Sibling
	Total int64
}

// PlanFor lists the wanted files of a repo and rejects unsafe or unusable repos.
func PlanFor(inf hf.Info) (Plan, error) {
	if inf.IsGated() {
		return Plan{}, errors.New("this model is gated (needs a Hugging Face login), which aituner does not use")
	}
	if inf.PickleOnly() {
		return Plan{}, errors.New("this repository only has pickle weights, which are unsafe to load")
	}
	var p Plan
	hasWeights := false
	for _, s := range inf.Siblings {
		if !Wanted(s.Name) {
			continue
		}
		p.Files = append(p.Files, s)
		p.Total += s.Size
		if strings.HasSuffix(s.Name, ".safetensors") {
			hasWeights = true
		}
	}
	if !hasWeights {
		return Plan{}, errors.New("this repository has no safetensors weights")
	}
	return p, nil
}

// dirSize is the number of bytes currently on disk under dir, including partial files.
func dirSize(dir string) int64 {
	var n int64
	_ = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if fi, err := d.Info(); err == nil {
				n += fi.Size()
			}
		}
		return nil
	})
	return n
}

// Verify checks that every planned file exists at dest with exactly the size Hugging Face lists.
func Verify(dest string, plan Plan) error {
	for _, f := range plan.Files {
		fi, err := os.Stat(filepath.Join(dest, filepath.FromSlash(f.Name)))
		if err != nil {
			return fmt.Errorf("missing file %s", f.Name)
		}
		if fi.Size() != f.Size {
			return fmt.Errorf("%s is %d bytes, expected %d", f.Name, fi.Size(), f.Size)
		}
	}
	return nil
}

// Complete reports whether dest holds a finished download of repo (marker present and matching).
func Complete(dest, repo string) (int64, bool) {
	b, err := os.ReadFile(filepath.Join(dest, MarkerName))
	if err != nil {
		return 0, false
	}
	var m marker
	if json.Unmarshal(b, &m) != nil || m.Repo != repo {
		return 0, false
	}
	return m.Bytes, true
}

// Required is the free space a download needs: the bytes still to fetch, plus 2% and a fixed slack for
// metadata and temporary files. Zero when everything is already on disk.
func Required(total, have int64) int64 {
	need := total - have
	if need <= 0 {
		return 0
	}
	return need + need/50 + diskSlack
}

// Start begins downloading repo under root. It fails fast, before writing anything, on a bad repo, a full disk
// or a running download.
func (m *Manager) Start(parent context.Context, mlx bench.MLX, repo, root string) (Status, error) {
	dest, err := modeldir.Dest(root, repo)
	if err != nil {
		return Status{}, err
	}
	if n, ok := Complete(dest, repo); ok {
		return Status{Repo: repo, Dest: dest, State: State_Done, BytesDone: n, BytesTotal: n}, nil
	}
	inf, err := m.HF.Info(parent, repo)
	if err != nil {
		return Status{}, fmt.Errorf("cannot read the model listing: %w", err)
	}
	plan, err := PlanFor(inf)
	if err != nil {
		return Status{}, err
	}
	have := dirSize(dest) // resuming: bytes already there do not need space again
	if req := Required(plan.Total, have); req > 0 {
		if free := modeldir.Stat(root).FreeBytes; free > 0 && free < req {
			return Status{}, fmt.Errorf("not enough free space in %s: need about %s, %s available", root, human(req), human(free))
		}
	}
	if err := modeldir.Ensure(root); err != nil {
		return Status{}, err
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return Status{}, err
	}

	m.mu.Lock()
	if m.active != nil {
		m.mu.Unlock()
		return Status{}, ErrBusy
	}
	ctx, cancel := context.WithCancel(parent)
	r := &run{cancel: cancel, st: Status{Repo: repo, Dest: dest, State: State_Running, BytesTotal: plan.Total, BytesDone: have, StartedAt: time.Now().UnixMilli()}}
	m.active = r
	delete(m.last, repo)
	st := r.st
	m.mu.Unlock()

	go m.work(ctx, mlx, r, plan)
	return st, nil
}

func (m *Manager) finish(r *run, state, errMsg string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r.st.State, r.st.Error, r.st.FinishedAt = state, errMsg, time.Now().UnixMilli()
	r.st.SpeedBps = 0
	m.last[r.st.Repo] = r.st
	m.active = nil
	r.cancel()
}

func (m *Manager) work(ctx context.Context, mlx bench.MLX, r *run, plan Plan) {
	done := make(chan error, 1)
	go func() { done <- mlx.Download(ctx, r.st.Repo, r.st.Dest, Allow, Ignore) }()

	tick := time.NewTicker(700 * time.Millisecond)
	defer tick.Stop()
	prev, prevT := dirSize(r.st.Dest), time.Now()
	var speed float64
	for {
		select {
		case err := <-done:
			if ctx.Err() != nil {
				m.finish(r, State_Cancel, "")
				return
			}
			if err != nil {
				m.finish(r, State_Error, cleanErr(err))
				return
			}
			if err := Verify(r.st.Dest, plan); err != nil {
				m.finish(r, State_Error, "download finished but failed verification: "+err.Error())
				return
			}
			mk, _ := json.Marshal(marker{Repo: r.st.Repo, Bytes: plan.Total, Files: len(plan.Files), CompletedAt: time.Now().UnixMilli()})
			if err := os.WriteFile(filepath.Join(r.st.Dest, MarkerName), mk, 0o644); err != nil {
				m.finish(r, State_Error, "could not record completion: "+err.Error())
				return
			}
			m.mu.Lock()
			r.st.BytesDone = plan.Total
			m.mu.Unlock()
			m.finish(r, State_Done, "")
			return
		case <-tick.C:
			cur, now := dirSize(r.st.Dest), time.Now()
			if dt := now.Sub(prevT).Seconds(); dt > 0 {
				inst := float64(cur-prev) / dt
				if speed == 0 {
					speed = inst
				} else {
					speed = 0.7*speed + 0.3*inst // smooth
				}
			}
			prev, prevT = cur, now
			m.mu.Lock()
			r.st.BytesDone, r.st.SpeedBps = min(cur, plan.Total), max(speed, 0)
			m.mu.Unlock()
		}
	}
}

func cleanErr(err error) string {
	s := err.Error()
	if len(s) > 400 {
		s = s[len(s)-400:]
	}
	return s
}

func human(b int64) string {
	const g = 1 << 30
	if b >= g {
		return fmt.Sprintf("%.1f GB", float64(b)/g)
	}
	return fmt.Sprintf("%d MB", b>>20)
}

// Cancel stops the running download for repo. Partial files are kept so a later attempt resumes.
func (m *Manager) Cancel(repo string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.active != nil && m.active.st.Repo == repo {
		m.active.cancel()
		return true
	}
	return false
}

// Active reports whether a download is running.
func (m *Manager) Active() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.active != nil
}

// List returns the running download, this session's finished ones, and completed downloads found on disk under
// root (two levels: <org>/<name>), newest first.
func (m *Manager) List(root string) []Status {
	byRepo := map[string]Status{}
	if orgs, err := os.ReadDir(root); err == nil {
		for _, o := range orgs {
			if !o.IsDir() {
				continue
			}
			subs, _ := os.ReadDir(filepath.Join(root, o.Name()))
			for _, s := range subs {
				dest := filepath.Join(root, o.Name(), s.Name())
				repo := o.Name() + "/" + s.Name()
				if n, ok := Complete(dest, repo); ok {
					byRepo[repo] = Status{Repo: repo, Dest: dest, State: State_Done, BytesDone: n, BytesTotal: n}
				}
			}
		}
	}
	m.mu.Lock()
	for k, v := range m.last {
		if v.State != State_Done { // a done one is already on disk
			byRepo[k] = v
		}
	}
	if m.active != nil {
		byRepo[m.active.st.Repo] = m.active.st
	}
	m.mu.Unlock()
	out := make([]Status, 0, len(byRepo))
	for _, v := range byRepo {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Repo < out[j].Repo })
	return out
}

// ChatCommand builds the shell command to chat with a downloaded model using the venv's mlx_lm.
func ChatCommand(binDir, dest string) string {
	q := func(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }
	exe := "mlx_lm.chat"
	if binDir != "" {
		exe = q(binDir + "/mlx_lm.chat")
	}
	return exe + " --model " + q(dest)
}
