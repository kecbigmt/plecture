package service

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/kecbigmt/plecture/app/internal/domain"
	"github.com/kecbigmt/plecture/app/internal/state"
	"github.com/kecbigmt/plecture/contracts/event"
	contract "github.com/kecbigmt/plecture/contracts/state"

	"github.com/kecbigmt/plecture/app/internal/config"
)

// publishTickAction delivers the side effect for a computed action (terminal
// push and/or same-session event) before its marker is persisted, so a
// delivery failure here leaves the caller free to retry on the next tick
// rather than recording an action whose delivery never actually happened. A
// non-nil error means the delivery itself failed. A terminal push's target
// wake can fail independently of the push (the event is already recorded by
// then), so that failure is reported as a warning rather than an error.
func publishTickAction(cfg *config.Config, store *state.Store, sessionName, instance string, action CheckAction, alreadySatisfied bool) ([]string, error) {
	switch action.Action {
	case "satisfied":
		if alreadySatisfied {
			return nil, nil
		}
		_, wakeErr, err := PublishTerminalToParent(cfg, store, sessionName, TerminalParams{
			Type:     event.TypeTerminalDone,
			Summary:  action.Summary,
			Metadata: map[string]string{event.MetaInstance: instance},
			DedupKey: instance + "|done|" + action.Fingerprint,
		})
		if err != nil {
			return nil, err
		}
		return wakeWarnings(wakeErr), nil
	case "review_required":
		if _, err := EventPublish(cfg, store, sessionName, EventPublishParams{
			Type:      event.TypeTickReviewRequired,
			Direction: event.Internal,
			Source:    event.SourceTick,
			Summary:   action.Summary,
			Body:      action.Body,
			Metadata:  unmetItemsMetadata(instance, action.UnmetItems),
		}); err != nil {
			return nil, err
		}
		if action.RevivalRevision != "" {
			return publishAutoRevivalKicks(cfg, store, sessionName, instance, action)
		}
	case "kick":
		if _, err := EventPublish(cfg, store, sessionName, EventPublishParams{
			Type:      event.TypeUserEmit,
			Direction: event.Outbound,
			Source:    event.SourceTick,
			Summary:   action.Summary,
			Body:      action.Body,
			Metadata:  unmetItemsMetadata(instance, action.UnmetItems),
		}); err != nil {
			return nil, err
		}
	case "escalate":
		if _, err := EventPublish(cfg, store, sessionName, EventPublishParams{
			Type:      event.TypeTickEscalated,
			Direction: event.Internal,
			Source:    event.SourceTick,
			Summary:   action.Summary,
			Body:      action.Body,
			Metadata:  unmetItemsMetadata(instance, action.UnmetItems),
		}); err != nil {
			return nil, err
		}
		// Escalate is also pushed one hop to the parent (the goal loop's
		// actuator layer), on top of the same-session record above (kept
		// for plecture.tick.escalated compatibility and observability).
		_, wakeErr, err := PublishTerminalToParent(cfg, store, sessionName, TerminalParams{
			Type:     event.TypeTerminalEscalate,
			Summary:  action.Summary,
			Body:     action.Body,
			Metadata: map[string]string{event.MetaInstance: instance},
			DedupKey: instance + "|escalate|" + action.Fingerprint,
		})
		if err != nil {
			return nil, err
		}
		return wakeWarnings(wakeErr), nil
	}
	return nil, nil
}

// unmetItemsMetadata carries a kick/review_required/escalate event's unmet
// items as a JSON companion to its prose Body: the structured CheckUnmetItem
// list (kind/output/value/pending_reason/...) already computed for CheckAction
// otherwise never reached the delivered event, leaving a receiving agent only
// the flattened text to parse. Absent when there are no unmet items (already
// carries no information) or on a marshal failure (never expected — all
// fields are plain strings/bools).
func unmetItemsMetadata(instance string, items []CheckUnmetItem) map[string]string {
	meta := map[string]string{"instance": instance}
	if len(items) == 0 {
		return meta
	}
	if b, err := json.Marshal(items); err == nil {
		meta["unmet_items"] = string(b)
	}
	return meta
}

// wakeWarnings turns a non-fatal terminal-push wake failure into a
// human-readable tick warning (nil when there is nothing to report).
func wakeWarnings(wakeErr error) []string {
	if wakeErr == nil {
		return nil
	}
	return []string{fmt.Sprintf("terminal push recorded but parent wake failed: %v", wakeErr)}
}

// stampLastTick records the session-level tick watermark the reactor's
// `heartbeat` sweep reads (wiki verification-gate.md: "once tick runs, the
// baseline is reset"). It is session-scoped, not per-instance, because tick
// evaluates every produced instance of a session in one pass.
func stampLastTick(store *state.Store, sessionName string) error {
	now := time.Now()
	return store.Update(sessionName, func(s *domain.Session) error {
		s.LastTickAt = now
		s.UpdatedAt = now
		return nil
	})
}

func persistTickAction(store *state.Store, sessionName, instance string, action CheckAction) error {
	now := time.Now()
	return store.Update(sessionName, func(s *domain.Session) error {
		st := s.Tasks[instance]
		if st == nil {
			return fmt.Errorf("instance %q not found in session %s", instance, sessionName)
		}
		if st.DoneWhen == nil {
			st.DoneWhen = &contract.DoneWhenState{}
		}
		st.DoneWhen.LastAction = action.Action
		st.DoneWhen.LastFingerprint = action.Fingerprint
		st.DoneWhen.LastReason = action.Summary
		st.DoneWhen.LastUnsatisfied = append([]string(nil), action.Items...)
		st.DoneWhen.LastBody = action.Body
		if action.RevivalRevision != "" {
			// Revival explicitly resets the budget (checkActionForResult
			// computed Round off a zeroed round counter), so it must win even
			// though Round is now numerically below the exhausted Rounds this
			// overwrites. Stamping the revision here is the dedup record: the
			// same revision can never revive the budget (or kick a reviewer)
			// again.
			st.DoneWhen.Rounds = action.Round
			st.DoneWhen.LastAutoRevivalRevision = action.RevivalRevision
		} else if action.Round > st.DoneWhen.Rounds {
			st.DoneWhen.Rounds = action.Round
		}
		if action.Action == "escalate" {
			st.DoneWhen.EscalatedAt = now
			st.DoneWhen.EscalateReason = action.Body
		}
		s.UpdatedAt = now
		return nil
	})
}
