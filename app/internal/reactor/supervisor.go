// Package reactor drives `plect tick` mechanically instead of leaving it to an
// orchestrator's judgment or memory (docs/wiki/verification-gate.md). It is
// the tick-side sibling of internal/dispatch (channel delivery): both are
// per-session followers of the same durable event log, started and stopped
// by an identical "one goroutine per up session" supervisor. They stay two
// separate packages rather than one merged follower because they enforce two
// different, independently evolving policies — channel delivery's
// `TypeChannelError` exclusion vs. tick's self-excitation whitelist — and
// because internal/service already imports internal/dispatch
// (dispatch.SeedCursor, via createsetup.go), so a reactor that calls
// service.TickSession cannot itself live inside internal/dispatch without an
// import cycle.
package reactor

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/kecbigmt/plect/app/internal/config"
	"github.com/kecbigmt/plect/app/internal/domain"
	"github.com/kecbigmt/plect/app/internal/eventlog"
	"github.com/kecbigmt/plect/app/internal/sessionhub"
	"github.com/kecbigmt/plect/app/internal/state"
	"github.com/kecbigmt/plect/app/internal/task"
	contract "github.com/kecbigmt/plect/contracts/state"
)

// Supervisor keeps at most one sessionReactor per active session, starting
// one when a session's run scope comes up and cancelling it when the scope
// goes down or the session is destroyed — the same lifecycle contract as
// dispatch.Supervisor.
type Supervisor struct {
	cfg      *config.Config
	state    *state.Store
	log      *eventlog.Store
	hub      *sessionhub.Registry
	observer task.Observer
	logger   *slog.Logger
	poll     time.Duration
}

// NewSupervisor builds a supervisor over the same event log, session state,
// and per-session reader hub the bus and dispatch.Supervisor share.
func NewSupervisor(cfg *config.Config, st *state.Store, log *eventlog.Store, hub *sessionhub.Registry) *Supervisor {
	return &Supervisor{cfg: cfg, state: st, log: log, hub: hub, logger: slog.Default(), poll: time.Second}
}

// Run polls session state and reconciles the running reactors until ctx ends,
// then cancels and joins all of them.
func (sup *Supervisor) Run(ctx context.Context) {
	active := map[string]context.CancelFunc{}
	var wg sync.WaitGroup
	defer func() {
		for _, cancel := range active {
			cancel()
		}
		wg.Wait()
	}()

	tick := time.NewTicker(sup.poll)
	defer tick.Stop()
	for {
		sup.reconcile(ctx, active, &wg)
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

func (sup *Supervisor) reconcile(ctx context.Context, active map[string]context.CancelFunc, wg *sync.WaitGroup) {
	sessions := sup.state.All()
	for name, s := range sessions {
		if _, running := active[name]; running || !hasRunScopeUp(s.Tasks) {
			continue
		}
		r := sup.buildReactor(name, s)
		rctx, cancel := context.WithCancel(ctx)
		active[name] = cancel
		wg.Go(func() { r.run(rctx) })
	}
	for name, cancel := range active {
		if s, ok := sessions[name]; !ok || !hasRunScopeUp(s.Tasks) {
			cancel()
			delete(active, name)
		}
	}
}

// buildReactor resolves the session's `[tick]` declaration once at start
// (mirroring dispatch.buildDispatcher's static channel resolution). A
// workflow load failure degrades to an empty TickConfig rather than
// abstaining entirely: the judge builtin trigger (AC2) is declaration-
// independent, so a session must still be watched for it even when its
// workflow config cannot be resolved.
func (sup *Supervisor) buildReactor(name string, s *domain.Session) *sessionReactor {
	var tc config.TickConfig
	if s.Workflow != "" {
		workflows, err := sup.cfg.LoadWorkflows(s.WorktreePath)
		if err != nil {
			sup.logger.Warn("reactor: load workflows failed; declared [tick]/heartbeat inactive, judge builtin still active", "session", name, "error", err)
		} else if wf, ok := workflows[s.Workflow]; ok && wf.Tick != nil {
			tc = *wf.Tick
		}
	}
	return &sessionReactor{
		session:  name,
		cfg:      sup.cfg,
		state:    sup.state,
		log:      sup.log,
		hub:      sup.hub,
		tick:     tc,
		observer: sup.observer,
		logger:   sup.logger,
	}
}

func hasRunScopeUp(tasks map[string]*contract.TaskState) bool {
	for _, e := range tasks {
		if e != nil && e.Scope == contract.TaskScopeRun && e.Status == contract.TaskStatusProduced {
			return true
		}
	}
	return false
}
