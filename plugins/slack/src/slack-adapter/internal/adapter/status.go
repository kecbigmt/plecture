package adapter

import (
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"
)

const (
	defaultStatusTTL   = 15 * time.Minute
	maxLoadingMessages = 10

	// statusShowFlag is the non-empty `status` string sent when a caller has
	// no more specific text of its own to send (only loading_messages
	// renders in a channel thread — confirmed empirically — so `status`'s
	// content is otherwise irrelevant; it only has to be non-empty to mean
	// "show" rather than "clear").
	statusShowFlag = "active"
)

// ThreadStatusSetter sets a Slack thread's shimmer status line
// (assistant.threads.setStatus). `status` is purely an on/off flag: a
// non-empty value shows the thread's loading_messages (or, absent any,
// Slack's own default text); empty clears it. `status`'s own content is
// never rendered in a channel thread.
type ThreadStatusSetter interface {
	SetThreadStatus(channelID, threadTS, status string, loadingMessages []string) error
}

// validateLoadingMessages is shared by config startup validation and the
// POST /status request path so both reject over Slack's 10-message limit
// the same way.
func validateLoadingMessages(msgs []string) error {
	if len(msgs) > maxLoadingMessages {
		return fmt.Errorf("loading_messages must have at most %d entries, got %d", maxLoadingMessages, len(msgs))
	}
	return nil
}

// StatusManager shows a thread's shimmer status and enforces the TTL
// fallback: a session can end its turn without ever calling reply (e.g. it
// reports on the PR instead), and nothing else would clear the shimmer in
// that case.
type StatusManager struct {
	setter ThreadStatusSetter
	ttl    time.Duration
	logger *slog.Logger

	mu     sync.Mutex
	timers map[string]*time.Timer // keyed by thread_ts
}

func NewStatusManager(setter ThreadStatusSetter, ttl time.Duration, logger *slog.Logger) *StatusManager {
	if ttl <= 0 {
		ttl = defaultStatusTTL
	}
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	}
	return &StatusManager{
		setter: setter,
		ttl:    ttl,
		logger: logger,
		timers: make(map[string]*time.Timer),
	}
}

// Set clears before it sets: a loading_messages entry sent right after an
// earlier status call on the same thread was observed (against a live
// workspace) to flash once and then revert to Slack's own default text,
// while the same entry sent right after an explicit clear renders
// persistently. This can collapse back to a single call if a later check
// shows a direct overwrite rendering reliably too.
//
// It also pushes the TTL timer out on a later call for the same thread
// rather than stacking a second clear.
func (m *StatusManager) Set(channelID, threadTS, status string, loadingMessages []string) error {
	if err := m.setter.SetThreadStatus(channelID, threadTS, "", nil); err != nil {
		return err
	}
	if err := m.setter.SetThreadStatus(channelID, threadTS, status, loadingMessages); err != nil {
		return err
	}
	m.startTimer(channelID, threadTS)
	return nil
}

// Clear cancels any pending TTL timer first, so an idle period afterward
// doesn't fire a redundant (or, once the thread has moved on to a new
// status, incorrect) clear.
func (m *StatusManager) Clear(channelID, threadTS string) error {
	m.cancelTimer(threadTS)
	return m.setter.SetThreadStatus(channelID, threadTS, "", nil)
}

// Stop is for shutdown, not a substitute for Clear: it cancels pending
// timers without touching Slack.
func (m *StatusManager) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for threadTS, t := range m.timers {
		t.Stop()
		delete(m.timers, threadTS)
	}
}

func (m *StatusManager) startTimer(channelID, threadTS string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if t, ok := m.timers[threadTS]; ok {
		t.Stop()
	}
	m.timers[threadTS] = time.AfterFunc(m.ttl, func() {
		m.mu.Lock()
		delete(m.timers, threadTS)
		m.mu.Unlock()
		if err := m.setter.SetThreadStatus(channelID, threadTS, "", nil); err != nil {
			m.logger.Error("status ttl clear failed",
				"component", "slack-adapter", "event", "status_ttl_clear_error",
				"thread_ts", threadTS, "error", err)
		}
	})
}

func (m *StatusManager) cancelTimer(threadTS string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if t, ok := m.timers[threadTS]; ok {
		t.Stop()
		delete(m.timers, threadTS)
	}
}
