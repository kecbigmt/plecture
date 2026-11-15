package service

import (
	"fmt"
	"time"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/domain"
	"github.com/kecbigmt/plecture/app/internal/eventlog"
	"github.com/kecbigmt/plecture/app/internal/state"
	"github.com/kecbigmt/plecture/contracts/event"
	contract "github.com/kecbigmt/plecture/contracts/state"
)

// SleepIfIdle sleeps an up session once its last explicit status clear has
// stayed newer than every inbound event for its workflow's declared duration.
func SleepIfIdle(cfg *config.Config, store *state.Store, name string, now time.Time) (bool, error) {
	slept := false
	err := withLifecycleLock(store, name, func() error {
		s, err := store.GetE(name)
		if err != nil || s == nil || s.Status != contract.SessionStatusUp || !cfg.RunScopeUp(s) {
			return err
		}
		wf, err := cfg.ResolveSessionWorkflow(s)
		if err != nil {
			return err
		}
		if wf == nil || wf.Session.SleepAfterIdle.Duration <= 0 {
			return nil
		}
		if s.ParentSession == "" && s.Population == nil {
			return nil
		}
		log := eventlog.NewStore(store.Dir())
		status, ok, err := log.LatestByType(name, event.TypeStatusMessage)
		if err != nil || !ok || status.Metadata["cleared"] != "true" {
			return err
		}
		activation := s.CreatedAt
		for _, task := range domain.MergedTasks(s) {
			if task != nil && task.Scope == contract.TaskScopeRun && task.Status == contract.TaskStatusProduced && task.SetupAt.After(activation) {
				activation = task.SetupAt
			}
		}
		if status.Time.Before(activation) {
			return nil
		}
		if now.Sub(status.Time) < wf.Session.SleepAfterIdle.Duration {
			return nil
		}
		inbound, err := log.Tail(name, event.Filter{Direction: event.Inbound}, 1)
		if err != nil {
			return fmt.Errorf("read latest inbound for idle sleep: %w", err)
		}
		if len(inbound) > 0 && inbound[0].Time.After(status.Time) {
			return nil
		}
		if _, err := downUnlocked(cfg, store, DownParams{Identifier: name, Sleep: true, automatic: true}); err != nil {
			return err
		}
		inbound, err = log.Tail(name, event.Filter{Direction: event.Inbound}, 1)
		if err != nil {
			return fmt.Errorf("read inbound after idle sleep: %w", err)
		}
		if len(inbound) > 0 && inbound[0].Time.After(status.Time) {
			if _, err := queueSleep(store, name, "event", inbound[0].Time); err != nil {
				return fmt.Errorf("request wake after idle sleep: %w", err)
			}
		}
		slept = true
		return nil
	})
	return slept, err
}

// SleepIdleSessions evaluates the opt-in idle sleep policy for every session.
func SleepIdleSessions(cfg *config.Config, store *state.Store, now time.Time) error {
	sessions, err := store.AllE()
	if err != nil {
		return err
	}
	for name, s := range sessions {
		if s == nil || s.Status != contract.SessionStatusUp {
			continue
		}
		if _, err := SleepIfIdle(cfg, store, name, now); err != nil {
			return fmt.Errorf("idle sleep %s: %w", name, err)
		}
	}
	return nil
}
