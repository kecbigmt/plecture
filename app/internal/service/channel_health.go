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

// CheckChannelHealth escalates a session's open event-channel
// validation/delivery failure streak to a live ancestor exactly once per
// episode, once the streak crosses channelHealthFailureThreshold consecutive
// failures or has been open longer than channelHealthFailureAge. A later
// successful validation or delivery (internal/dispatch again) clears the
// streak, ending the episode; a subsequent failure starts a new one and is
// free to escalate again. Returns whether an escalation was pushed.
func CheckChannelHealth(cfg *config.Config, store *state.Store, sessionName string) (bool, error) {
	s, err := store.GetE(sessionName)
	if err != nil {
		return false, err
	}
	if s == nil {
		return false, &Error{Code: ErrSessionNotFound, Message: fmt.Sprintf("session %q not found", sessionName)}
	}
	ch := s.ChannelHealth
	if ch == nil || ch.ConsecutiveFailures == 0 || !ch.EscalatedAt.IsZero() {
		return false, nil
	}
	crossed := ch.ConsecutiveFailures >= channelHealthFailureThreshold ||
		(!ch.FirstFailureAt.IsZero() && time.Since(ch.FirstFailureAt) >= channelHealthFailureAge)
	if !crossed {
		return false, nil
	}
	if !pushChannelHealthEscalation(cfg, store, sessionName, ch) {
		return false, nil
	}
	persistChannelHealthEscalation(store, sessionName)
	return true, nil
}

func pushChannelHealthEscalation(cfg *config.Config, store *state.Store, origin string, ch *contract.ChannelHealth) bool {
	meta := map[string]string{
		"escalation_kind":      escalationKindChannelDeliveryFailed,
		"channel_failure_kind": ch.LastKind,
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
	// to distinguish.
	dedupKey := fmt.Sprintf("%s|channel|%s", origin, ch.FirstFailureAt.UTC().Format(time.RFC3339Nano))
	id, wakeErr, err := publishHealthEscalationToLiveAncestor(cfg, store, origin, TerminalParams{
		Type:     event.TypeTerminalEscalate,
		Summary:  fmt.Sprintf("%s event channel is failing", origin),
		Body:     channelHealthEscalationBody(origin, ch),
		Metadata: meta,
		DedupKey: dedupKey,
	})
	if err != nil {
		slog.Warn("publish channel health escalation failed", "session", origin, "error", err)
		return false
	}
	if id == "" {
		localID, _, localErr := publishTerminalTo(cfg, store, origin, origin, false, TerminalParams{
			Type:     event.TypeTerminalDead,
			Summary:  fmt.Sprintf("%s channel health escalation is undeliverable", origin),
			Body:     channelHealthEscalationBody(origin, ch),
			Metadata: meta,
			DedupKey: dedupKey + "|undeliverable",
		})
		if localErr != nil {
			slog.Warn("record undeliverable channel health escalation failed", "session", origin, "error", localErr)
			return false
		}
		return localID != ""
	}
	if wakeErr != nil {
		slog.Warn("wake parent for channel health escalation failed", "session", origin, "target", id, "error", wakeErr)
	}
	return true
}

func channelHealthEscalationBody(origin string, ch *contract.ChannelHealth) string {
	lines := []string{
		fmt.Sprintf("%s event channel %s is failing.", origin, ch.LastKind),
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

func persistChannelHealthEscalation(store *state.Store, name string) {
	now := time.Now()
	if err := store.Update(name, func(s *domain.Session) error {
		if s.ChannelHealth == nil || s.ChannelHealth.ConsecutiveFailures == 0 {
			// The streak already cleared (recovered) before this push landed;
			// there is nothing left to dedup against.
			return nil
		}
		s.ChannelHealth.EscalatedAt = now
		s.UpdatedAt = now
		return nil
	}); err != nil {
		slog.Warn("persist channel health escalation failed", "session", name, "error", err)
	}
}
