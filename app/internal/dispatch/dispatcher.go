package dispatch

import (
	"context"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/kecbigmt/plecture/app/internal/channel"
	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/domain"
	"github.com/kecbigmt/plecture/app/internal/eventlog"
	"github.com/kecbigmt/plecture/app/internal/plugins"
	"github.com/kecbigmt/plecture/app/internal/sessionhub"
	"github.com/kecbigmt/plecture/app/internal/state"
	"github.com/kecbigmt/plecture/app/internal/task"
	"github.com/kecbigmt/plecture/contracts/event"
)

// dispatcherConsumer is this consumer's cursor kind. The cursor is durable per
// session, so it survives down/up; it assumes a single resident process (one
// dispatcher per session process-wide).
const dispatcherConsumer = "delivery"

// fallbackDrain re-drains even if a wake was missed/coalesced — correctness rests
// on the durable cursor, so this only bounds worst-case latency, not delivery.
const fallbackDrain = 5 * time.Second

// repairHoldLimit is measured from the event's own append time: a repair that
// never converges would otherwise stall every later event behind the held one.
const repairHoldLimit = 30 * time.Minute

// sessionDispatcher follows one session's event log and fans each event out to
// the matching workflow channels. Static config (channels/defs) is resolved
// once by the supervisor; session outputs are re-read per drain so a down/up
// that re-created the runtime (new socket path) is picked up.
type sessionDispatcher struct {
	session string
	// cfg is the resolved config as of dispatcher build, matching channels/
	// defs below: used only for the run-scope gate, which does not need to
	// react to a config change mid-drain the way the reactor's does.
	cfg      *config.Config
	channels []config.EventChannel
	defs     map[string]config.ChannelDefinition
	log      *eventlog.Store
	state    *state.Store
	hub      *sessionhub.Registry
	policy   channel.RetryPolicy
	// plugins feeds a `bin` reference inside a terminal verb, mirroring
	// task.SessionVars.Plugins.
	plugins []plugins.Mounted
	// terminal is the workflow's terminal-declaring effect, resolved once at
	// dispatcher build (see resolveTerminalOwner). Nil means the workflow
	// declares none; a channel that consumes no terminal verb is unaffected
	// either way.
	terminal *terminalOwner
	// awaitingRepair is injected because the judgment lives in internal/service,
	// which imports this package. Nil means a failed delivery is never held.
	awaitingRepair func(session string) (bool, error)
	// held is memory only: a restart redelivers once, which at-least-once
	// delivery allows, and it keeps a retry from re-sending to channels that
	// already delivered.
	held heldEvent
}

type heldEvent struct {
	id        string
	delivered map[string]bool
}

func (d *sessionDispatcher) run(ctx context.Context) {
	SeedCursor(d.log, d.session)
	startGen, _ := d.log.StreamID(d.session)
	// Re-drain on a wake from the shared per-session reader instead of polling on
	// our own timer, so the session keeps a single follow loop. The fallback
	// ticker re-drains defensively if a wake was ever missed.
	wake := d.hub.Watch(d.session)
	defer wake.Close()
	fallback := time.NewTicker(fallbackDrain)
	defer fallback.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		s, err := d.state.GetE(d.session)
		if err != nil {
			// An unreadable store must never read as "destroyed": that would
			// permanently exit this loop (nothing restarts it) instead of
			// retrying once the store recovers.
			slog.Default().Error("dispatcher: read session state failed", "session", d.session, "error", err)
		} else if s == nil {
			return // destroyed
		} else if d.cfg.RunScopeUp(s) {
			// Skip (don't exit) while run scope is down so a fast down/up
			// resumes without the supervisor and this goroutine desyncing;
			// the supervisor owns teardown.
			d.drain(ctx, s, &startGen)
		}
		select {
		case <-ctx.Done():
			return
		case <-wake.Wake():
		case <-fallback.C:
		}
	}
}

