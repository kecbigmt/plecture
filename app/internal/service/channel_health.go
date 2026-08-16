package service

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/domain"
	"github.com/kecbigmt/plecture/app/internal/state"
	"github.com/kecbigmt/plecture/contracts/event"
	contract "github.com/kecbigmt/plecture/contracts/state"
)

// escalationKindChannelDeliveryFailed names the terminal escalation this
// file pushes, matching the naming convention already established by
// escalationKindDoneWhenNonConvergence and health's "health."+state values.
// One constant covers both streaks (validation and delivery): from a
// parent's point of view they mean the same thing — this session's event
// channel isn't working — and the escalation's channel_failure_kind metadata
// (see pushChannelHealthEscalation) already carries the distinction.
const escalationKindChannelDeliveryFailed = "delivery.failed"

// channelHealthFailureThreshold and channelHealthFailureAge are the two ways
// an open channel failure streak (internal/dispatch persists it as
// contract.ChannelHealth) crosses the escalation threshold: enough
// consecutive failures in a row, or open long enough. The values mirror two
// defaults already established elsewhere in this codebase —
// channel.DefaultRetryPolicy's MaxAttempts and
// config.DefaultHealthcheckConfig's Period — rather than introducing new
// tuning constants.
const (
	channelHealthFailureThreshold = 3
	channelHealthFailureAge       = 5 * time.Minute
)

// CheckChannelHealth escalates a session's open event-channel failure
// streaks to a live ancestor, exactly once per episode, once a streak
// crosses channelHealthFailureThreshold consecutive failures or has been
// open longer than channelHealthFailureAge. Validation (checked once, at a
// dispatcher's build) and delivery (checked per event) are two entirely
// independent streaks — see contract.Session.ChannelValidationHealth /
// ChannelDeliveryHealth — so both are checked and may each escalate on their
// own; a session can simultaneously have one declared channel that never
// resolves and another that resolves but times out, and a reader needs both
// signals, not just whichever happened to be checked last. A later success
// of the matching kind (internal/dispatch again) clears that one streak,
// ending its episode; a subsequent failure of that kind starts a new one and
// is free to escalate again. Returns whether at least one escalation was
// pushed.
func CheckChannelHealth(cfg *config.Config, store *state.Store, sessionName string) (bool, error) {
	s, err := store.GetE(sessionName)
	if err != nil {
		return false, err
	}
	if s == nil {
		return false, &Error{Code: ErrSessionNotFound, Message: fmt.Sprintf("session %q not found", sessionName)}
	}
	validationPushed := checkChannelHealthStreak(cfg, store, sessionName, s.ChannelValidationHealth, contract.ChannelFailureKindValidation)
	deliveryPushed := checkChannelHealthStreak(cfg, store, sessionName, s.ChannelDeliveryHealth, contract.ChannelFailureKindDelivery)
	return validationPushed || deliveryPushed, nil
}

func checkChannelHealthStreak(cfg *config.Config, store *state.Store, sessionName string, ch *contract.ChannelHealth, kind string) bool {
	if ch == nil || ch.ConsecutiveFailures == 0 || !ch.EscalatedAt.IsZero() {
		return false
	}
	crossed := ch.ConsecutiveFailures >= channelHealthFailureThreshold ||
		(!ch.FirstFailureAt.IsZero() && time.Since(ch.FirstFailureAt) >= channelHealthFailureAge)
	if !crossed {
		return false
	}
	if !pushChannelHealthEscalation(cfg, store, sessionName, ch, kind) {
		return false
	}
	persistChannelHealthEscalation(store, sessionName, kind)
	return true
}

func pushChannelHealthEscalation(cfg *config.Config, store *state.Store, origin string, ch *contract.ChannelHealth, kind string) bool {
	meta := map[string]string{
		"escalation_kind":      escalationKindChannelDeliveryFailed,
		"channel_failure_kind": kind,
	}
	if ch.LastChannel != "" {
		meta["channel"] = ch.LastChannel
	}
	if ch.LastError != "" {
		meta["error"] = ch.LastError
	}
	// The episode's own start time makes a stable dedup key for its whole
	// lifetime — reusing it (rather than a running counter, like health's
	// notifyCount) is enough here because this path only ever fires once per
	// episode: unlike health, there is no renotify-while-still-failing case
	// to distinguish. kind is part of the key so the validation and delivery
	// streaks, which are independent episodes, can never collide even if
	// they happen to open at the same instant.
	dedupKey := fmt.Sprintf("%s|channel|%s|%s", origin, kind, ch.FirstFailureAt.UTC().Format(time.RFC3339Nano))
	id, wakeErr, err := publishHealthEscalationToLiveAncestor(cfg, store, origin, TerminalParams{
		Type:     event.TypeTerminalEscalate,
		Summary:  fmt.Sprintf("%s event channel %s is failing", origin, kind),
		Body:     channelHealthEscalationBody(origin, ch, kind),
		Metadata: meta,
		DedupKey: dedupKey,
	})
	if err != nil {
		slog.Warn("publish channel health escalation failed", "session", origin, "kind", kind, "error", err)
		return false
	}
	if id == "" {
		localID, _, localErr := publishTerminalTo(cfg, store, origin, origin, false, TerminalParams{
			Type:     event.TypeTerminalDead,
			Summary:  fmt.Sprintf("%s channel health escalation is undeliverable", origin),
			Body:     channelHealthEscalationBody(origin, ch, kind),
			Metadata: meta,
			DedupKey: dedupKey + "|undeliverable",
		})
		if localErr != nil {
			slog.Warn("record undeliverable channel health escalation failed", "session", origin, "kind", kind, "error", localErr)
			return false
		}
		return localID != ""
	}
	if wakeErr != nil {
		slog.Warn("wake parent for channel health escalation failed", "session", origin, "kind", kind, "target", id, "error", wakeErr)
	}
	return true
}

func channelHealthEscalationBody(origin string, ch *contract.ChannelHealth, kind string) string {
	lines := []string{
		fmt.Sprintf("%s event channel %s is failing.", origin, kind),
	}
	if ch.LastChannel != "" {
		lines = append(lines, "", "channel: "+ch.LastChannel)
	}
	if ch.LastError != "" {
		lines = append(lines, "error: "+ch.LastError)
	}
	lines = append(lines, fmt.Sprintf("consecutive_failures: %d", ch.ConsecutiveFailures))
	if !ch.FirstFailureAt.IsZero() {
		lines = append(lines, "first_failure_at: "+ch.FirstFailureAt.UTC().Format(time.RFC3339))
	}
	return strings.Join(lines, "\n")
}

func persistChannelHealthEscalation(store *state.Store, name, kind string) {
	now := time.Now()
	if err := store.Update(name, func(s *domain.Session) error {
		var ch *contract.ChannelHealth
		switch kind {
		case contract.ChannelFailureKindValidation:
			ch = s.ChannelValidationHealth
		case contract.ChannelFailureKindDelivery:
			ch = s.ChannelDeliveryHealth
		}
		if ch == nil || ch.ConsecutiveFailures == 0 {
			// The streak already cleared (recovered) before this push landed;
			// there is nothing left to dedup against.
			return nil
		}
		ch.EscalatedAt = now
		s.UpdatedAt = now
		return nil
	}); err != nil {
		slog.Warn("persist channel health escalation failed", "session", name, "kind", kind, "error", err)
	}
}
