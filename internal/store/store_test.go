package store

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func open(t *testing.T) *DB {
	t.Helper()
	d, err := Open(filepath.Join(t.TempDir(), "sub", "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func setup(t *testing.T, d *DB, name string) (*Tenant, Run) {
	t.Helper()
	ctx := context.Background()
	id, err := d.EnsureTenant(ctx, name)
	if err != nil {
		t.Fatal(err)
	}
	tn := d.ForTenant(id)
	m, err := tn.UpsertMachine(ctx, "fp-"+name, json.RawMessage(`{"chip":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	r, err := tn.CreateRun(ctx, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	return tn, r
}

func TestOpenModes(t *testing.T) {
	p := filepath.Join(t.TempDir(), "a", "b.db")
	d, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	fi, _ := os.Stat(p)
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("db mode %v", fi.Mode().Perm())
	}
	di, _ := os.Stat(filepath.Dir(p))
	if di.Mode().Perm() != 0o700 {
		t.Fatalf("dir mode %v", di.Mode().Perm())
	}
}

func TestTenantIsolation(t *testing.T) {
	d := open(t)
	ctx := context.Background()
	a, ra := setup(t, d, "a")
	b, rb := setup(t, d, "b")

	if _, err := b.GetRun(ctx, ra.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("b read a's run: %v", err)
	}
	if err := b.SetPhase(ctx, ra.ID, PhaseDetected, PhaseBaselineRunning, ""); !errors.Is(err, ErrWrongPhase) {
		t.Fatalf("b moved a's run: %v", err)
	}
	// b writing a result / change against a's run must not land anywhere
	_ = b.AddResult(ctx, ra.ID, Result{Stage: "baseline", Suite: "s", Engine: "e", Metric: "m", Value: 1, Unit: "u"})
	if _, err := b.AddTuneChange(ctx, ra.ID, "k", "1", "2"); err != nil {
		t.Fatal(err)
	}
	if rs, _ := a.Results(ctx, ra.ID); len(rs) != 0 {
		t.Fatalf("a's run polluted by b: %v", rs)
	}
	if cs, _ := a.TuneChanges(ctx, ra.ID); len(cs) != 0 {
		t.Fatalf("a's run polluted by b: %v", cs)
	}
	if rs, _ := b.Results(ctx, ra.ID); len(rs) != 0 {
		t.Fatal("b can read a's results")
	}
	// legit writes are visible only to the owner
	if err := a.AddResult(ctx, ra.ID, Result{Stage: "baseline", Suite: "s", Engine: "e", Metric: "m", Value: 2, Unit: "u"}); err != nil {
		t.Fatal(err)
	}
	if rs, _ := b.Results(ctx, rb.ID); len(rs) != 0 {
		t.Fatal("b sees a's result")
	}
	if rs, _ := a.Results(ctx, ra.ID); len(rs) != 1 {
		t.Fatalf("a lost its result: %v", rs)
	}
	// cache
	if err := a.CachePut(ctx, "canirun", "k", json.RawMessage(`{"a":1}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := b.CacheGet(ctx, "canirun", "k"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("b read a's cache: %v", err)
	}
	if lr, _ := b.LatestRun(ctx); lr.ID != rb.ID {
		t.Fatal("b's latest run is not its own")
	}
}

func TestPhaseGate(t *testing.T) {
	d := open(t)
	ctx := context.Background()
	a, r := setup(t, d, "a")
	steps := [][2]string{
		{PhaseDetected, PhaseBaselineRunning}, {PhaseBaselineRunning, PhaseBaselineDone},
		{PhaseBaselineDone, PhaseTuneReviewed}, {PhaseTuneReviewed, PhaseTunedRunning}, {PhaseTunedRunning, PhaseTunedDone},
	}
	// skipping ahead is refused
	if err := a.SetPhase(ctx, r.ID, PhaseDetected, PhaseTunedDone, ""); !errors.Is(err, ErrWrongPhase) {
		t.Fatalf("skip allowed: %v", err)
	}
	for _, s := range steps {
		if err := a.SetPhase(ctx, r.ID, s[0], s[1], ""); err != nil {
			t.Fatalf("%v: %v", s, err)
		}
	}
	// stale `from` is refused (compare-and-set)
	if err := a.SetPhase(ctx, r.ID, PhaseBaselineRunning, PhaseBaselineDone, ""); !errors.Is(err, ErrWrongPhase) {
		t.Fatalf("stale from allowed: %v", err)
	}
	got, _ := a.GetRun(ctx, r.ID)
	if got.Phase != PhaseTunedDone {
		t.Fatalf("phase %s", got.Phase)
	}
}

func TestResultsAndReplace(t *testing.T) {
	d := open(t)
	ctx := context.Background()
	a, r := setup(t, d, "a")
	for i := 0; i < 2; i++ {
		if err := a.AddResult(ctx, r.ID, Result{Stage: "baseline", Suite: "gpu", Engine: "mlx", Metric: "tflops", Value: 5, Unit: "TFLOPS",
			Trials: json.RawMessage(`[4.9,5,5.1]`)}); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.DeleteStageResults(ctx, r.ID, "baseline"); err != nil {
		t.Fatal(err)
	}
	if rs, _ := a.Results(ctx, r.ID); len(rs) != 0 {
		t.Fatal("not cleared")
	}
	if err := a.AddResult(ctx, r.ID, Result{Stage: "bogus", Suite: "x", Engine: "x", Metric: "x", Unit: "x"}); err == nil {
		t.Fatal("bad stage accepted")
	}
}

func TestMigrationsIdempotent(t *testing.T) {
	p := filepath.Join(t.TempDir(), "x.db")
	for i := 0; i < 2; i++ {
		d, err := Open(p)
		if err != nil {
			t.Fatal(err)
		}
		d.Close()
	}
}

func TestSettingsAreTenantScopedAndDeletable(t *testing.T) {
	d := open(t)
	ctx := context.Background()
	a, _ := setup(t, d, "a")
	b, _ := setup(t, d, "b")
	if err := a.SetSetting(ctx, "models_dir", "/Users/x/Models"); err != nil {
		t.Fatal(err)
	}
	if v, _ := b.GetSetting(ctx, "models_dir"); v != "" {
		t.Fatalf("b sees a's setting: %q", v)
	}
	if v, _ := a.GetSetting(ctx, "models_dir"); v != "/Users/x/Models" {
		t.Fatalf("a lost its setting: %q", v)
	}
	if err := a.SetSetting(ctx, "models_dir", "/Users/x/Other"); err != nil { // upsert
		t.Fatal(err)
	}
	if v, _ := a.GetSetting(ctx, "models_dir"); v != "/Users/x/Other" {
		t.Fatalf("upsert failed: %q", v)
	}
	if err := a.SetSetting(ctx, "models_dir", ""); err != nil { // empty resets to default
		t.Fatal(err)
	}
	if v, _ := a.GetSetting(ctx, "models_dir"); v != "" {
		t.Fatalf("not deleted: %q", v)
	}
}
