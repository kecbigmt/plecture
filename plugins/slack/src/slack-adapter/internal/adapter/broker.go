package adapter

import (
	"encoding/json"
	"errors"
	"fmt"
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
type pendingMentionKey struct{ channel, thread string }

type pendingMention struct {
	ChannelID string    `json:"channel_id"`
	ThreadTS  string    `json:"thread_ts"`
	MentionTS string    `json:"mention_ts"`
	Recorded  time.Time `json:"recorded"`
}

const pendingMentionLifetime = 24 * time.Hour

const mentionSubscriptionKey = "mentions"
const mentionWatermarkRetention = 30 * 24 * time.Hour

type mentionWatermark struct {
	SessionName string    `json:"session_name"`
	ThreadTS    string    `json:"thread_ts"`
	Through     string    `json:"through"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type Broker struct {
	mu       sync.RWMutex
	subs     map[subscriptionKey]Subscriber
	pending  map[pendingMentionKey]pendingMention
	mentions map[subscriptionKey]mentionWatermark
	path     string
	logger   *slog.Logger
	loadErr  error
}

func NewBroker(path string, logger *slog.Logger) *Broker {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	}
	b := &Broker{subs: make(map[subscriptionKey]Subscriber), pending: make(map[pendingMentionKey]pendingMention), mentions: make(map[subscriptionKey]mentionWatermark), path: path, logger: logger}
	if path != "" {
		b.loadErr = b.load()
	}
	return b
}

func (b *Broker) RecordPendingMention(channel, thread, mention string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now()
	pruned := b.prunePendingLocked(now)
	key := pendingMentionKey{channel, thread}
	if old, ok := b.pending[key]; ok && compareSlackTS(old.MentionTS, mention) > 0 {
		if pruned {
			b.persistLocked()
		}
		return
	}
	b.pending[key] = pendingMention{ChannelID: channel, ThreadTS: thread, MentionTS: mention, Recorded: now}
	b.persistLocked()
}

func (b *Broker) PendingMention(channel, thread string) string {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.prunePendingLocked(time.Now()) {
		b.persistLocked()
	}
	return b.pending[pendingMentionKey{channel, thread}].MentionTS
}

func (b *Broker) ConsumePendingMention(channel, thread, mention string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	key := pendingMentionKey{channel, thread}
	if b.pending[key].MentionTS == mention {
		delete(b.pending, key)
		b.persistLocked()
	}
}

func (b *Broker) prunePendingLocked(now time.Time) bool {
	changed := false
	for key, mention := range b.pending {
		if mention.Recorded.IsZero() || now.Sub(mention.Recorded) >= pendingMentionLifetime {
			delete(b.pending, key)
			changed = true
		}
	}
	return changed
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

func (b *Broker) ListMentions() []Subscriber {
	return b.ListThread(mentionSubscriptionKey)
}

func (b *Broker) MentionWatermark(session, thread string) string {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.mentions[subscriptionKey{session, thread}].Through
}

func (b *Broker) MarkMentionDelivered(session, thread, through string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	key := subscriptionKey{session, thread}
	if _, ok := b.subs[subscriptionKey{session, mentionSubscriptionKey}]; !ok {
		return
	}
	if old := b.mentions[key].Through; old != "" && compareSlackTS(old, through) >= 0 {
		return
	}
	b.mentions[key] = mentionWatermark{SessionName: session, ThreadTS: thread, Through: through, UpdatedAt: time.Now()}
	b.persistLocked()
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
	Version           int                `json:"version"`
	Subscribers       []Subscriber       `json:"subscribers"`
	PendingMentions   []pendingMention   `json:"pending_mentions,omitempty"`
	MentionWatermarks []mentionWatermark `json:"mention_watermarks,omitempty"`
}

func (b *Broker) load() error {
	data, err := os.ReadFile(b.path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read subscriber state: %w", err)
	}
	var ps persistedState
	if err := json.Unmarshal(data, &ps); err != nil {
		return fmt.Errorf("parse subscriber state: %w", err)
	}
	if ps.Version != 2 {
		return fmt.Errorf("unsupported subscriber state version %d at %s; run the migration", ps.Version, b.path)
	}
	for _, s := range ps.Subscribers {
		if s.SessionName == "" || s.ThreadTS == "" || (s.ChannelID == "" && s.ThreadTS != mentionSubscriptionKey) {
			continue
		}
		b.subs[subscriptionKey{s.SessionName, s.ThreadTS}] = s
	}
	for _, mention := range ps.PendingMentions {
		if mention.ChannelID == "" || mention.ThreadTS == "" || mention.MentionTS == "" {
			continue
		}
		b.pending[pendingMentionKey{mention.ChannelID, mention.ThreadTS}] = mention
	}
	b.prunePendingLocked(time.Now())
	for _, wm := range ps.MentionWatermarks {
		if wm.SessionName != "" && isSlackTimestampShape(wm.ThreadTS) && isSlackTimestampShape(wm.Through) && time.Since(wm.UpdatedAt) < mentionWatermarkRetention {
			b.mentions[subscriptionKey{wm.SessionName, wm.ThreadTS}] = wm
		}
	}
	return nil
}

func (b *Broker) persistLocked() {
	if b.path == "" || b.loadErr != nil {
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
	pending := make([]pendingMention, 0, len(b.pending))
	for _, mention := range b.pending {
		pending = append(pending, mention)
	}
	watermarks := make([]mentionWatermark, 0, len(b.mentions))
	for key, wm := range b.mentions {
		if time.Since(wm.UpdatedAt) >= mentionWatermarkRetention {
			delete(b.mentions, key)
			continue
		}
		watermarks = append(watermarks, wm)
	}
	if err := enc.Encode(persistedState{Version: 2, Subscribers: b.snapshotLocked(), PendingMentions: pending, MentionWatermarks: watermarks}); err != nil {
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