// SeedCursor commits the dispatcher's durable read cursor at the session log's
// current tail, but only if no cursor exists yet (idempotent). When it runs
// decides what gets delivered:
//
//   - At session birth (service.Create, before the initial task instruction is
//     appended), the tail is empty, so every event produced afterwards —
//     including that first plect.instruction — is delivered once the dispatcher
//     starts. Without this, the instruction is appended during create but the
//     dispatcher only starts ~1s later when the run scope comes up, and its own
//     first-start seed below would land past the instruction and drop it.
//   - On a dispatcher's first start for a session born before this mechanism
//     existed (migration), no birth cursor was seeded, so it lands at the live
//     tail and the old history isn't re-flooded to the runtime.
//
// A resumed dispatcher already has a cursor and keeps it (events appended while
// down are still delivered).
func SeedCursor(log *eventlog.Store, session string) {
	if log.HasCursor(session, dispatcherConsumer) {
		return
	}
	_, _, end, err := log.List(session, 0, event.Filter{})
	if err != nil {
		slog.Default().Warn("dispatcher: seed cursor: list failed; cursor left unseeded, next start will retry", "session", session, "error", err)
		return
	}
	if err := log.CommitCursor(session, dispatcherConsumer, end); err != nil {
		slog.Default().Warn("dispatcher: seed cursor: commit failed; cursor left unseeded, next start will retry", "session", session, "error", err)
	}
}

// drain delivers every event past the committed cursor, advancing the cursor one
// event at a time so the at-least-once replay window after a crash is a single
// event, not a batch.
func (d *sessionDispatcher) drain(ctx context.Context, s *domain.Session, startGen *string) {
	if g, _ := d.log.StreamID(d.session); *startGen != "" && g != *startGen {
		// Log rotated/compacted: the byte cursor is meaningless, re-read from head.
		if err := d.log.CommitCursor(d.session, dispatcherConsumer, 0); err != nil {
			slog.Default().Warn("dispatcher: reset cursor after log rotation failed", "session", d.session, "error", err)
		}
		*startGen = g
	}
	cur, err := d.log.ReadCursor(d.session, dispatcherConsumer)
	if err != nil {
		slog.Default().Warn("dispatcher: read cursor failed; skipping this drain, will retry on next wake", "session", d.session, "error", err)
		return
	}
	evs, offs, next, err := d.log.List(d.session, cur, event.Filter{})
	if err != nil {
		slog.Default().Warn("dispatcher: list events failed; skipping this drain, will retry on next wake", "session", d.session, "error", err)
		return
	}
	for i, ev := range evs {
		if ctx.Err() != nil {
			return
		}
		held := d.processEvent(ctx, s, ev)
		if ctx.Err() != nil {
			return // cancelled mid-event; leave the cursor so it replays on restart
		}
		if held {
			return // head-of-line: the next wake or fallback drain retries this event
		}
		commit := next
		if i+1 < len(offs) {
			commit = offs[i+1]
		}
		// A commit failure here means the event was already delivered above but the
		// cursor didn't advance past it — logged so it's observable, and left for the
		// next drain to redeliver (at-least-once) rather than silently losing track.
		if err := d.log.CommitCursor(d.session, dispatcherConsumer, commit); err != nil {
			slog.Default().Warn("dispatcher: commit cursor failed; event may redeliver on next drain", "session", d.session, "offset", commit, "error", err)
		}
	}
}

