package service

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/kecbigmt/sennit/app/internal/config"
	"github.com/kecbigmt/sennit/app/internal/domain"
	"github.com/kecbigmt/sennit/app/internal/eventlog"
	"github.com/kecbigmt/sennit/app/internal/state"
	"github.com/kecbigmt/sennit/app/internal/task"
	"github.com/kecbigmt/sennit/contracts/event"
	contract "github.com/kecbigmt/sennit/contracts/state"
)

// TickParams carries SkipRefresh, unlike CheckParams: check never refreshes,
// so only tick needs a way to suppress its (default-on) refresh.
type TickParams struct {
	SessionName string
	SkipRefresh bool
	Observer    task.Observer
}

// TickSession is the Goal Loop actuator (ADR amendment 2026-07-03, story
// PR-C/PR-D): it refreshes outputs (unless SkipRefresh), evaluates done_when
// for each produced task instance, and — unlike CheckSession — carries out
// the result. A round advances only when the observed facts actually changed
// since the last tick (checkActionForResult's fingerprint compare);
// satisfied/escalate push a terminal event to the parent exactly once per
// instance; review_required and kick publish same-session events that drive
// the reviewer/work session. Against that same refreshed fact set, it also
// fires [[chains]]: each chain whose `when` holds and whose wired outputs are
// present spawns its workflow (idempotent — an already-active target is
// reported, not re-spawned), exactly as the chaining wiki's timing section
// requires ("evaluation and firing are... done by sennit tick").
func TickSession(cfg *config.Config, store *state.Store, params TickParams) (*CheckResult, error) {
	resolvedName, computed, chainPlan, warnings, err := evaluateSessionActions(cfg, store, params.SessionName, !params.SkipRefresh)
	if err != nil {
		return nil, err
	}

	// Stamp unconditionally (even when no instance has a computed action): a
	// tick always resets the `stale_when` staleness clock the reactor tracks,
	// whether or not anything was found unsatisfied this round.
	if err := stampLastTick(store, resolvedName); err != nil {
		return nil, err
	}

	var actions []CheckAction
	for _, c := range computed {
		action := c.action
		// Publish before persisting the marker: a publish failure must leave
		// LastAction/fingerprint unadvanced so the next tick retries this same
		// action instead of silently skipping delivery.
		warnings, err := publishTickAction(cfg, store, resolvedName, c.instance, action, c.alreadySatisfied)
		if err != nil {
			return nil, err
		}
		action.Warnings = append(action.Warnings, warnings...)
		actions = append(actions, action)
		if err := persistTickAction(store, resolvedName, c.instance, action); err != nil {
			return nil, err
		}
	}

	chains := make([]ChainSpawn, 0, len(chainPlan))
	for _, sp := range chainPlan {
		if sp.Fired && !sp.AlreadyActive {
			up, err := Up(cfg, store, UpParams{
				Identifier:    sp.Resource,
				Tag:           sp.Tag,
				Workflow:      sp.Workflow,
				Inputs:        sp.Inputs,
				ParentSession: sp.ParentSession,
				Observer:      params.Observer,
			})
			// A spawn failure (e.g. a transient workspace/runtime error) must not
			// discard the done_when actions already published/persisted above,
			// nor the other chains' results — it is reported on this entry only,
			// so the next tick can retry the same (idempotent) fire.
			if err != nil {
				sp.Warnings = append(sp.Warnings, fmt.Sprintf("spawn failed: %v", err))
			} else {
				sp.Spawned = true
				sp.TargetSession = up.SessionName
			}
		} else if sp.Fired && sp.AlreadyActive {
			delivered, err := publishAlreadyActiveChainKick(cfg, store, resolvedName, sp)
			if err != nil {
				sp.Warnings = append(sp.Warnings, fmt.Sprintf("already-active kick failed: %v", err))
			} else if delivered {
				sp.KickDelivered = true
			} else {
				sp.KickDebounced = true
			}
		}
		chains = append(chains, sp)
	}

	return &CheckResult{Actions: actions, Chains: chains, Warnings: warnings}, nil
}

func publishAlreadyActiveChainKick(cfg *config.Config, store *state.Store, workSession string, sp ChainSpawn) (bool, error) {
	if sp.TargetSession == "" {
		return false, nil
	}
	dedupKey := chainKickDedupKey(workSession, sp)
	log := eventlog.NewStore(store.Dir())
	evs, _, _, err := log.List(sp.TargetSession, 0, event.Filter{
		Types:   []string{event.TypeUserEmit},
		Sources: []string{event.SourceTick},
	})
	if err != nil {
		return false, err
	}
	for _, ev := range evs {
		if ev.Metadata["chain_dedup_key"] == dedupKey {
			return false, nil
		}
	}

	meta := map[string]string{
		"work_session":    workSession,
		"chain_id":        sp.ChainID,
		"instance":        sp.Instance,
		"chain_dedup_key": dedupKey,
	}
	for _, key := range []string{"revision", "judge_ids", "pr_url"} {
		if v := chainInputString(sp.Inputs, key); v != "" {
			meta[key] = v
		}
	}
	_, err = EventPublish(cfg, store, sp.TargetSession, EventPublishParams{
		Type:      event.TypeUserEmit,
		Source:    event.SourceTick,
		Direction: event.Outbound,
		Summary:   fmt.Sprintf("Re-evaluate %s at revision %s", workSession, metaValue(meta, "revision", "current")),
		Body:      chainKickBody(workSession, sp, meta),
		Metadata:  meta,
	})
	if err != nil {
		return false, err
	}
	return true, nil
}

