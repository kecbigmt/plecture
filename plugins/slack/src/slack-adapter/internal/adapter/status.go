package adapter

import (
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"
)

const (
	// defaultStatusText is workflow-neutral: the plugin must not carry
	// workflow knowledge (e.g. "reviewing…"), only a generic "something is
	// happening" signal.
	defaultStatusText  = "is thinking…"
	defaultStatusTTL   = 15 * time.Minute
	maxLoadingMessages = 10
)

// ThreadStatusSetter sets or clears a Slack thread's shimmer status line
// (assistant.threads.setStatus). An empty status clears it.
type ThreadStatusSetter interface {
	SetThreadStatus(channelID, threadTS, status string, loadingMessages []string) error
}

// validateLoadingMessages enforces Slack's assistant.threads.setStatus limit
// of 10 loading_messages. Shared by config startup validation and the
// POST /status request path so both reject the same way.
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

// NewStatusManager returns a StatusManager. ttl <= 0 falls back to
// defaultStatusTTL.
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

// Set shows status on the thread and (re)starts the TTL timer that clears
// it. A later Set on the same thread pushes the timer out again rather than
// stacking a second clear.
func (m *StatusManager) Set(channelID, threadTS, status string, loadingMessages []string) error {
	if err := m.setter.SetThreadStatus(channelID, threadTS, status, loadingMessages); err != nil {
		return err
	}
	m.startTimer(channelID, threadTS)
	return nil
}

// Clear clears the thread's status immediately and cancels any pending TTL
// timer, so an idle period afterward doesn't fire a redundant (or, if the
// thread has since moved on to a new status, incorrect) clear.
func (m *StatusManager) Clear(channelID, threadTS string) error {
	m.cancelTimer(threadTS)
	return m.setter.SetThreadStatus(channelID, threadTS, "", nil)
}

// Stop cancels every pending TTL timer without clearing status on Slack.
// Callers use this on shutdown, not as a substitute for Clear.
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
