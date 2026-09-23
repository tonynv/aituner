package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

// Phases of the fixed workflow (SPEC §2). Transitions are compare-and-set in SetPhase.
const (
	PhaseDetected        = "detected"
	PhaseBaselineRunning = "baseline_running"
	PhaseBaselineDone    = "baseline_done"
	PhaseTuneReviewed    = "tune_reviewed"
	PhaseTunedRunning    = "tuned_running"
	PhaseTunedDone       = "tuned_done"
)

// allowed lists legal transitions. Failure paths return a running phase to its predecessor.
var allowed = map[string][]string{
	PhaseDetected:        {PhaseBaselineRunning},
	PhaseBaselineRunning: {PhaseBaselineDone, PhaseDetected},
	PhaseBaselineDone:    {PhaseTuneReviewed},
	PhaseTuneReviewed:    {PhaseTunedRunning},
	PhaseTunedRunning:    {PhaseTunedDone, PhaseTuneReviewed},
	PhaseTunedDone:       {},
}

func canMove(from, to string) bool {
	for _, t := range allowed[from] {
		if t == to {
			return true
		}
	}
	return false
}

type Machine struct {
	ID          string          `json:"id"`
	Fingerprint string          `json:"fingerprint"`
	Snapshot    json.RawMessage `json:"snapshot"`
	DetectedAt  int64           `json:"detected_at"`
}

type Run struct {
	ID        string `json:"id"`
	MachineID string `json:"machine_id"`
	Phase     string `json:"phase"`
	Note      string `json:"note"`
	CreatedAt int64  `json:"created_at"`
	UpdatedAt int64  `json:"updated_at"`
}

type Result struct {
	Stage    string          `json:"stage"`
	Suite    string          `json:"suite"`
	Engine   string          `json:"engine"`
	Metric   string          `json:"metric"`
	Value    float64         `json:"value"`
	Unit     string          `json:"unit"`
	Trials   json.RawMessage `json:"trials"`
	Versions json.RawMessage `json:"versions"`
	Thermal  string          `json:"thermal"`
}

type TuneChange struct {
	ID         int64  `json:"id"`
	Key        string `json:"key"`
	Before     string `json:"before"`
	After      string `json:"after"`
	AppliedAt  int64  `json:"applied_at"`
	RevertedAt *int64 `json:"reverted_at"`
}

// UpsertMachine stores the hardware snapshot, keyed by fingerprint so re-detection updates in place.
func (t *Tenant) UpsertMachine(ctx context.Context, fingerprint string, snapshot json.RawMessage) (Machine, error) {
	id := newID()
	_, err := t.db.ExecContext(ctx, `
INSERT INTO machines(id,tenant_id,fingerprint,snapshot,detected_at) VALUES (?,?,?,?,?)
ON CONFLICT(tenant_id,fingerprint) DO UPDATE SET snapshot=excluded.snapshot, detected_at=excluded.detected_at`,
		id, t.id, fingerprint, string(snapshot), now())
	if err != nil {
		return Machine{}, err
	}
	var m Machine
	var snap string
	err = t.db.QueryRowContext(ctx, `SELECT id,fingerprint,snapshot,detected_at FROM machines WHERE tenant_id=? AND fingerprint=?`,
		t.id, fingerprint).Scan(&m.ID, &m.Fingerprint, &snap, &m.DetectedAt)
	m.Snapshot = json.RawMessage(snap)
	return m, err
}

func (t *Tenant) CreateRun(ctx context.Context, machineID string) (Run, error) {
	r := Run{ID: newID(), MachineID: machineID, Phase: PhaseDetected, CreatedAt: now(), UpdatedAt: now()}
	_, err := t.db.ExecContext(ctx, `INSERT INTO runs(id,tenant_id,machine_id,phase,note,created_at,updated_at) VALUES (?,?,?,?,?,?,?)`,
		r.ID, t.id, machineID, r.Phase, "", r.CreatedAt, r.UpdatedAt)
	return r, err
}

func (t *Tenant) GetRun(ctx context.Context, id string) (Run, error) {
	return t.scanRun(t.db.QueryRowContext(ctx,
		`SELECT id,machine_id,phase,note,created_at,updated_at FROM runs WHERE tenant_id=? AND id=?`, t.id, id))
}

// LatestRun returns the most recent run, or ErrNotFound.
func (t *Tenant) LatestRun(ctx context.Context) (Run, error) {
	return t.scanRun(t.db.QueryRowContext(ctx,
		`SELECT id,machine_id,phase,note,created_at,updated_at FROM runs WHERE tenant_id=? ORDER BY created_at DESC, rowid DESC LIMIT 1`, t.id))
}

func (t *Tenant) scanRun(row *sql.Row) (Run, error) {
	var r Run
	err := row.Scan(&r.ID, &r.MachineID, &r.Phase, &r.Note, &r.CreatedAt, &r.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Run{}, ErrNotFound
	}
	return r, err
}

