package adapter

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/slack-go/slack"
)

const defaultStatusTTL = 15 * time.Minute

type ThreadStatusSetter interface {
	SetThreadStatus(channelID, threadTS, status string) error
}

func writeStatusError(logger *slog.Logger, w http.ResponseWriter, err error) {
	var slackErr slack.SlackErrorResponse
	if errors.As(err, &slackErr) {
		logger.Warn("status: slack api rejected request", "component", "slack-adapter", "event", "status_slack_error", "error", slackErr.Err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		json.NewEncoder(w).Encode(map[string]string{"error": slackErr.Err})
		return
	}
	http.Error(w, err.Error(), http.StatusInternalServerError)
}

type statusThread struct {
	mu          sync.Mutex
	generation  uint64
	turnID      string
	priorTurnID string
	known       bool
	processing  bool
	timer       *time.Timer
}

type StatusManager struct {
	setter  ThreadStatusSetter
	ttl     time.Duration
	logger  *slog.Logger
	mu      sync.Mutex
	threads map[threadKey]*statusThread
}

func NewStatusManager(setter ThreadStatusSetter, ttl time.Duration, logger *slog.Logger) *StatusManager {
	if ttl <= 0 {
		ttl = defaultStatusTTL
	}
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	}
	return &StatusManager{setter: setter, ttl: ttl, logger: logger, threads: make(map[threadKey]*statusThread)}
}

func (m *StatusManager) thread(channelID, threadTS string) *statusThread {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := threadKey{channelID: channelID, threadTS: threadTS}
	if m.threads[key] == nil {
		m.threads[key] = &statusThread{}
	}
	return m.threads[key]
}

func (m *StatusManager) Begin(channelID, threadTS, turnID string) error {
	st := m.thread(channelID, threadTS)
	st.mu.Lock()
	defer st.mu.Unlock()
	if turnID == "" {
		m.logger.Warn("status activity missing turn correlation", "channel_id", channelID, "thread_ts", threadTS, "generation", st.generation)
	}
	if turnID != "" && (turnID == st.priorTurnID || turnID == st.turnID && !st.processing) {
		m.logger.Info("stale status activity ignored", "channel_id", channelID, "thread_ts", threadTS, "turn_id", turnID, "generation", st.generation)
		return nil
	}
	if turnID != "" && st.turnID != "" && turnID != st.turnID {
		st.priorTurnID = st.turnID
	}
	st.generation++
	st.turnID = turnID
	st.known = true
	st.processing = true
	if st.timer != nil {
		st.timer.Stop()
	}
	if err := m.setter.SetThreadStatus(channelID, threadTS, "processing"); err != nil {
		m.logger.Warn("status transition failed", "channel_id", channelID, "thread_ts", threadTS, "turn_id", turnID, "generation", st.generation, "status", "processing", "error", err)
		return err
	}
	m.logger.Info("status transition delivered", "channel_id", channelID, "thread_ts", threadTS, "turn_id", turnID, "generation", st.generation, "status", "processing")
	m.startOverdueTimer(st, channelID, threadTS)
	return nil
}

func (m *StatusManager) End(channelID, threadTS, turnID string) error {
	st := m.thread(channelID, threadTS)
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.known && !st.processing {
		return nil
	}
	if turnID != "" && st.turnID != "" && turnID != st.turnID {
		m.logger.Info("stale status completion ignored", "channel_id", channelID, "thread_ts", threadTS, "turn_id", turnID, "current_turn_id", st.turnID, "generation", st.generation)
		return nil
	}
	if turnID == "" || st.turnID == "" {
		m.logger.Warn("status completion missing turn correlation", "channel_id", channelID, "thread_ts", threadTS, "generation", st.generation)
	}
	if err := m.setter.SetThreadStatus(channelID, threadTS, "active"); err != nil {
		m.logger.Warn("status transition failed", "channel_id", channelID, "thread_ts", threadTS, "turn_id", turnID, "generation", st.generation, "status", "active", "error", err)
		return err
	}
	m.logger.Info("status transition delivered", "channel_id", channelID, "thread_ts", threadTS, "turn_id", turnID, "generation", st.generation, "status", "active")
	st.processing = false
	st.known = true
	st.turnID = turnID
	if st.timer != nil {
		st.timer.Stop()
		st.timer = nil
	}
	return nil
}

func (m *StatusManager) Deliver(channelID, threadTS, turnID string, deliver func() error) error {
	return m.DeliveryContext(channelID, threadTS, turnID, func(_ bool, _ string) error {
		return deliver()
	})
}

// DeliveryContext holds the status lock through delivery and gives late
// content the current session status, so its completion cannot undo a newer
// turn's processing state or leave an already completed turn processing.
func (m *StatusManager) DeliveryContext(channelID, threadTS, turnID string, deliver func(late bool, currentStatus string) error) error {
	st := m.thread(channelID, threadTS)
	st.mu.Lock()
	defer st.mu.Unlock()
	late := turnID != "" && (turnID == st.priorTurnID || turnID == st.turnID && !st.processing)
	currentStatus := "processing"
	if st.known && !st.processing {
		currentStatus = "active"
	}
	if late {
		m.logger.Info("late content delivered", "channel_id", channelID, "thread_ts", threadTS, "turn_id", turnID, "generation", st.generation, "current_status", currentStatus)
	}
	return deliver(late, currentStatus)
}

func (m *StatusManager) startOverdueTimer(st *statusThread, channelID, threadTS string) {
	generation := st.generation
	st.timer = time.AfterFunc(m.ttl, func() {
		st.mu.Lock()
		defer st.mu.Unlock()
		if st.generation == generation && st.processing {
			m.logger.Warn("status processing overdue", "component", "slack-adapter", "event", "status_overdue", "channel_id", channelID, "thread_ts", threadTS, "turn_id", st.turnID, "generation", generation)
		}
	})
}

func (m *StatusManager) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, st := range m.threads {
		st.mu.Lock()
		if st.timer != nil {
			st.timer.Stop()
			st.timer = nil
		}
		st.mu.Unlock()
	}
}
