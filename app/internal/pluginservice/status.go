package pluginservice

import (
	"sort"
	"sync"
	"time"

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

// StatusRegistry is an in-memory, thread-safe home for every declared
// service's Status, owned by one resident process. It has no consumer yet beyond
// the Supervisor that maintains it and this package's own tests; a
// cross-process view is deferred until something needs one, per the
// repository's YAGNI rule — nothing here blocks adding that later.
type StatusRegistry struct {
	mu       sync.Mutex
	statuses map[string]Status
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