// SetPhase atomically moves a run from one phase to the next. It returns ErrWrongPhase if the run is
// not currently in `from` or the transition is not allowed. note is stored (e.g. a failure reason).
func (t *Tenant) SetPhase(ctx context.Context, runID, from, to, note string) error {
	if !canMove(from, to) {
		return ErrWrongPhase
	}
	res, err := t.db.ExecContext(ctx, `UPDATE runs SET phase=?, note=?, updated_at=? WHERE tenant_id=? AND id=? AND phase=?`,
		to, note, now(), t.id, runID, from)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrWrongPhase
	}
	return nil
}

func (t *Tenant) AddResult(ctx context.Context, runID string, r Result) error {
	if r.Trials == nil {
		r.Trials = json.RawMessage("[]")
	}
	if r.Versions == nil {
		r.Versions = json.RawMessage("{}")
	}
	_, err := t.db.ExecContext(ctx, `
INSERT INTO bench_results(tenant_id,run_id,stage,suite,engine,metric,value,unit,trials,versions,thermal,created_at)
SELECT ?,?,?,?,?,?,?,?,?,?,?,? WHERE EXISTS (SELECT 1 FROM runs WHERE tenant_id=? AND id=?)`,
		t.id, runID, r.Stage, r.Suite, r.Engine, r.Metric, r.Value, r.Unit, string(r.Trials), string(r.Versions), r.Thermal, now(),
		t.id, runID)
	return err
}

func (t *Tenant) Results(ctx context.Context, runID string) ([]Result, error) {
	rows, err := t.db.QueryContext(ctx, `
SELECT stage,suite,engine,metric,value,unit,trials,versions,thermal FROM bench_results
WHERE tenant_id=? AND run_id=? ORDER BY id`, t.id, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Result{}
	for rows.Next() {
		var r Result
		var tr, ve string
		if err := rows.Scan(&r.Stage, &r.Suite, &r.Engine, &r.Metric, &r.Value, &r.Unit, &tr, &ve, &r.Thermal); err != nil {
			return nil, err
		}
		r.Trials, r.Versions = json.RawMessage(tr), json.RawMessage(ve)
		out = append(out, r)
	}
	return out, rows.Err()
}

// DeleteStageResults clears a stage so a failed/retried benchmark never mixes partial data.
func (t *Tenant) DeleteStageResults(ctx context.Context, runID, stage string) error {
	_, err := t.db.ExecContext(ctx, `DELETE FROM bench_results WHERE tenant_id=? AND run_id=? AND stage=?`, t.id, runID, stage)
	return err
}

func (t *Tenant) AddTuneChange(ctx context.Context, runID, key, before, after string) (int64, error) {
	res, err := t.db.ExecContext(ctx, `
INSERT INTO tune_changes(tenant_id,run_id,key,before,after,applied_at)
SELECT ?,?,?,?,?,? WHERE EXISTS (SELECT 1 FROM runs WHERE tenant_id=? AND id=?)`,
		t.id, runID, key, before, after, now(), t.id, runID)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (t *Tenant) MarkReverted(ctx context.Context, id int64) error {
	res, err := t.db.ExecContext(ctx, `UPDATE tune_changes SET reverted_at=? WHERE tenant_id=? AND id=? AND reverted_at IS NULL`, now(), t.id, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (t *Tenant) TuneChanges(ctx context.Context, runID string) ([]TuneChange, error) {
	rows, err := t.db.QueryContext(ctx, `SELECT id,key,before,after,applied_at,reverted_at FROM tune_changes WHERE tenant_id=? AND run_id=? ORDER BY id`, t.id, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TuneChange{}
	for rows.Next() {
		var c TuneChange
		var rev sql.NullInt64
		if err := rows.Scan(&c.ID, &c.Key, &c.Before, &c.After, &c.AppliedAt, &rev); err != nil {
			return nil, err
		}
		if rev.Valid {
			v := rev.Int64
			c.RevertedAt = &v
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

type CacheEntry struct {
	Body      json.RawMessage
	FetchedAt int64
}

func (t *Tenant) CachePut(ctx context.Context, source, key string, body json.RawMessage) error {
	_, err := t.db.ExecContext(ctx, `
INSERT INTO reco_cache(tenant_id,source,key,body,fetched_at) VALUES (?,?,?,?,?)
ON CONFLICT(tenant_id,source,key) DO UPDATE SET body=excluded.body, fetched_at=excluded.fetched_at`,
		t.id, source, key, string(body), now())
	return err
}

func (t *Tenant) CacheGet(ctx context.Context, source, key string) (CacheEntry, error) {
	var e CacheEntry
	var b string
	err := t.db.QueryRowContext(ctx, `SELECT body,fetched_at FROM reco_cache WHERE tenant_id=? AND source=? AND key=?`, t.id, source, key).Scan(&b, &e.FetchedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return e, ErrNotFound
	}
	e.Body = json.RawMessage(b)
	return e, err
}
