package jobs

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestSubmitFinishList(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", dir) // config.StatePath uses state dir directly

	j, err := Submit("discover")
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if j.Status != StatusPending || j.Kind != "discover" {
		t.Fatalf("initial job: %+v", j)
	}

	if _, err := MarkRunning(j.ID); err != nil {
		t.Fatalf("mark running: %v", err)
	}
	finished, err := Finish(j.ID, StatusDone, "", []byte(`{"rows":2}`))
	if err != nil {
		t.Fatalf("finish: %v", err)
	}
	if finished.Status != StatusDone || string(finished.Result) != `{"rows":2}` {
		t.Fatalf("finished: %+v", finished)
	}

	ledger, err := Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	got, ok := ledger.Get(j.ID)
	if !ok || got.Status != StatusDone {
		t.Fatalf("durable row: %+v ok=%v", got, ok)
	}
	rows := ledger.List("", 20, 0)
	if len(rows) != 1 || rows[0].ID != j.ID {
		t.Fatalf("list: %+v", rows)
	}

	// stale running rows repair on load
	corrupt := []byte(`[{"id":"zz","kind":"discover","status":"running"}]`)
	path := filepath.Join(dir, "syska-sonic-cli", "jobs.json")
	if err := os.WriteFile(path, corrupt, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	l2, err := Load()
	if err != nil {
		t.Fatalf("repair load: %v", err)
	}
	r, ok := l2.Get("zz")
	if !ok || r.Status != StatusError {
		t.Fatalf("stale repair: %+v ok=%v", r, ok)
	}
	if r.ErrorMessage == "" {
		t.Fatal("repair should explain")
	}
}

func TestLoadMissingFile(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	l, err := Load()
	if err != nil {
		t.Fatalf("load empty: %v", err)
	}
	if len(l.Jobs) != 0 {
		t.Fatalf("expected empty: %+v", l.Jobs)
	}
}

func TestStatusFilter(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	if _, err := Submit("discover"); err != nil {
		t.Fatalf("submit: %v", err)
	}
	l, _ := Load()
	if len(l.List(StatusDone, 0, 0)) != 0 {
		t.Fatal("done filter should be empty")
	}
	if len(l.List(StatusPending, 0, 0)) != 1 {
		t.Fatal("pending filter should be 1")
	}
	rows := l.List("bogus", 0, 0)
	if len(rows) != 0 {
		t.Fatal("bogus filter must return nothing")
	}
	_ = errors.New
}
