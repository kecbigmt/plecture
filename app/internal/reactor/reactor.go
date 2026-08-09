package reactor

import (
	"context"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/kecbigmt/plect/app/internal/config"
	"github.com/kecbigmt/plect/app/internal/domain"
	"github.com/kecbigmt/plect/app/internal/eventlog"
	"github.com/kecbigmt/plect/app/internal/service"
	"github.com/kecbigmt/plect/app/internal/sessionhub"
	"github.com/kecbigmt/plect/app/internal/state"
	"github.com/kecbigmt/plect/app/internal/task"
	"github.com/kecbigmt/plect/contracts/event"
	contract "github.com/kecbigmt/plect/contracts/state"
)

// reactorConsumer is this consumer's durable cursor name — distinct from
// dispatch's "dispatcher" cursor so the two followers advance independently
// over the same log.
const reactorConsumer = "tick-reactor"

// fallbackDrain re-drains even if a wake was missed/coalesced, mirroring
// dispatch's fallback ticker: correctness rests on the durable cursor, so
// this only bounds worst-case latency, not delivery.
const fallbackDrain = 5 * time.Second

// staleCheckInterval is the cadence of the periodic `stale_when` sweep. It is
// deliberately coarse relative to typical `stale_when` values (minutes), and
// checked once immediately at startup so a bus restart sweeps any
// already-overdue session promptly rather than waiting a full interval
// (ADR amendment 2026-07-04 §2/§3: "on recovery, whatever exceeds the
// freshness ceiling gets swept").
const staleCheckInterval = time.Minute

// sessionReactor follows one session's event log and ticks it when a
// declared event pattern matches, the judge builtin fires, or `stale_when`
// has elapsed since the session's last tick. Static config (tick) is
// resolved once by the supervisor, matching dispatch's sessionDispatcher.
type sessionReactor struct {
	session  string
	cfg      *config.Config
	state    *state.Store
	log      *eventlog.Store
	hub      *sessionhub.Registry
	tick     config.TickConfig
	observer task.Observer
	logger   *slog.Logger
	// tickFn defaults to service.TickSession; overridable in tests to observe
	// invocation count/concurrency (AC6) without weakening production
	// behavior — buildReactor never sets it.
	tickFn func(*config.Config, *state.Store, service.TickParams) (*service.CheckResult, error)
	// staleCheckEvery defaults to staleCheckInterval; overridable in tests so
	// AC4's stale-sweep cases don't need to wait a full minute of wall clock.
	staleCheckEvery time.Duration
}

func (r *sessionReactor) run(ctx context.Context) {
	seedCursor(r.log, r.session)
	startGen, _ := r.log.Gen(r.session)
	wake := r.hub.Watch(r.session)
	defer wake.Close()
	fallback := time.NewTicker(fallbackDrain)
	defer fallback.Stop()
	interval := r.staleCheckEvery
	if interval <= 0 {
		interval = staleCheckInterval
	}
	staleCheck := time.NewTicker(interval)
	defer staleCheck.Stop()

	// Immediate check, not just on the first tick of staleCheck: a bus that
	// was down past `stale_when` must sweep the backlog on restart, not wait
	// up to another full interval.
	r.checkStale(ctx)
	for {
		if ctx.Err() != nil {
			return
		}
		s := r.state.Get(r.session)
		if s == nil {
			return // destroyed
		}
		if hasRunScopeUp(s.Tasks) {
			r.drain(ctx, &startGen)
		}
		select {
		case <-ctx.Done():
			return
		case <-wake.Wake():
		case <-fallback.C:
		case <-staleCheck.C:
			r.checkStale(ctx)
		}
	}
}

// seedCursor commits the reactor's durable read cursor at the session log's
// current tail, but only if no cursor exists yet — mirroring
// dispatch.SeedCursor exactly (same idempotency and birth-event rationale),
// duplicated rather than shared because the two consumers must stay free to
// evolve independently.
func seedCursor(log *eventlog.Store, session string) {
	if log.HasCursor(session, reactorConsumer) {
		return
	}
	_, _, end, err := log.List(session, 0, event.Filter{})
	if err != nil {
		slog.Default().Warn("reactor: seed cursor: list failed; cursor left unseeded, next start will retry", "session", session, "error", err)
		return
	}
	if err := log.CommitCursor(session, reactorConsumer, end); err != nil {
		slog.Default().Warn("reactor: seed cursor: commit failed; cursor left unseeded, next start will retry", "session", session, "error", err)
	}
}

