package adapter

import (
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Subscriber binds a Slack thread to a channel-server socket. thread_ts is
// the routing key for Slack → channel-server. SessionName is the routing key
// for external notifications (POST /notify) that don't know about thread_ts.
type Subscriber struct {
	ThreadTS    string    `json:"thread_ts"`
	ChannelID   string    `json:"channel_id"`
	SocketPath  string    `json:"socket_path"`
	SessionName string    `json:"session_name"`
	Since       time.Time `json:"since"`
}

// Broker is the in-memory subscriber registry. With a non-empty persistence
// path, every mutation is written tmp→rename and reloaded on startup so a
// restart does not require producers to re-register.
type Broker struct {
	mu     sync.RWMutex
	subs   map[string]Subscriber
	path   string
	logger *slog.Logger
}

// NewBroker returns a Broker. A non-empty path enables disk persistence.
func NewBroker(path string, logger *slog.Logger) *Broker {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	}
	b := &Broker{
		subs:   make(map[string]Subscriber),
		path:   path,
		logger: logger,
	}
	if path != "" {
		b.load()
	}
	return b
}

// Subscribe inserts or replaces a subscription, defaulting Since to now.
//
// The mutex is held through persist so the on-disk order matches the
// in-memory order; releasing the lock before write lets a stale snapshot
// land after a newer one and resurrect a removed entry on next load.
func (b *Broker) Subscribe(s Subscriber) Subscriber {
	if s.Since.IsZero() {
		s.Since = time.Now()
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.subs[s.ThreadTS] = s
	b.persist(b.snapshotLocked())
	return s
}

// Unsubscribe returns the removed subscriber, or zero value + false if absent.
func (b *Broker) Unsubscribe(threadTS string) (Subscriber, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	s, ok := b.subs[threadTS]
	if ok {
		delete(b.subs, threadTS)
		b.persist(b.snapshotLocked())
	}
	return s, ok
}

func (b *Broker) Find(threadTS string) (Subscriber, bool) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	s, ok := b.subs[threadTS]
	return s, ok
}

// BySession returns the first subscriber whose SessionName matches.
// Returns zero value + false if no match (including for empty session_name,
// which migrated entries may carry).
//
// Linear scan over b.subs. Single-digit subscriber counts in practice;
// switch to a reverse map keyed by SessionName if that ever changes.
func (b *Broker) BySession(sessionName string) (Subscriber, bool) {
	if sessionName == "" {
		return Subscriber{}, false
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	for _, s := range b.subs {
		if s.SessionName == sessionName {
			return s, true
		}
	}
	return Subscriber{}, false
}

func (b *Broker) List() []Subscriber {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.snapshotLocked()
}

// snapshotLocked must be called with b.mu held.
func (b *Broker) snapshotLocked() []Subscriber {
	out := make([]Subscriber, 0, len(b.subs))
	for _, s := range b.subs {
		out = append(out, s)
	}
	return out
}

// load reads b.path. Missing / corrupt → start empty (next /subscribe
// rewrites the file cleanly).
func (b *Broker) load() {
	data, err := os.ReadFile(b.path)
	if errors.Is(err, fs.ErrNotExist) {
		b.logger.Info("no subscriber state to restore", "path", b.path)
		return
	}
	if err != nil {
		b.logger.Warn("failed to read subscriber state, starting empty", "path", b.path, "error", err)
		return
	}
	var subs []Subscriber
	if err := json.Unmarshal(data, &subs); err != nil {
		b.logger.Warn("failed to parse subscriber state, starting empty", "path", b.path, "error", err)
		return
	}
	for _, s := range subs {
		if s.ThreadTS == "" {
			continue
		}
		b.subs[s.ThreadTS] = s
	}
	b.logger.Info("restored subscribers", "count", len(b.subs), "path", b.path)
}

// persist writes subs to b.path atomically (tmp → rename), deliberately
// without the fsync that contracts/atomicfile applies to sennit's durable paths
// (state, subscription registries, event cursors): losing the last write
// before a crash just means a stale reload, self-healed by producers
// re-registering, so paying an fsync on every subscribe/unsubscribe isn't
// worth it. Failures are logged but never propagate: the in-memory
// registration the HTTP handler just acknowledged outranks the on-disk copy.
func (b *Broker) persist(subs []Subscriber) {
	if b.path == "" {
		return
	}
	dir := filepath.Dir(b.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		b.logger.Warn("failed to create subscriber state directory", "path", dir, "error", err)
		return
	}
	tmp, err := os.CreateTemp(dir, ".subscribers-*.json")
	if err != nil {
		b.logger.Warn("failed to create temp subscriber state file", "dir", dir, "error", err)
		return
	}
	tmpPath := tmp.Name()
	enc := json.NewEncoder(tmp)
	enc.SetIndent("", "  ")
	if err := enc.Encode(subs); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		b.logger.Warn("failed to encode subscriber state", "error", err)
		return
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		b.logger.Warn("failed to close temp subscriber state file", "error", err)
		return
	}
	if err := os.Rename(tmpPath, b.path); err != nil {
		os.Remove(tmpPath)
		b.logger.Warn("failed to rename subscriber state file", "from", tmpPath, "to", b.path, "error", err)
	}
}