// processEvent runs one worker per matching channel and returns once all are
// terminal, so the cursor only advances after the event is fully processed.
// It returns true when a failed delivery is held instead of recorded, in which
// case the caller must leave the cursor in place.
// The delivery streak (Session.ChannelDeliveryHealth) is updated once per
// event, from the workers' combined outcome, rather than once per channel:
// a multi-channel workflow with one permanently broken channel and one
// healthy one would otherwise have concurrent per-channel writes to the same
// session-level streak race each other, and the healthy channel's steady
// stream of successes would starve the broken one's streak of ever reaching
// the escalation threshold.
func (d *sessionDispatcher) processEvent(ctx context.Context, s *domain.Session, ev event.Event) (held bool) {
	if ev.Type == event.TypeChannelError && ev.Metadata["event_type"] == event.TypeChannelError {
		return false
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	matched := false
	failures := map[int]deliveryFailure{}
	delivered := map[string]bool{}
	if d.held.id == ev.ID {
		for name := range d.held.delivered {
			delivered[name] = true
		}
	}
	for i, ch := range d.channels {
		if !channelMatches(ch, ev) {
			continue
		}
		if ev.Type == event.TypeChannelError && ev.Metadata["channel"] == ch.Name {
			continue
		}
		def, ok := d.defs[ch.Uses]
		if !ok {
			continue // resolution is validated at load; defensive
		}
		matched = true // set on the main goroutine, before any worker starts — no lock needed
		if delivered[ch.Name] {
			continue
		}
		wg.Go(func() {
			inputs, err := channelInputs(s, ch, def)
			if err != nil {
				mu.Lock()
				failures[i] = deliveryFailure{channel: ch.Name, cause: err}
				mu.Unlock()
				return
			}
			bins := config.MountedBins{Mounted: d.plugins, SourcePath: def.SourcePath}
			opts := channel.DeliverOptions{
				Bin: func(ref string) (string, error) { return bins.ResolveBin(ref, def.Ownership()) },
				Terminal: terminalResolver(s, d.terminal, task.SessionVars{
					Name:             s.Name,
					ResourceID:       s.ResourceID,
					WorkspaceDirPath: s.WorkspaceDirPath,
					Branch:           domain.SessionBranch(s),
					Inputs:           s.Inputs,
					Plugins:          d.plugins,
				}),
			}
			attempts, derr := channel.DeliverWithRetryAndOptions(ctx, def, inputs, ev, d.policy, opts)
			mu.Lock()
			defer mu.Unlock()
			if derr != nil {
				failures[i] = deliveryFailure{channel: ch.Name, attempts: attempts, cause: derr, attempted: true}
				return
			}
			delivered[ch.Name] = true
		})
	}
	wg.Wait()
	if ctx.Err() != nil {
		return false // shutdown mid-event, not a delivery outcome either way
	}
	if len(failures) == 0 {
		d.held = heldEvent{}
		if matched {
			recordDeliverySuccess(d.state, d.session)
		}
		return false
	}
	if d.shouldHold(ev, failures) {
		if d.held.id != ev.ID {
			slog.Default().Info("dispatcher: holding event until automatic repair finishes", "session", d.session, "event_id", ev.ID)
		}
		d.held = heldEvent{id: ev.ID, delivered: delivered}
		return true
	}
	d.held = heldEvent{}
	ordered := make([]int, 0, len(failures))
	for i := range failures {
		ordered = append(ordered, i)
	}
	slices.Sort(ordered)
	for _, i := range ordered {
		f := failures[i]
		d.recordFailure(ctx, ev, f.channel, f.attempts, f.cause)
	}
	first := failures[ordered[0]]
	recordDeliveryFailure(d.state, d.session, first.channel, first.cause, time.Now())
	return false
}

type deliveryFailure struct {
	channel   string
	attempts  int
	cause     error
	attempted bool
}

// shouldHold never holds a failure before delivery (an unresolvable input):
// it says nothing about liveness, so repair cannot fix it.
func (d *sessionDispatcher) shouldHold(ev event.Event, failures map[int]deliveryFailure) bool {
	if d.awaitingRepair == nil {
		return false
	}
	for _, f := range failures {
		if !f.attempted {
			return false
		}
	}
	if time.Since(ev.Time) > repairHoldLimit {
		return false
	}
	awaiting, err := d.awaitingRepair(d.session)
	if err != nil {
		slog.Default().Warn("dispatcher: read repair state failed; recording the delivery failure instead of holding", "session", d.session, "error", err)
		return false
	}
	return awaiting
}

func channelMatches(ch config.EventChannel, ev event.Event) bool {
	for _, include := range ch.Include {
		if event.MatchType(include, ev.Type) {
			return true
		}
		if ev.Type == event.TypeSessionResumed && event.MatchType(include, event.TypeInstruction) {
			return true
		}
	}
	return false
}

// recordFailure appends a plect.channel.error, but never for a ctx cancellation —
// a shutdown/suspend is not a delivery failure, and the event replays from the
// uncommitted cursor on restart.
func (d *sessionDispatcher) recordFailure(ctx context.Context, ev event.Event, channelName string, attempts int, cause error) {
	if ctx.Err() != nil {
		return
	}
	_, _, _, _ = d.log.Append(channel.ChannelErrorEvent(ev, channelName, attempts, cause))
}
