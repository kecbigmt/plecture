package service

import (
	"fmt"
	"strings"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/domain"
	"github.com/kecbigmt/plecture/app/internal/eventlog"
	"github.com/kecbigmt/plecture/app/internal/state"
	"github.com/kecbigmt/plecture/contracts/event"
)

// terminalScanLimit bounds the reverse scan a dedup check uses against the
// target's recent terminal events (P1: event id dedup so a repeated
// goal-loop tick does not re-push the same fact). A session with
// more than this many terminal pushes since the one being deduped would
// re-push — acceptable, since D1/P1 only require idempotent *handling* of a
// duplicate, not that one can never occur.
const terminalScanLimit = 200

// TerminalParams describes a single-hop terminal push (ADR: cross-session
// terminal event propagation, D1-D3).
type TerminalParams struct {
	Type    string // event.TypeTerminalDone | TypeTerminalEscalate | TypeTerminalDead
	Summary string
	Body    string
	// Metadata carries extra fields (e.g. the task instance). MetaOriginSession,
	// MetaRelation, and MetaDedupKey (when DedupKey is set) are stamped by the
	// publisher and must not be set here.
	Metadata map[string]string
	// DedupKey is the idempotency key; a push carrying the same (Type, DedupKey)
	// already recorded on the target within the scan window is skipped.
	DedupKey string
}

// resolveTerminalTarget strips a "root:" pseudo-parent prefix
// (domain.ImplicitRootParent) down to the addressable session it names, so
// every terminal-push path — PublishTerminalToParent (done/escalate) and
// pushDeadReport's ancestor walk (dead) — resolves a literal ParentSession
// value to the same delivery target instead of each re-implementing the
// strip. A plain (non-root:) parent passes through unchanged.
func resolveTerminalTarget(parent string) string {
	if rootTarget, ok := strings.CutPrefix(parent, "root:"); ok {
		return rootTarget
	}
	return parent
}

// PublishTerminalToParent pushes a done/escalate/dead terminal event from
// origin to origin's immediate parent (D1: single-hop; D2/D3: the parent
// decides whether to handle it or forward further). Returns ("", nil, nil)
// when origin has no parent — a root session has no immediate parent to push
// to, which D1 leaves undefined rather than treating as an error. wakeErr
// reports a failed sleep wake request after the push was recorded.
func PublishTerminalToParent(cfg *config.Config, store *state.Store, origin string, p TerminalParams) (id string, wakeErr error, err error) {
	s, err := store.GetE(origin)
	if err != nil {
		return "", nil, err
	}
	if s == nil {
		return "", nil, &Error{Code: ErrSessionNotFound, Message: fmt.Sprintf("session %q not found", origin)}
	}
	if s.ParentSession == "" {
		return "", nil, nil
	}
	target := resolveTerminalTarget(s.ParentSession)
	return publishTerminalTo(cfg, store, origin, target, true, p)
}

// publishTerminalTo appends a plect.terminal.* event into target's own log
// (D1: push routing writes into the *receiving* session's partition,
// not the origin's, so the receiver's own dispatcher — which only ever reads
// its own log — delivers it once the receiver's workflow channels include
// the plect.terminal.* type). Metadata carries the origin session and its
// relation to target so readers don't need a second tree lookup.
//
// A down target requests a wake only when it is sleeping. A deliberate full
// stop must retain the push without allowing a child to restart its parent.
// A self-targeted push also cannot request a wake: its own bad news must not
// reverse the session's lifecycle decision.
func publishTerminalTo(cfg *config.Config, store *state.Store, origin, target string, wakeIfDown bool, p TerminalParams) (id string, wakeErr error, err error) {
	if target == origin {
		wakeIfDown = false
	}
	if p.DedupKey != "" {
		dup, derr := hasRecentTerminalEvent(store, target, p.Type, p.DedupKey)
		if derr != nil {
			return "", nil, &Error{Code: ErrExecutionFailed, Message: derr.Error()}
		}
		if dup {
			return "", nil, nil
		}
	}
	meta := make(map[string]string, len(p.Metadata)+3)
	for k, v := range p.Metadata {
		meta[k] = v
	}
	meta[event.MetaOriginSession] = origin
	sessions, err := store.AllE()
	if err != nil {
		return "", nil, err
	}
	meta[event.MetaRelation] = string(domain.RelationFromTarget(sessions, target, origin))
	if p.DedupKey != "" {
		meta[event.MetaDedupKey] = p.DedupKey
	}
	summary := p.Summary
	// dead's summary already names origin inline (pushDeadReport); done/escalate
	// summaries are generic done_when text (checkActionForResult) that reads as
	// the target's own event once session_name is rewritten to target.
	if p.Type != event.TypeTerminalDead {
		summary = fmt.Sprintf("%s (from %s)", summary, origin)
	}
	stored, _, _, aerr := eventlog.NewStore(store.Dir()).Append(event.Event{
		SessionName: target,
		Type:        p.Type,
		Source:      event.SourcePlect,
		Direction:   event.Inbound,
		Summary:     summary,
		Body:        p.Body,
		Metadata:    meta,
	})
	if aerr != nil {
		return "", nil, &Error{Code: ErrExecutionFailed, Message: aerr.Error()}
	}
	if wakeIfDown {
		ts, err := store.GetE(target)
		if err != nil {
			return "", nil, err
		}
		if ts != nil && !cfg.RunScopeUp(ts) && !ts.SleepSince.IsZero() {
			if _, requestErr := RequestWake(store, target, stored.Time); requestErr != nil {
				wakeErr = fmt.Errorf("request wake %q: %w", target, requestErr)
			}
		}
	}
	return stored.ID, wakeErr, nil
}

func hasRecentTerminalEvent(store *state.Store, session, typ, dedupKey string) (bool, error) {
	evs, err := eventlog.NewStore(store.Dir()).Tail(session, event.Filter{Types: []string{typ}}, terminalScanLimit)
	if err != nil {
		return false, err
	}
	for _, ev := range evs {
		if ev.Metadata[event.MetaDedupKey] == dedupKey {
			return true, nil
		}
	}
	return false, nil
}