// drain reads every event past the committed cursor and ticks at most once
// for the whole batch — same-session ticks stay serialized (one goroutine
// owns this session's follow loop) and rapid bursts coalesce into one tick,
// exactly as verification-gate.md's serialization/debounce rule requires.
func (r *sessionReactor) drain(ctx context.Context, startGen *string) {
	if g, _ := r.log.Gen(r.session); *startGen != "" && g != *startGen {
		// Log rotated/compacted: the byte cursor is meaningless, re-read from head.
		if err := r.log.CommitCursor(r.session, reactorConsumer, 0); err != nil {
			slog.Default().Warn("reactor: reset cursor after log rotation failed", "session", r.session, "error", err)
		}
		*startGen = g
	}
	cur, err := r.log.ReadCursor(r.session, reactorConsumer)
	if err != nil {
		slog.Default().Warn("reactor: read cursor failed; skipping this drain, will retry on next wake", "session", r.session, "error", err)
		return
	}
	evs, _, next, err := r.log.List(r.session, cur, event.Filter{})
	if err != nil {
		slog.Default().Warn("reactor: list events failed; skipping this drain, will retry on next wake", "session", r.session, "error", err)
		return
	}
	if len(evs) == 0 {
		return
	}
	triggered := false
	for _, ev := range evs {
		if r.shouldTrigger(ev) {
			triggered = true
		}
	}
	// Advance the cursor unconditionally so a non-matching event is never
	// re-scanned; this consumer never fans out per-event side effects, so
	// unlike dispatch there is no per-event replay window to preserve.
	if err := r.log.CommitCursor(r.session, reactorConsumer, next); err != nil {
		slog.Default().Warn("reactor: commit cursor failed; batch may re-scan on next drain", "session", r.session, "error", err)
	}
	if triggered {
		r.doTick(ctx)
	}
}

// shouldTrigger decides whether ev should cause a tick. The judge builtin is
// checked first because it is declaration-independent (AC2). The
// self-emitted exclusion is checked next and wins over any declared `on`
// pattern — a workflow that declares an overly broad pattern (e.g. "*") must
// not make tick re-trigger on its own output (AC5, ADR amendment 2026-07-04
// §3: "the self-excitation-prevention whitelist overrides the declaration").
// Only events that clear both
// checks are matched against the declared patterns (AC1/AC3).
func (r *sessionReactor) shouldTrigger(ev event.Event) bool {
	if ev.Type == event.TypeJudgeRecorded {
		return true
	}
	if isSelfEmitted(ev) {
		return false
	}
	for _, pattern := range r.tick.On {
		if event.MatchType(pattern, ev.Type) {
			return true
		}
	}
	return false
}

// isSelfEmitted reports whether ev is something tick's own actuation
// produces: a terminal push, a lifecycle event accompanying a chain-spawned
// session's own birth, or one of tick's same-session progress markers
// (review_required, escalated, and — critically — kick's user.emit). The
// last one is why this excludes by Source rather than by enumerating types:
// kick's user.emit is otherwise indistinguishable in Type from a genuine
// external user.emit, so a workflow declaring a broad pattern (e.g. "*" or
// "user.*") would otherwise retrigger tick on its own kick output. These are
// excluded from pattern matching regardless of what a workflow declares in
// `[tick].on`.
func isSelfEmitted(ev event.Event) bool {
	if ev.Source == event.SourceTick {
		return true
	}
	switch ev.Type {
	case event.TypeTerminalDone, event.TypeTerminalEscalate, event.TypeTerminalDead:
		return true
	}
	return strings.HasPrefix(ev.Type, event.TypeLifecyclePrefix)
}

// defaultMaxStaleWhen caps the quiet-tick backoff interval when a workflow's
// `[tick]` declares no `max_stale_when`: standing goals must
// keep observing at some ceiling even after many quiet sweeps, so backoff
// never fully suppresses ticking.
const defaultMaxStaleWhen = 8 * time.Hour

// checkStale ticks the session when `stale_when` (scaled by the quiet-tick
// backoff) has elapsed since LastTickAt. A zero LastTickAt (never ticked)
// counts as infinitely stale, so a freshly produced instance with
// `stale_when` gets its first observation on the next sweep rather than
// waiting a full period.
func (r *sessionReactor) checkStale(ctx context.Context) {
	if r.tick.StaleWhen.Duration <= 0 {
		return
	}
	s := r.state.Get(r.session)
	if s == nil || !hasRunScopeUp(s.Tasks) {
		return
	}
	if !s.LastTickAt.IsZero() {
		n := 0
		if s.TickBackoff != nil {
			n = s.TickBackoff.ConsecutiveUnchanged
		}
		if time.Since(s.LastTickAt) < backoffInterval(r.tick.StaleWhen.Duration, r.maxStaleWhen(), n) {
			return
		}
	}
	r.doTick(ctx)
}

