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
	ThreadTS         string    `json:"thread_ts"`
	ChannelID        string    `json:"channel_id"`
	SocketPath       string    `json:"socket_path"`
	SessionName      string    `json:"session_name"`
	Since            time.Time `json:"since"`
	DeliveredThrough string    `json:"delivered_through,omitempty"`
}

// Tombstone preserves a thread's delivery watermark across Unsubscribe, so
// that plect down / up (or an ECS task replacement doing the same) does not
// re-deliver the transcript the resumed session already saw. It is scoped to
// the (thread_ts, session_name) pair that produced it: a different session
// later binding the same thread is a new subscription, not a resumed one,
// and gets no watermark.
type Tombstone struct {
	ThreadTS         string    `json:"thread_ts"`
	SessionName      string    `json:"session_name"`
	DeliveredThrough string    `json:"delivered_through"`
	UnsubscribedAt   time.Time `json:"unsubscribed_at"`
}

// tombstoneTTL bounds how long an unsubscribed thread's watermark survives.
// Fixed rather than configurable: this is a single-user adapter and the
// value only needs to outlast the gap between a deploy's down and up, not
// tune to a workload.
const tombstoneTTL = 30 * 24 * time.Hour

// Broker is the in-memory subscriber registry. With a non-empty persistence
// path, every mutation is written tmp→rename and reloaded on startup so a
// restart does not require producers to re-register.
type Broker struct {
	mu         sync.RWMutex
	subs       map[string]Subscriber
	tombstones map[string]Tombstone
	path       string
	logger     *slog.Logger
}

// NewBroker returns a Broker. A non-empty path enables disk persistence.
func NewBroker(path string, logger *slog.Logger) *Broker {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	}
	b := &Broker{
		subs:       make(map[string]Subscriber),
		tombstones: make(map[string]Tombstone),
		path:       path,
		logger:     logger,
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
	if existing, ok := b.subs[s.ThreadTS]; ok && s.DeliveredThrough == "" {
		s.DeliveredThrough = existing.DeliveredThrough
	}
	if s.DeliveredThrough == "" {
		key := tombstoneKey(s.ThreadTS, s.SessionName)
		if tomb, ok := b.tombstones[key]; ok {
			s.DeliveredThrough = tomb.DeliveredThrough
			delete(b.tombstones, key)
		}
	}
	b.subs[s.ThreadTS] = s
	b.persistLocked()
	return s
}

// Unsubscribe returns the removed subscriber, or zero value + false if absent.
//
// A non-empty watermark is preserved as a tombstone rather than dropped, so
// a same-session resubscribe (Subscribe, above) can restore it. A
// subscriber that never received anything leaves no tombstone — there is
// nothing to protect against redelivery.
func (b *Broker) Unsubscribe(threadTS string) (Subscriber, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	s, ok := b.subs[threadTS]
	if !ok {
		return Subscriber{}, false
	}
	delete(b.subs, threadTS)
	if s.DeliveredThrough != "" {
		key := tombstoneKey(s.ThreadTS, s.SessionName)
		b.tombstones[key] = Tombstone{
			ThreadTS:         s.ThreadTS,
			SessionName:      s.SessionName,
			DeliveredThrough: s.DeliveredThrough,
			UnsubscribedAt:   time.Now(),
		}
	}
	b.persistLocked()
	return s, true
}

func tombstoneKey(threadTS, sessionName string) string {
	return threadTS + "\x00" + sessionName
}

func (b *Broker) Find(threadTS string) (Subscriber, bool) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	s, ok := b.subs[threadTS]
	return s, ok
}

func (b *Broker) MarkDelivered(threadTS, deliveredThrough string) (Subscriber, bool) {
	if deliveredThrough == "" {
		return Subscriber{}, false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	s, ok := b.subs[threadTS]
	if !ok {
		return Subscriber{}, false
	}
	if s.DeliveredThrough != "" && compareSlackTS(s.DeliveredThrough, deliveredThrough) >= 0 {
		return s, true
	}
	s.DeliveredThrough = deliveredThrough
	b.subs[threadTS] = s
	b.persistLocked()
	return s, true
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
	return b.subsSnapshotLocked()
}

// subsSnapshotLocked must be called with b.mu held.
func (b *Broker) subsSnapshotLocked() []Subscriber {
	out := make([]Subscriber, 0, len(b.subs))
	for _, s := range b.subs {
		out = append(out, s)
	}
	return out
}

// tombstonesSnapshotLocked must be called with b.mu held.
func (b *Broker) tombstonesSnapshotLocked() []Tombstone {
	out := make([]Tombstone, 0, len(b.tombstones))
	for _, t := range b.tombstones {
		out = append(out, t)
	}
	return out
}

// pruneTombstonesLocked drops tombstones older than tombstoneTTL. Called on
// load and on every persist (persistLocked), per the fixed 30-day retention
// — no config key, since a longer-lived gap between unsubscribe and
// resubscribe means the resuming session no longer needs the redelivery
// guard.
func (b *Broker) pruneTombstonesLocked() {
	cutoff := time.Now().Add(-tombstoneTTL)
	for k, t := range b.tombstones {
		if t.UnsubscribedAt.Before(cutoff) {
			delete(b.tombstones, k)
		}
	}
}

// persistedState is subscribers.json's on-disk shape.
type persistedState struct {
	Subscribers []Subscriber `json:"subscribers"`
	Tombstones  []Tombstone  `json:"tombstones,omitempty"`
}

// decodeState parses subscribers.json. Two shapes are accepted: the current
// {"subscribers": [...], "tombstones": [...]} envelope, and the bare
// []Subscriber array written before tombstones existed. This is read-only
// compatibility, not migration code — a file in the old shape has no
// tombstones key, which just means an empty tombstones list.
func decodeState(data []byte) (persistedState, error) {
	var ps persistedState
	if err := json.Unmarshal(data, &ps); err == nil {
		return ps, nil
	}
	var legacy []Subscriber
	if err := json.Unmarshal(data, &legacy); err != nil {
		return persistedState{}, err
	}
	return persistedState{Subscribers: legacy}, nil
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
	ps, err := decodeState(data)
	if err != nil {
		b.logger.Warn("failed to parse subscriber state, starting empty", "path", b.path, "error", err)
		return
	}
	for _, s := range ps.Subscribers {
		if s.ThreadTS == "" {
			continue
		}
		b.subs[s.ThreadTS] = s
	}
	for _, t := range ps.Tombstones {
		if t.ThreadTS == "" {
			continue
		}
		b.tombstones[tombstoneKey(t.ThreadTS, t.SessionName)] = t
	}
	b.pruneTombstonesLocked()
	b.logger.Info("restored subscribers", "count", len(b.subs), "tombstones", len(b.tombstones), "path", b.path)
}

// persistLocked prunes expired tombstones, then writes the full snapshot
// (subscribers + tombstones) atomically (tmp → rename), deliberately
// without the fsync that contracts/atomicfile applies to plect's durable
// paths (state, subscription registries, event cursors): losing the last
// write before a crash just means a stale reload, self-healed by producers
// re-registering, so paying an fsync on every subscribe/unsubscribe isn't
// worth it. Failures are logged but never propagate: the in-memory mutation
// the HTTP handler just acknowledged outranks the on-disk copy. Must be
// called with b.mu held.
func (b *Broker) persistLocked() {
	b.pruneTombstonesLocked()
	if b.path == "" {
		return
	}
	ps := persistedState{
		Subscribers: b.subsSnapshotLocked(),
		Tombstones:  b.tombstonesSnapshotLocked(),
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
	if err := enc.Encode(ps); err != nil {
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
