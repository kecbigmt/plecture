// Package watcher implements the resident GitHub watcher daemon: it keeps a
// subscription registry, polls GitHub via the gh CLI, and forwards change
// notifications to the configured delivery path.
//
// Component boundaries (tools/tws/CLAUDE.md): the watcher imports no tws
// internals.
package watcher

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"github.com/kecbigmt/plect/contracts/atomicfile"
)

// registryVersion is the on-disk subscription format version. It was bumped to 2
// when the key changed from session-only (1:1) to (session, resource) so one
// session can funnel several PRs. A file at any other version is
// discarded on load: the github_watch task re-subscribes on the next `tws up`,
// so there is nothing to migrate.
const registryVersion = 2

// Subscription is one watched resource bound to a tws session. A session may
// have several (one per resource), so the registry is keyed by (session,
// resource), not session alone.
type Subscription struct {
	SessionName string `json:"session_name"`
	Resource    string `json:"resource"`
	// Branch is the session's working branch; used to discover the linked PR
	// for issue resources. Optional.
	Branch string `json:"branch,omitempty"`
	// Last holds the most recent observed values, used for change detection
	// across daemon restarts.
	Last map[string]string `json:"last,omitempty"`
}

// subKey is the registry key for a subscription: (session, resource). The NUL
// separator can't appear in either field, so the composite is unambiguous.
func subKey(session, resource string) string {
	return session + "\x00" + resource
}

// registry is the on-disk subscription store.
type registry struct {
	Version       int                      `json:"version"`
	Subscriptions map[string]*Subscription `json:"subscriptions"` // keyed by subKey(session, resource)
}

// Store persists subscriptions under flock so subscribe/unsubscribe (task
// scripts) and the serve loop (daemon) can interleave safely.
type Store struct {
	path string
}

// NewStore creates a Store rooted at dir; empty dir defaults to
// ~/.local/share/github-watcher (XDG).
func NewStore(dir string) *Store {
	if dir == "" {
		dataHome := os.Getenv("XDG_DATA_HOME")
		if dataHome == "" {
			home, _ := os.UserHomeDir()
			dataHome = filepath.Join(home, ".local", "share")
		}
		dir = filepath.Join(dataHome, "github-watcher")
	}
	return &Store{path: filepath.Join(dir, "subscriptions.json")}
}

// Subscribe upserts a (session, resource) subscription. Re-subscribing the same
// pair refreshes branch but keeps the observed baseline so a task
// retry doesn't replay old notifications.
func (s *Store) Subscribe(sub Subscription) error {
	return s.update(func(r *registry) error {
		key := subKey(sub.SessionName, sub.Resource)
		if existing, ok := r.Subscriptions[key]; ok {
			// Re-subscribe is additive/idempotent: a non-empty field updates,
			// an empty one preserves what's stored. This keeps a runtime
			// `tws subscribe` (which omits --branch) from wiping the branch a
			// dispatch-time auto-subscribe recorded for the same resource —
			// losing it would break an issue session's linked-PR resolution.
			if sub.Branch != "" {
				existing.Branch = sub.Branch
			}
			return nil
		}
		r.Subscriptions[key] = &sub
		return nil
	})
}

// Unsubscribe removes every subscription for a session (all its resources) —
// the session-destroyed path. Removing a non-existent session succeeds (cleanup
// scripts must be idempotent).
func (s *Store) Unsubscribe(sessionName string) error {
	return s.update(func(r *registry) error {
		for key, sub := range r.Subscriptions {
			if sub != nil && sub.SessionName == sessionName {
				delete(r.Subscriptions, key)
			}
		}
		return nil
	})
}

// UnsubscribeResource removes a single (session, resource) subscription, leaving
// the session's other watched resources in place. Idempotent.
func (s *Store) UnsubscribeResource(sessionName, resource string) error {
	return s.update(func(r *registry) error {
		delete(r.Subscriptions, subKey(sessionName, resource))
		return nil
	})
}

// All returns the current subscriptions, keyed by subKey(session, resource).
func (s *Store) All() (map[string]*Subscription, error) {
	r, err := s.load()
	if err != nil {
		return nil, err
	}
	return r.Subscriptions, nil
}

// SetLast persists the observed baseline for one (session, resource)
// subscription (no-op when it has been removed mid-poll).
func (s *Store) SetLast(sessionName, resource string, last map[string]string) error {
	return s.update(func(r *registry) error {
		if sub, ok := r.Subscriptions[subKey(sessionName, resource)]; ok {
			sub.Last = last
		}
		return nil
	})
}

func (s *Store) load() (*registry, error) {
	r := &registry{Version: registryVersion, Subscriptions: map[string]*Subscription{}}
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return r, nil
		}
		return nil, err
	}
	if err := json.Unmarshal(data, r); err != nil {
		return nil, fmt.Errorf("parse %s: %w", s.path, err)
	}
	// A file from an older (or newer) format is discarded, not migrated: the
	// task re-subscribes on the next `tws up`. Return a fresh empty registry
	// at the current version so the next write persists the upgrade.
	if r.Version != registryVersion {
		return &registry{Version: registryVersion, Subscriptions: map[string]*Subscription{}}, nil
	}
	if r.Subscriptions == nil {
		r.Subscriptions = map[string]*Subscription{}
	}
	return r, nil
}

// update is the locked read-modify-write primitive.
func (s *Store) update(fn func(*registry) error) error {
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	lock, err := os.OpenFile(s.path+".lock", os.O_CREATE|os.O_RDONLY, 0o644)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)

	r, err := s.load()
	if err != nil {
		return err
	}
	if err := fn(r); err != nil {
		return err
	}
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return atomicfile.Write(s.path, data)
}
