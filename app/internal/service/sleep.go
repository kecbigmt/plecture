package service

import (
	"fmt"
	"slices"
	"time"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/domain"
	"github.com/kecbigmt/plecture/app/internal/eventlog"
	"github.com/kecbigmt/plecture/app/internal/state"
	"github.com/kecbigmt/plecture/contracts/event"
	contract "github.com/kecbigmt/plecture/contracts/state"
)

func appendSleepEvent(store *state.Store, name, reason, summary string) error {
	_, _, _, err := eventlog.NewStore(store.Dir()).Append(event.Event{
		SessionName: name, Type: event.TypeSessionSleep, Source: event.SourcePlect,
		Direction: event.Internal, Summary: summary,
		Metadata: map[string]string{"reason": reason},
	})
	return err
}

func appendStopEvent(store *state.Store, name string) error {
	_, _, _, err := eventlog.NewStore(store.Dir()).Append(event.Event{
		SessionName: name, Type: event.TypeSessionStop, Source: event.SourcePlect,
		Direction: event.Internal, Summary: "session fully stopped",
	})
	return err
}

func appendWakeEvent(store *state.Store, name, result, summary string) error {
	_, _, _, err := eventlog.NewStore(store.Dir()).Append(event.Event{
		SessionName: name, Type: event.TypeSessionWake, Source: event.SourcePlect,
		Direction: event.Internal, Summary: summary,
		Metadata: map[string]string{"result": result},
	})
	return err
}

func sleepQueued(store *state.Store, name string) (bool, error) {
	evs, err := eventlog.NewStore(store.Dir()).Tail(name, event.Filter{Types: []string{event.TypeSessionSleep}}, 1)
	if err != nil || len(evs) == 0 {
		return false, err
	}
	return evs[0].Metadata["reason"] != "idle", nil
}

func queuedForWake(cfg *config.Config, store *state.Store, s *domain.Session) (bool, error) {
	if s == nil || s.Status != contract.SessionStatusDown || s.SleepSince.IsZero() || sessionFinalized(s) {
		return false, nil
	}
	queued, err := sleepQueued(store, s.Name)
	if err != nil || !queued {
		return false, err
	}
	suspended, err := wakeSuspensionReason(store, s.Name)
	if err != nil || suspended != "" {
		return false, err
	}
	count, err := wakeFailureCount(store, s.Name)
	if err != nil {
		return false, err
	}
	return count < wakeAttemptLimit(cfg, s), nil
}

func wakeAttemptLimit(cfg *config.Config, s *domain.Session) int {
	if cfg != nil {
		if wf, err := cfg.ResolveSessionWorkflow(s); err == nil && wf != nil && wf.Health.AutoRepairAttempts > 0 {
			return wf.Health.AutoRepairAttempts
		}
	}
	return 3
}

// SleepQueuePosition returns zero when a session is ineligible for automatic wake.
func SleepQueuePosition(cfg *config.Config, store *state.Store, name string) (int, error) {
	sessions, err := store.AllE()
	if err != nil {
		return 0, err
	}
	type queued struct {
		name string
		at   time.Time
	}
	var waiting []queued
	for candidate, s := range sessions {
		ready, err := queuedForWake(cfg, store, s)
		if err != nil {
			return 0, err
		}
		if ready {
			waiting = append(waiting, queued{candidate, s.SleepSince})
		}
	}
	slices.SortFunc(waiting, func(a, b queued) int {
		if d := a.at.Compare(b.at); d != 0 {
			return d
		}
		if a.name < b.name {
			return -1
		}
		if a.name > b.name {
			return 1
		}
		return 0
	})
	for i, item := range waiting {
		if item.name == name {
			return i + 1, nil
		}
	}
	return 0, nil
}

func sleepVisibility(cfg *config.Config, store *state.Store, s *domain.Session) (string, int, error) {
	if s.SleepSince.IsZero() || s.Status != contract.SessionStatusDown {
		return "", 0, nil
	}
	position, err := SleepQueuePosition(cfg, store, s.Name)
	if err != nil {
		return "", 0, err
	}
	if position > 0 {
		return "waiting_capacity", position, nil
	}
	suspended, err := wakeSuspensionReason(store, s.Name)
	if err != nil {
		return "", 0, err
	}
	if suspended != "" {
		return "suspended", 0, nil
	}
	return "waiting_event", 0, nil
}

