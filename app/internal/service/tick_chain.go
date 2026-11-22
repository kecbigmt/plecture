package service

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/eventlog"
	"github.com/kecbigmt/plecture/app/internal/state"
	"github.com/kecbigmt/plecture/contracts/event"
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

	meta := make(map[string]string, len(sp.Inputs)+4)
	for key := range sp.Inputs {
		meta[key] = chainInputString(sp.Inputs, key)
	}
	// The kick's own keys are written last so an input that happens to share
	// a name cannot redirect dedup or misattribute the kick; the body still
	// lists that input.
	meta["work_session"] = workSession
	meta["chain_id"] = sp.ChainID
	meta["instance"] = sp.Instance
	meta["chain_dedup_key"] = dedupKey
	_, err = EventPublish(cfg, store, sp.TargetSession, EventPublishParams{
		Type:      event.TypeUserEmit,
		Source:    event.SourceTick,
		Direction: event.Inbound,
		Summary:   fmt.Sprintf("Re-evaluate %s", workSession),
		Body:      chainKickBody(workSession, sp),
		Metadata:  meta,
	})
	if err != nil {
		return false, err
	}
	return true, nil
}

func chainKickDedupKey(workSession string, sp ChainSpawn) string {
	parts := []string{workSession, sp.TargetSession, sp.ChainID, sp.Instance}
	for _, key := range slices.Sorted(maps.Keys(sp.Inputs)) {
		parts = append(parts, key+"="+chainInputString(sp.Inputs, key))
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return fmt.Sprintf("%x", sum[:])
}

// A non-string input is rendered as JSON rather than with Go's default
// formatting, so an array or object reads the same as it was declared.
func chainInputString(inputs map[string]any, key string) string {
	switch v := inputs[key].(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(v)
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return fmt.Sprint(v)
		}
		return string(b)
	}
}

func chainKickBody(workSession string, sp ChainSpawn) string {
	lines := []string{
		fmt.Sprintf("Re-evaluate the work session `%s`.", workSession),
		"",
	}
	keys := slices.Sorted(maps.Keys(sp.Inputs))
	if len(keys) == 0 {
		lines = append(lines, "- (no inputs)")
	}
	for _, key := range keys {
		lines = append(lines, fmt.Sprintf("- %s: %s", key, chainInputString(sp.Inputs, key)))
	}
	lines = append(lines, "", "Record one `plect judge` action per pending judge id against the work session.")
	return strings.Join(lines, "\n")
}