// maxStaleWhen returns the declared `max_stale_when` cap, falling back to
// defaultMaxStaleWhen when the workflow declares none.
func (r *sessionReactor) maxStaleWhen() time.Duration {
	if r.tick.MaxStaleWhen.Duration > 0 {
		return r.tick.MaxStaleWhen.Duration
	}
	return defaultMaxStaleWhen
}

// backoffInterval computes stale_when * 2^n capped at max. Doubling
// iteratively (rather than shifting n) keeps this well-defined for large n
// without overflowing time.Duration.
func backoffInterval(base, max time.Duration, n int) time.Duration {
	interval := base
	for i := 0; i < n && interval < max; i++ {
		interval *= 2
	}
	if interval > max {
		interval = max
	}
	return interval
}

// updateBackoff runs right after any tick (doTick, regardless of trigger) and
// decides whether the next interval resets to stale_when (a change occurred)
// or keeps growing (quiet). "Change" is fingerprint diff or an inbound event
// since the last sweep — self-emitted events are never
// Inbound (see direction normalization in service.EventPublish), so an
// orchestrator publishing to its own session every tick does not reset its
// own backoff.
func (r *sessionReactor) updateBackoff(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	fingerprint := r.compositeFingerprint()
	inbound, next := r.hasInboundSince(r.lastLogPosition())
	prev := r.lastFingerprint()
	changed := inbound || fingerprint != prev
	if err := r.state.Update(r.session, func(s *domain.Session) error {
		if s.TickBackoff == nil {
			s.TickBackoff = &contract.TickBackoff{}
		}
		s.TickBackoff.LastFingerprint = fingerprint
		s.TickBackoff.LastLogPosition = next
		if changed {
			s.TickBackoff.ConsecutiveUnchanged = 0
		} else {
			s.TickBackoff.ConsecutiveUnchanged++
		}
		return nil
	}); err != nil {
		r.logger.Warn("reactor: update tick backoff failed", "session", r.session, "error", err)
	}
}

func (r *sessionReactor) lastFingerprint() string {
	if s := r.state.Get(r.session); s != nil && s.TickBackoff != nil {
		return s.TickBackoff.LastFingerprint
	}
	return ""
}

func (r *sessionReactor) lastLogPosition() int64 {
	if s := r.state.Get(r.session); s != nil && s.TickBackoff != nil {
		return s.TickBackoff.LastLogPosition
	}
	return 0
}

// hasInboundSince reports whether any Inbound event was appended past since,
// and returns the log's current end offset to persist as the next watermark.
func (r *sessionReactor) hasInboundSince(since int64) (bool, int64) {
	evs, _, next, err := r.log.List(r.session, since, event.Filter{Direction: event.Inbound})
	if err != nil {
		slog.Default().Warn("reactor: scan inbound events failed", "session", r.session, "error", err)
		return false, since
	}
	return len(evs) > 0, next
}

// compositeFingerprint joins every produced instance's done_when fingerprint
// (computed the same way tick/check dedup keys are) into one string
// representing the whole session's observed state — the stale sweep operates
// at session granularity, not per instance.
func (r *sessionReactor) compositeFingerprint() string {
	result, err := service.CheckSession(r.cfg, r.state, service.CheckParams{SessionName: r.session})
	if err != nil {
		slog.Default().Warn("reactor: compute fingerprint failed", "session", r.session, "error", err)
		return ""
	}
	instances := make([]string, len(result.Actions))
	for i, a := range result.Actions {
		instances[i] = a.Instance + "=" + a.Fingerprint
	}
	slices.Sort(instances)
	return strings.Join(instances, "\x00")
}

// doTick actuates a tick regardless of what triggered it (declared `on`
// pattern, judge builtin, or the stale sweep) and always refreshes the
// backoff bookkeeping afterward. This must not live only in checkStale's
// caller: a reactively-triggered tick (drain, e.g. an inbound event matching
// a declared pattern) advances LastTickAt exactly like a stale-sweep tick, so
// it must reset ConsecutiveUnchanged the same way — otherwise the next
// checkStale computes its interval off a stale, inflated n and the backoff
// never actually shortens back to stale_when despite the inbound arrival
// that should have reset it.
func (r *sessionReactor) doTick(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	fn := r.tickFn
	if fn == nil {
		fn = service.TickSession
	}
	if _, err := fn(r.cfg, r.state, service.TickParams{SessionName: r.session, Observer: r.observer}); err != nil {
		slog.Default().Warn("reactor: tick failed", "session", r.session, "error", err)
		return
	}
	if r.tick.StaleWhen.Duration > 0 {
		r.updateBackoff(ctx)
	}
}