func queueSleep(store *state.Store, name, reason string, at time.Time) (bool, error) {
	pending, err := sleepQueued(store, name)
	if err != nil {
		return false, err
	}
	s, err := store.GetE(name)
	if err != nil {
		return false, err
	}
	suspended, err := wakeSuspensionReason(store, name)
	if err != nil {
		return false, err
	}
	if s == nil || s.Status == contract.SessionStatusDestroyed || (!s.SleepSince.IsZero() && pending && suspended == "") {
		return false, nil
	}
	// An append failure must not leave a sleep marker with no queue reason.
	if err := appendSleepEvent(store, name, reason, "waiting for capacity"); err != nil {
		return false, fmt.Errorf("record sleep event: %w", err)
	}
	err = store.Update(name, func(s *domain.Session) error {
		s.SleepSince = at.UTC()
		s.Status = contract.SessionStatusDown
		s.UpdatedAt = time.Now()
		return nil
	})
	return err == nil, err
}

// RequestWake records the first inbound wake request for a sleeping session.
func RequestWake(store *state.Store, name string, at time.Time) (bool, error) {
	woken := false
	err := withLifecycleLock(store, name, func() error {
		s, err := store.GetE(name)
		if err != nil || s == nil || s.SleepSince.IsZero() || s.Status != contract.SessionStatusDown || sessionFinalized(s) {
			return err
		}
		woken, err = queueSleep(store, name, "event", at)
		return err
	})
	return woken, err
}

func sessionFinalized(s *domain.Session) bool {
	active := 0
	for _, task := range s.Tasks {
		if task == nil || task.Status == contract.TaskStatusCleaned {
			continue
		}
		active++
		if task.FinalizedAt.IsZero() {
			return false
		}
	}
	return active > 0
}

// WakeQueuedSessions retries queued sessions in the order their wake intent
// was recorded. A capacity-blocked attempt stays queued at its original place.
func WakeQueuedSessions(cfg *config.Config, store *state.Store) error {
	sessions, err := store.AllE()
	if err != nil {
		return err
	}
	type candidate struct {
		name string
		at   time.Time
	}
	var pending []candidate
	for name, s := range sessions {
		queued, err := queuedForWake(cfg, store, s)
		if err != nil {
			return err
		}
		if queued {
			pending = append(pending, candidate{name, s.SleepSince})
		}
	}
	slices.SortFunc(pending, func(a, b candidate) int {
		if d := a.at.Compare(b.at); d != 0 {
			return d
		}
		if a.name < b.name {
			return -1
		}
		if a.name > b.name {
			return 1
		}
		return 0
	})
	for _, item := range pending {
		err := tryLifecycleLock(store, item.name, func() error {
			current, err := store.GetE(item.name)
			if err != nil {
				return err
			}
			queued, err := queuedForWake(cfg, store, current)
			if err != nil || !queued {
				return err
			}
			count, err := wakeFailureCount(store, item.name)
			if err != nil {
				return err
			}
			limit := wakeAttemptLimit(cfg, current)
			result, upErr := upUnlocked(cfg, store, UpParams{Identifier: item.name})
			if upErr == nil && result.Result == "sleeping" {
				return nil
			}
			if upErr == nil {
				return nil
			}
			if svcErr, ok := upErr.(*Error); ok && svcErr.Code == ErrChildUpInProgress {
				return nil
			}
			if err := appendWakeEvent(store, item.name, "failed", upErr.Error()); err != nil {
				return err
			}
			if count+1 >= limit {
				return appendWakeEvent(store, item.name, "suspended", fmt.Sprintf("automatic wake suspended after %d attempts: %v", limit, upErr))
			}
			return nil
		})
		if err == errLifecycleBusy {
			continue
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func wakeFailureCount(store *state.Store, name string) (int, error) {
	evs, _, _, err := eventlog.NewStore(store.Dir()).List(name, 0, event.Filter{Types: []string{event.TypeSessionSleep, event.TypeSessionWake}})
	if err != nil {
		return 0, err
	}
	count := 0
	for _, ev := range evs {
		if ev.Type == event.TypeSessionSleep {
			count = 0
		} else if ev.Metadata["result"] == "failed" {
			count++
		}
	}
	return count, nil
}

func wakeSuspensionReason(store *state.Store, name string) (string, error) {
	evs, _, _, err := eventlog.NewStore(store.Dir()).List(name, 0, event.Filter{Types: []string{event.TypeSessionSleep, event.TypeSessionWake}})
	if err != nil {
		return "", err
	}
	for i := len(evs) - 1; i >= 0; i-- {
		if evs[i].Type == event.TypeSessionSleep {
			return "", nil
		}
		if evs[i].Metadata["result"] == "succeeded" {
			return "", nil
		}
		if evs[i].Metadata["result"] == "suspended" {
			return evs[i].Summary, nil
		}
	}
	return "", nil
}
