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

type Subscriber struct {
	ThreadTS         string    `json:"thread_ts"`
	ChannelID        string    `json:"channel_id"`
	SessionName      string    `json:"session_name"`
	Since            time.Time `json:"since"`
	DeliveredThrough string    `json:"delivered_through,omitempty"`
}

type subscriptionKey struct{ session, thread string }

type Broker struct {
	mu     sync.RWMutex
	subs   map[subscriptionKey]Subscriber
	path   string
	logger *slog.Logger
}

func NewBroker(path string, logger *slog.Logger) *Broker {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	}
	b := &Broker{subs: make(map[subscriptionKey]Subscriber), path: path, logger: logger}
	if path != "" {
		b.load()
	}
	return b
}

func (b *Broker) Subscribe(s Subscriber) Subscriber {
	b.mu.Lock()
	defer b.mu.Unlock()
	key := subscriptionKey{s.SessionName, s.ThreadTS}
	if old, ok := b.subs[key]; ok {
		if s.DeliveredThrough == "" {
			s.DeliveredThrough = old.DeliveredThrough
		}
		if s.Since.IsZero() {
			s.Since = old.Since
		}
	}
	if s.Since.IsZero() {
		s.Since = time.Now()
	}
	b.subs[key] = s
	b.persistLocked()
	return s
}

func (b *Broker) Unsubscribe(session, thread string) (Subscriber, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	key := subscriptionKey{session, thread}
	s, ok := b.subs[key]
	if ok {
		delete(b.subs, key)
		b.persistLocked()
	}
	return s, ok
}

// A thread-only unsubscribe cannot choose among sessions; released cleanup
// requests have no session name, so accept them only for a unique subscriber.
func (b *Broker) UnsubscribeUniqueThread(thread string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	var found subscriptionKey
	matched := false
	for key := range b.subs {
		if key.thread != thread {
			continue
		}
		if matched {
			return false
		}
		found, matched = key, true
	}
	if !matched {
		return true
	}
	delete(b.subs, found)
	b.persistLocked()
	return true
}

func (b *Broker) UnsubscribeBySession(session string) []Subscriber {
	b.mu.Lock()
	defer b.mu.Unlock()
	var removed []Subscriber
	for key, sub := range b.subs {
		if key.session == session {
			removed = append(removed, sub)
			delete(b.subs, key)
		}
	}
	if len(removed) != 0 {
		b.persistLocked()
	}
	return removed
}

func (b *Broker) Find(session, thread string) (Subscriber, bool) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	s, ok := b.subs[subscriptionKey{session, thread}]
	return s, ok
}

func (b *Broker) ListThread(thread string) []Subscriber {
	b.mu.RLock()
	defer b.mu.RUnlock()
	var out []Subscriber
	for _, s := range b.subs {
		if s.ThreadTS == thread {
			out = append(out, s)
		}
	}
	return out
}

func (b *Broker) MarkDelivered(session, thread, through string) (Subscriber, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	key := subscriptionKey{session, thread}
	s, ok := b.subs[key]
	if !ok || through == "" {
		return s, false
	}
	if s.DeliveredThrough != "" && compareSlackTS(s.DeliveredThrough, through) >= 0 {
		return s, true
	}
	s.DeliveredThrough = through
	b.subs[key] = s
	b.persistLocked()
	return s, true
}

func (b *Broker) BySession(session string) (Subscriber, bool) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if session == "" {
		return Subscriber{}, false
	}
	var found Subscriber
	matched := false
	for _, sub := range b.subs {
		if sub.SessionName != session {
			continue
		}
		if matched {
			return Subscriber{}, false
		}
		found, matched = sub, true
	}
	return found, matched
}

func (b *Broker) List() []Subscriber {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.snapshotLocked()
}

func (b *Broker) snapshotLocked() []Subscriber {
	out := make([]Subscriber, 0, len(b.subs))
	for _, s := range b.subs {
		out = append(out, s)
	}
	return out
}

type persistedState struct {
	Version     int          `json:"version"`
	Subscribers []Subscriber `json:"subscribers"`
}

func (b *Broker) load() {
	data, err := os.ReadFile(b.path)
	if errors.Is(err, fs.ErrNotExist) {
		return
	}
	if err != nil {
		b.logger.Warn("failed to read subscriber state", "error", err)
		return
	}
	var ps persistedState
	if err := json.Unmarshal(data, &ps); err != nil {
		b.logger.Warn("failed to parse subscriber state", "error", err)
		return
	}
	if ps.Version != 2 {
		b.logger.Warn("unsupported subscriber state version; run the migration", "version", ps.Version, "path", b.path)
		return
	}
	for _, s := range ps.Subscribers {
		if s.SessionName == "" || s.ThreadTS == "" || s.ChannelID == "" {
			continue
		}
		b.subs[subscriptionKey{s.SessionName, s.ThreadTS}] = s
	}
}

func (b *Broker) persistLocked() {
	if b.path == "" {
		return
	}
	dir := filepath.Dir(b.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		b.logger.Error("create subscriber state directory", "error", err)
		return
	}
	f, err := os.CreateTemp(dir, ".subscribers-*.json")
	if err != nil {
		b.logger.Error("create subscriber state file", "error", err)
		return
	}
	defer os.Remove(f.Name())
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(persistedState{Version: 2, Subscribers: b.snapshotLocked()}); err != nil {
		f.Close()
		b.logger.Error("write subscriber state", "error", err)
		return
	}
	if err := f.Close(); err != nil {
		b.logger.Error("close subscriber state", "error", err)
		return
	}
	if err := os.Rename(f.Name(), b.path); err != nil {
		b.logger.Error("persist subscriber state", "error", err)
	}
}
