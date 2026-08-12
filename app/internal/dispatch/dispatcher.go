package dispatch

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/plecture/plect/app/internal/channel"
	"github.com/plecture/plect/app/internal/config"
	"github.com/plecture/plect/app/internal/domain"
	"github.com/plecture/plect/app/internal/eventlog"
	"github.com/plecture/plect/app/internal/sessionhub"
	"github.com/plecture/plect/app/internal/state"
	"github.com/plecture/plect/contracts/event"
	contract "github.com/plecture/plect/contracts/state"
)

// dispatcherConsumer is this consumer's cursor name. The cursor is durable per
// session, so it survives down/up; it assumes a single bus daemon (one
// dispatcher per session process-wide).
const dispatcherConsumer = "dispatcher"

// fallbackDrain re-drains even if a wake was missed/coalesced — correctness rests
// on the durable cursor, so this only bounds worst-case latency, not delivery.
const fallbackDrain = 5 * time.Second

// sessionDispatcher follows one session's event log and fans each event out to
// the matching workflow channels. Static config (channels/defs) is resolved
// once by the supervisor; session outputs are re-read per drain so a down/up
// that re-created the runtime (new socket path) is picked up.
type sessionDispatcher struct {
	session  string
	channels []config.EventChannel
	defs     map[string]config.ChannelDefinition
	log      *eventlog.Store
	state    *state.Store
	hub      *sessionhub.Registry
	policy   channel.RetryPolicy
	// envExecutor routes an exec channel's argv through the workflow's
	// environment when the channel declares execution = "environment"; nil
	// (host degeneration, or environment resolution failed) means every
	// channel delivers exactly as it did before this field existed.
	envExecutor channel.Executor
}

func (d *sessionDispatcher) run(ctx context.Context) {
	SeedCursor(d.log, d.session)
	startGen, _ := d.log.Gen(d.session)
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
		s := d.state.Get(d.session)
		if s == nil {
			return // destroyed
		}
		// Skip (don't exit) while run scope is down so a fast down/up resumes
		// without the supervisor and this goroutine desyncing; the supervisor
		// owns teardown.
		if hasRunScopeUp(s.Tasks) {
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
	if g, _ := d.log.Gen(d.session); *startGen != "" && g != *startGen {
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
		d.processEvent(ctx, s, ev)
		if ctx.Err() != nil {
			return // cancelled mid-event; leave the cursor so it replays on restart
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
func (d *sessionDispatcher) processEvent(ctx context.Context, s *domain.Session, ev event.Event) {
	// Never deliver a channel error — a channel with include="*" would otherwise
	// loop on its own failures. Structural, not just a config convention.
	if ev.Type == event.TypeChannelError {
		return
	}
	var wg sync.WaitGroup
	for _, ch := range d.channels {
		if !channelMatches(ch, ev) {
			continue
		}
		def, ok := d.defs[ch.Uses]
		if !ok {
			continue // resolution is validated at load; defensive
		}
		wg.Go(func() {
			inputs, err := channelInputs(s, ch)
			if err != nil {
				d.recordFailure(ctx, ev, ch.Name, 0, err)
				return
			}
			attempts, derr := channel.DeliverWithRetryAndExecutor(ctx, def, inputs, ev, d.policy, d.envExecutor)
			if derr != nil {
				d.recordFailure(ctx, ev, ch.Name, attempts, derr)
			}
		})
	}
	wg.Wait()
}

func channelMatches(ch config.EventChannel, ev event.Event) bool {
	for _, include := range ch.Include {
		if event.MatchType(include, ev.Type) {
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

func hasRunScopeUp(tasks map[string]*contract.TaskState) bool {
	for _, e := range tasks {
		if e != nil && e.Scope == contract.TaskScopeRun && e.Status == contract.TaskStatusProduced {
			return true
		}
	}
	return false
}
