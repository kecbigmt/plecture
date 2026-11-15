package pluginservice

import (
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"syscall"
	"time"

	"github.com/kecbigmt/plecture/app/internal/datahome"
	"github.com/kecbigmt/plecture/app/internal/domain"
)

// Status is one service's resident-global running record — the fields the
// plugin service lifecycle ADR's Consequences section calls out: service
// identity, running state, pid, restart count, last exit, last error, last
// health result, plugin id, and the content hash of the plugin that
// produced the running process.
type Status struct {
	ID           string
	PluginID     string
	Name         string
	Running      bool
	PID          int
	RestartCount int
	LastExitAt   time.Time
	LastError    string
	Health       domain.HealthState
	ContentHash  string
}

// StatusRegistry is the resident process's status authority. Its snapshot
// gives short-lived CLI processes a view without sharing the registry.
type StatusRegistry struct {
	mu           sync.Mutex
	statuses     map[string]Status
	snapshotPath string
}

type statusSnapshot struct {
	OwnerPID int      `json:"owner_pid"`
	Statuses []Status `json:"statuses"`
}

func DefaultStatusSnapshotPath() string {
	return filepath.Join(datahome.Resolve(), "service-status.json")
}

func (r *StatusRegistry) SetSnapshotPath(path string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.snapshotPath = path
}

// NewStatusRegistry returns an empty registry.
func NewStatusRegistry() *StatusRegistry {
	return &StatusRegistry{statuses: make(map[string]Status)}
}

// Update applies fn to the current Status for id (its zero value if id has
// no entry yet) and stores the result. Read-modify-write, so callers can
// increment counters or set only the fields they know changed.
func (r *StatusRegistry) Update(id string, fn func(*Status)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	st := r.statuses[id]
	fn(&st)
	r.statuses[id] = st
	if r.snapshotPath != "" {
		if err := r.writeSnapshot(); err != nil {
			slog.Error("write service status snapshot", "error", err)
		}
	}
}

func (r *StatusRegistry) writeSnapshot() error {
	if err := os.MkdirAll(filepath.Dir(r.snapshotPath), 0o700); err != nil {
		return err
	}
	statuses := make([]Status, 0, len(r.statuses))
	for _, st := range r.statuses {
		statuses = append(statuses, st)
	}
	sort.Slice(statuses, func(i, j int) bool { return statuses[i].ID < statuses[j].ID })
	data, err := json.Marshal(statusSnapshot{OwnerPID: os.Getpid(), Statuses: statuses})
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(r.snapshotPath), ".service-status-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err := f.Chmod(0o600); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), r.snapshotPath)
}

func ReadStatusSnapshot(path string) ([]Status, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var snapshot statusSnapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return nil, err
	}
	if snapshot.OwnerPID <= 0 || syscall.Kill(snapshot.OwnerPID, 0) != nil {
		for i := range snapshot.Statuses {
			snapshot.Statuses[i].Running = false
			snapshot.Statuses[i].PID = 0
		}
	}
	return snapshot.Statuses, nil
}

// Get returns the current Status for id, if any.
func (r *StatusRegistry) Get(id string) (Status, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	st, ok := r.statuses[id]
	return st, ok
}

// All returns every known Status, sorted by ID for deterministic output.
func (r *StatusRegistry) All() []Status {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Status, 0, len(r.statuses))
	for _, st := range r.statuses {
		out = append(out, st)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
