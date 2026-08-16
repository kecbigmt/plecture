package busservice

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/kecbigmt/plecture/app/internal/domain"
	"github.com/kecbigmt/plecture/app/internal/plugins"
)

// PluginSource resolves the currently mounted plugins and lockfile, purely
// from local state — config.LoadPlugins in production. The supervisor calls
// it on every reconcile pass so a `plect plugin update` while the bus is
// running is picked up without restarting the bus itself.
type PluginSource func() ([]plugins.Mounted, *plugins.Lockfile, error)

// Supervisor starts every plugin-declared [[services]] entry when Run
// begins, restarts a crashed child according to its restart policy with
// bounded backoff, restarts a service when its owning plugin's content hash
// changes, and stops every service when ctx ends. One service's failure —
// a start error, a crash loop, even a panic in this package's own loop —
// never stops another service or the supervisor itself; see runOne.
type Supervisor struct {
	source PluginSource
	Status *StatusRegistry
	logger *slog.Logger

	// Tunables. Production defaults are deliberately more relaxed than
	// dispatch/reactor's 1s follower poll: services are daemons, not
	// per-session followers reacting to durable event log writes. Tests in
	// this package override these fields directly rather than through an
	// options API, matching the convention dispatch.Supervisor/
	// reactor.Supervisor already use.
	poll        time.Duration
	baseBackoff time.Duration
	maxBackoff  time.Duration
	waitDelay   time.Duration
}

// NewSupervisor builds a Supervisor over source.
func NewSupervisor(source PluginSource) *Supervisor {
	return &Supervisor{
		source:      source,
		Status:      NewStatusRegistry(),
		logger:      slog.Default(),
		poll:        5 * time.Second,
		baseBackoff: time.Second,
		maxBackoff:  30 * time.Second,
		waitDelay:   10 * time.Second,
	}
}

// runningService is one currently-active service goroutine.
type runningService struct {
	cancel context.CancelFunc
	done   chan struct{}
	hash   string
}

// Run reconciles declared services against currently running ones every
// poll tick until ctx ends, then cancels every running service and waits
// for all of them to stop before returning.
func (sup *Supervisor) Run(ctx context.Context) {
	running := map[string]*runningService{}
	defer func() {
		for _, rs := range running {
			rs.cancel()
		}
		for _, rs := range running {
			<-rs.done
		}
	}()

	tick := time.NewTicker(sup.poll)
	defer tick.Stop()
	for {
		sup.reconcile(ctx, running)
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// reconcile is only ever called from Run's own goroutine, so running needs
// no locking of its own.
func (sup *Supervisor) reconcile(ctx context.Context, running map[string]*runningService) {
	mounted, lock, err := sup.source()
	if err != nil {
		sup.logger.Error("busservice: failed to load plugins; leaving currently running services as-is", "error", err)
		return
	}
	decls, err := BuildDeclarations(mounted, lock)
	if err != nil {
		sup.logger.Error("busservice: invalid service declarations; leaving currently running services as-is", "error", err)
		return
	}

	seen := make(map[string]bool, len(decls))
	for _, decl := range decls {
		seen[decl.ID] = true
		if rs, ok := running[decl.ID]; ok {
			if rs.hash == decl.ContentHash {
				continue
			}
			// The owning plugin's content changed (a `plect plugin update`
			// repointed its lock entry): stop the old process and wait for
			// it to actually exit before starting the replacement, so two
			// instances of the same service never run at once.
			rs.cancel()
			<-rs.done
			delete(running, decl.ID)
		}
		running[decl.ID] = sup.start(ctx, decl)
	}
	for id, rs := range running {
		if !seen[id] {
			rs.cancel()
			<-rs.done
			delete(running, id)
		}
	}
}

func (sup *Supervisor) start(ctx context.Context, decl Declaration) *runningService {
	dctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		sup.runOne(dctx, decl)
	}()
	return &runningService{cancel: cancel, done: done, hash: decl.ContentHash}
}

// runOne owns one service's whole lifetime: start, wait, and — per its
// restart policy — retry with doubling backoff, recording Status
// throughout. A recovered panic here (never expected from cmd.Start/
// cmd.Wait themselves, but defensive against a future bug in this loop) is
// logged and treated as a stopped service rather than propagating: one
// service's failure must never take the whole supervisor, and every other
// service, down with it — this goroutine boundary plus the recover is what
// makes that guarantee hold regardless of what runProcess does.
func (sup *Supervisor) runOne(ctx context.Context, decl Declaration) {
	defer func() {
		if r := recover(); r != nil {
			sup.logger.Error("busservice: recovered panic in service goroutine", "service", decl.ID, "panic", r)
			sup.Status.Update(decl.ID, func(st *Status) {
				st.Running = false
				st.LastError = fmt.Sprintf("panic: %v", r)
				st.Health = domain.HealthUnhealthy
			})
		}
	}()

	sup.Status.Update(decl.ID, func(st *Status) {
		st.ID, st.PluginID, st.Name = decl.ID, decl.PluginID, decl.Name
		st.ContentHash = decl.ContentHash
	})

	if missing := missingRequiredEnv(decl.RequiredEnv); len(missing) > 0 {
		// Naturally inert, not a failure: e.g. the Slack service without
		// SLACK_BOT_TOKEN configured. Checked once per (re)start attempt —
		// required env is expected to come from the bus process's own
		// environment, which does not change during the bus's lifetime, so
		// there is nothing to gain by rechecking every poll tick.
		sup.Status.Update(decl.ID, func(st *Status) {
			st.Running = false
			st.Health = domain.HealthUndeclared
			st.LastError = fmt.Sprintf("missing required environment: %s", strings.Join(missing, ", "))
		})
		return
	}

	restart := decl.Restart
	if restart == "" {
		restart = plugins.ServiceRestartOnFailure
	}

	backoff := sup.baseBackoff
	for {
		if ctx.Err() != nil {
			return
		}
		runErr := runProcess(ctx, decl, sup.logger, sup.waitDelay, func(pid int) {
			sup.Status.Update(decl.ID, func(st *Status) {
				st.Running = true
				st.PID = pid
				st.Health = domain.HealthHealthy
				st.LastError = ""
			})
		})

		if ctx.Err() != nil {
			// Our own shutdown SIGTERM caused this exit; record it as an
			// intentional stop, not a failure, and never restart.
			sup.Status.Update(decl.ID, func(st *Status) {
				st.Running = false
				st.Health = domain.HealthUndeclared
				st.LastError = ""
				st.LastExitAt = time.Now()
			})
			return
		}

		sup.Status.Update(decl.ID, func(st *Status) {
			st.Running = false
			st.LastExitAt = time.Now()
			if runErr != nil {
				st.LastError = runErr.Error()
			} else {
				st.LastError = ""
			}
		})

		if restart == plugins.ServiceRestartNever {
			sup.Status.Update(decl.ID, func(st *Status) { st.Health = domain.HealthUndeclared })
			return
		}

		// A long-running daemon exiting at all is unexpected, so on-failure
		// restarts even a clean (zero-status) exit: the two restart
		// policies this design offers are "keep it running" and "never
		// restart it," not "only restart a non-zero exit" — see the field
		// table in docs/design/plugin-packaging.md.
		sup.Status.Update(decl.ID, func(st *Status) {
			st.RestartCount++
			st.Health = domain.HealthUnhealthy
		})
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff *= 2
		if backoff > sup.maxBackoff {
			backoff = sup.maxBackoff
		}
	}
}
