// Package jobs is a tiny durable background-job ledger. Commands marked
// bg-able run as daemonless background work; agents submit, then
// query/wait on rows instead of writing their own polling loops. State lives
// in XDG_STATE_HOME/syska-sonic-cli/jobs.json.
package jobs

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/notaproton/syska-sonic-cli/internal/clierrors"
	"github.com/notaproton/syska-sonic-cli/internal/config"
)

const (
	StatusPending = "pending"
	StatusRunning = "running"
	StatusDone    = "done"
	StatusError   = "error"
)

// Job is one durable async operation. Status is one of
// pending|running|done|error (schema enum job_status). WriterPID records the
// process that owns an unfinished row so loads can tell "still running"
// (owned, pid alive) apart from "aborted by a dead process".
type Job struct {
	ID           string          `json:"id"`
	Kind         string          `json:"kind"`
	CreatedAt    string          `json:"created_at"`
	UpdatedAt    string          `json:"updated_at"`
	Status       string          `json:"status"`
	WriterPID    int             `json:"writer_pid,omitempty"`
	Result       json.RawMessage `json:"result,omitempty"`
	ErrorMessage string          `json:"error_message,omitempty"`
}

// Ledger is the loaded in-memory view; every mutation re-reads the file under
// an advisory lock so concurrent processes interleave safely.
type Ledger struct {
	Path string
	Jobs []Job
}

func now() string { return time.Now().UTC().Format(time.RFC3339Nano) }

// staleRow reports whether an unfinished row belongs to a dead process.
func staleRow(j Job) bool {
	if j.WriterPID <= 0 {
		return true
	}
	return !pidAlive(j.WriterPID)
}

func load() (*Ledger, error) {
	p, err := config.StatePath("jobs.json")
	if err != nil {
		return nil, err
	}
	l := &Ledger{Path: p}
	b, rerr := os.ReadFile(p)
	switch {
	case errors.Is(rerr, fs.ErrNotExist):
		return l, nil
	case rerr != nil:
		return nil, clierrors.New(clierrors.CodeDevice, "read jobs ledger %s", p).WithCause(rerr)
	}
	if err := json.Unmarshal(b, &l.Jobs); err != nil {
		return nil, clierrors.New(clierrors.CodeUsage, "jobs ledger %s is corrupt", p).
			WithHint("delete jobs.json to reset the ledger; historical job rows are informational only")
	}
	repaired := false
	for i := range l.Jobs {
		switch l.Jobs[i].Status {
		case StatusPending, StatusRunning:
			if staleRow(l.Jobs[i]) {
				l.Jobs[i].Status = StatusError
				l.Jobs[i].ErrorMessage = "aborted: owning process ended before the job finished"
				l.Jobs[i].UpdatedAt = now()
				repaired = true
			}
		}
	}
	if repaired {
		_ = l.Save() // best-effort durable repair; reads still work if this fails
	}
	return l, nil
}

// Load is the read-only view with stale-row repair; callers that need
// durability add Save() themselves.
func Load() (*Ledger, error) {
	l, err := load()
	if err != nil {
		return nil, err
	}
	return l, nil
}

func (l *Ledger) Save() error {
	b, err := json.MarshalIndent(l.Jobs, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(l.Path), 0o700); err != nil {
		return clierrors.New(clierrors.CodeDevice, "create ledger dir").WithCause(err)
	}
	tmp := l.Path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return clierrors.New(clierrors.CodeDevice, "write %s", tmp).WithCause(err)
	}
	return os.Rename(tmp, l.Path)
}

// mutate runs fn against a fresh load under lock, then persists.
func mutate(kind string, fn func(l *Ledger) (json.RawMessage, *Job)) (*Job, error) {
	p, err := config.StatePath("jobs.json")
	if err != nil {
		return nil, err
	}
	return withLock(p, func() (*Job, error) {
		l, err := load()
		if err != nil {
			return nil, err
		}
		result, job := fn(l)
		if err := l.Save(); err != nil {
			return nil, err
		}
		if result != nil {
			job.Result = result
		}
		return job, nil
	})
}

// Submit registers a fresh pending job durably.
func Submit(kind string) (*Job, error) {
	return mutate(kind, func(l *Ledger) (json.RawMessage, *Job) {
		buf := make([]byte, 4)
		if _, rerr := rand.Read(buf); rerr != nil {
			return nil, &Job{Status: StatusError, ErrorMessage: rerr.Error()}
		}
		j := &Job{
			ID:        hex.EncodeToString(buf),
			Kind:      kind,
			CreatedAt: now(),
			UpdatedAt: now(),
			Status:    StatusPending,
			WriterPID: os.Getpid(),
		}
		l.Jobs = append(l.Jobs, *j)
		return nil, j
	})
}

// MarkRunning transitions a pending job before its worker starts.
func MarkRunning(id string) (*Job, error) {
	return mutate("mark_running", func(l *Ledger) (json.RawMessage, *Job) {
		for i := range l.Jobs {
			if l.Jobs[i].ID == id {
				l.Jobs[i].Status = StatusRunning
				l.Jobs[i].UpdatedAt = now()
				return nil, &l.Jobs[i]
			}
		}
		return nil, &Job{ID: id, Status: StatusError, ErrorMessage: "job vanished from ledger while starting"}
	})
}

// Finish stores the terminal state of a job (done rows carry result).
func Finish(id, status, message string, result json.RawMessage) (*Job, error) {
	return mutate("finish_job", func(l *Ledger) (json.RawMessage, *Job) {
		for i := range l.Jobs {
			if l.Jobs[i].ID == id {
				l.Jobs[i].Status = status
				l.Jobs[i].UpdatedAt = now()
				l.Jobs[i].Result = result
				l.Jobs[i].ErrorMessage = message
				return result, &l.Jobs[i]
			}
		}
		return nil, &Job{ID: id, Status: StatusError, ErrorMessage: "job vanished from ledger while finishing"}
	})
}

func (l *Ledger) Get(id string) (Job, bool) {
	for _, j := range l.Jobs {
		if j.ID == id {
			return j, true
		}
	}
	return Job{}, false
}

// List returns newest-first rows, optionally filtered by status.
func (l *Ledger) List(status string, limit, offset int) []Job {
	out := make([]Job, 0, len(l.Jobs))
	for i := len(l.Jobs) - 1; i >= 0; i-- {
		if status != "" && l.Jobs[i].Status != status {
			continue
		}
		out = append(out, l.Jobs[i])
	}
	if offset > 0 {
		if offset >= len(out) {
			return nil
		}
		out = out[offset:]
	}
	if limit > 0 && limit < len(out) {
		out = out[:limit]
	}
	return out
}
