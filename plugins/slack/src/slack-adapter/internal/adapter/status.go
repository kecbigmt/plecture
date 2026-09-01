package adapter

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/slack-go/slack"
)

const (
	defaultStatusTTL   = 15 * time.Minute
	maxLoadingMessages = 10
	// maxLoadingMessageLen is Slack's rendering cap for a loading_messages
	// entry: measured against a live workspace, a 48-character entry
	// renders and a 52-character one is rejected with invalid_arguments.
	maxLoadingMessageLen = 48
)

// ThreadStatusSetter sets a Slack thread's shimmer status line
// (assistant.threads.setStatus). `status` is purely an on/off flag: a
// non-empty value shows the thread's loading_messages (or, absent any,
// Slack's own default text); empty clears it. `status`'s own content is
// never rendered in a channel thread.
type ThreadStatusSetter interface {
	SetThreadStatus(channelID, threadTS, status string, loadingMessages []string) error
}

// validateLoadingMessages rejects a POST /status request over Slack's
// 10-message limit.
func validateLoadingMessages(msgs []string) error {
	if len(msgs) > maxLoadingMessages {
		return fmt.Errorf("loading_messages must have at most %d entries, got %d", maxLoadingMessages, len(msgs))
	}
	return nil
}

// clipLoadingMessages truncates each entry over Slack's rendering cap so a
// long producer text (e.g. a tool command head) degrades to a shorter one
// instead of making the whole /status call fail; the caller's text also
// feeds non-Slack consumers, so the clip happens here rather than at the
// source.
func clipLoadingMessages(msgs []string) []string {
	if len(msgs) == 0 {
		return msgs
	}
	clipped := make([]string, len(msgs))
	for i, msg := range msgs {
		clipped[i] = clipText(msg, maxLoadingMessageLen)
	}
	return clipped
}

// clipText truncates on rune boundaries (loading message text is
// user/tool-provided and may be multibyte) and reserves one rune for the
// ellipsis so the result never exceeds max.
func clipText(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max-1]) + "…"
}

// writeStatusError distinguishes a Slack API rejection from an
// adapter-internal failure. Before this, both surfaced as an opaque 500
// with no log line, so a rejection (e.g. invalid_arguments) was
// indistinguishable from a network error without reading Slack's raw
// response.
func writeStatusError(logger *slog.Logger, w http.ResponseWriter, err error) {
	var slackErr slack.SlackErrorResponse
	if errors.As(err, &slackErr) {
		logger.Warn("status: slack api rejected request",
			"component", "slack-adapter", "event", "status_slack_error", "error", slackErr.Err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		json.NewEncoder(w).Encode(map[string]string{"error": slackErr.Err})
		return
	}
	http.Error(w, err.Error(), http.StatusInternalServerError)
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