// publishAutoRevivalKicks delivers the automatic post-exhaustion re-evaluation
// kick (issue #5) to each stale judge leaf's recorded reviewer session. It is
// deduplicated per (work session, reviewer, instance, leaf, revision) by
// scanning the reviewer's own log for a prior kick carrying the same dedup
// key — belt-and-suspenders alongside the round-state dedup
// (DoneWhenState.LastAutoRevivalRevision) persistTickAction records.
//
// A delivery failure to any reviewer is a hard error, not a warning: unlike
// the chain kick path (whose failure only affects that one chain entry's
// report), a swallowed failure here would let persistTickAction stamp
// LastAutoRevivalRevision anyway — the revival's dedup marker for this
// revision — and the reviewer would then never be retried for a revision it
// never actually received. Returning an error instead leaves the marker
// unset (publishTickAction's caller skips persistTickAction on error, exactly
// like every other action in this switch), so the next tick retries; already-
// delivered reviewers are skipped via the per-reviewer dedup scan above, so a
// retry after a partial failure only re-attempts the ones that failed.
func publishAutoRevivalKicks(cfg *config.Config, store *state.Store, workSession, instance string, action CheckAction) ([]string, error) {
	log := eventlog.NewStore(store.Dir())
	for _, rr := range action.RevivalReviewers {
		if rr.Session == "" {
			continue
		}
		dedupKey := autoRevivalDedupKey(workSession, rr.Session, instance, rr.LeafID, action.RevivalRevision)
		evs, _, _, err := log.List(rr.Session, 0, event.Filter{
			Types:   []string{event.TypeUserEmit},
			Sources: []string{event.SourceTick},
		})
		if err != nil {
			return nil, err
		}
		alreadyDelivered := false
		for _, ev := range evs {
			if ev.Metadata["revival_dedup_key"] == dedupKey {
				alreadyDelivered = true
				break
			}
		}
		if alreadyDelivered {
			continue
		}
		meta := map[string]string{
			"work_session":      workSession,
			"instance":          instance,
			"judge_id":          rr.LeafID,
			"revision":          action.RevivalRevision,
			"revival_dedup_key": dedupKey,
		}
		if _, err := EventPublish(cfg, store, rr.Session, EventPublishParams{
			Type:      event.TypeUserEmit,
			Source:    event.SourceTick,
			Direction: event.Outbound,
			Summary:   fmt.Sprintf("Re-evaluate %s at revision %s", workSession, action.RevivalRevision),
			Body:      autoRevivalKickBody(workSession, instance, rr.LeafID, action.RevivalRevision),
			Metadata:  meta,
		}); err != nil {
			return nil, fmt.Errorf("auto revival kick to %s: %w", rr.Session, err)
		}
	}
	return nil, nil
}

func autoRevivalDedupKey(workSession, reviewerSession, instance, leafID, revision string) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{workSession, reviewerSession, instance, leafID, revision}, "\x00")))
	return fmt.Sprintf("%x", sum[:])
}

func autoRevivalKickBody(workSession, instance, leafID, revision string) string {
	return strings.Join([]string{
		fmt.Sprintf("Re-evaluate `%s` (instance `%s`) — a new revision landed after review rounds were exhausted.", workSession, instance),
		"",
		fmt.Sprintf("- Revision: %s", revision),
		fmt.Sprintf("- Judge leaf: %s", leafID),
		"",
		"Record `sennit judge` for this leaf against the work session once you've re-reviewed.",
	}, "\n")
}

func chainKickDedupKey(workSession string, sp ChainSpawn) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{
		workSession,
		sp.TargetSession,
		sp.ChainID,
		sp.Instance,
		chainInputString(sp.Inputs, "revision"),
		chainInputString(sp.Inputs, "judge_ids"),
	}, "\x00")))
	return fmt.Sprintf("%x", sum[:])
}

func chainInputString(inputs map[string]any, key string) string {
	if len(inputs) == 0 {
		return ""
	}
	v, ok := inputs[key]
	if !ok || v == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(v))
}

func metaValue(meta map[string]string, key, fallback string) string {
	if v := strings.TrimSpace(meta[key]); v != "" {
		return v
	}
	return fallback
}

func chainKickBody(workSession string, sp ChainSpawn, meta map[string]string) string {
	lines := []string{
		fmt.Sprintf("Re-evaluate the work session `%s`.", workSession),
		"",
		fmt.Sprintf("- PR: %s", metaValue(meta, "pr_url", sp.Resource)),
		fmt.Sprintf("- Revision: %s", metaValue(meta, "revision", "current")),
		fmt.Sprintf("- Pending judge ids: %s", metaValue(meta, "judge_ids", "(unspecified)")),
		"",
		"Record one `sennit judge` action per pending judge id against the work session.",
	}
	return strings.Join(lines, "\n")
}

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
		// D7/D8: escalate is also pushed one hop to the parent (goal-loop
		// Layer 1), on top of the same-session record above (kept for
		// sennit.tick.escalated compat/observability, ADR D11 slice 5).
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
// `stale_when` sweep reads (wiki verification-gate.md: "once tick runs, the
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
