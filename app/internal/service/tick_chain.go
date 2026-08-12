package service

import (
	"crypto/sha256"
	"fmt"
	"strings"

	"github.com/cradel-dev/cradel/app/internal/config"
	"github.com/cradel-dev/cradel/app/internal/eventlog"
	"github.com/cradel-dev/cradel/app/internal/state"
	"github.com/cradel-dev/cradel/contracts/event"
)

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
// kick to each stale judge leaf's recorded reviewer session. It is
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
