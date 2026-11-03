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

// SleepQueuePosition returns zero for a full stop or event-waiting sleep.
func SleepQueuePosition(store *state.Store, name string) (int, error) {
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
		if s.SleepSince.IsZero() || s.Status != contract.SessionStatusDown || sessionFinalized(s) {
			continue
		}
		ready, err := sleepQueued(store, candidate)
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

func sleepVisibility(store *state.Store, s *domain.Session) (string, int, error) {
	if s.SleepSince.IsZero() || s.Status != contract.SessionStatusDown {
		return "", 0, nil
	}
	position, err := SleepQueuePosition(store, s.Name)
	if err != nil {
		return "", 0, err
	}
	if position > 0 {
		return "waiting_capacity", position, nil
	}
	return "waiting_event", 0, nil
}

func queueSleep(store *state.Store, name, reason string, at time.Time) (bool, error) {
	pending, err := sleepQueued(store, name)
	if err != nil {
		return false, err
	}
	queued := false
	err = store.Update(name, func(s *domain.Session) error {
		if s.Status == contract.SessionStatusDestroyed {
			return nil
		}
		if !s.SleepSince.IsZero() && pending {
			return nil
		}
		s.SleepSince = at.UTC()
		s.Status = contract.SessionStatusDown
		s.UpdatedAt = time.Now()
		queued = true
		return nil
	})
	if err != nil || !queued {
		return queued, err
	}
	if err := appendSleepEvent(store, name, reason, "waiting for capacity"); err != nil {
		return true, fmt.Errorf("record sleep event: %w", err)
	}
	return true, nil
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
		if s.Status != contract.SessionStatusDown || s.SleepSince.IsZero() || sessionFinalized(s) {
			continue
		}
		queued, err := sleepQueued(store, name)
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
		count, err := wakeFailureCount(store, item.name)
		if err != nil {
			return err
		}
		limit := 3
		if wf, err := cfg.ResolveSessionWorkflow(sessions[item.name]); err == nil && wf != nil && wf.Health.AutoRepairAttempts > 0 {
			limit = wf.Health.AutoRepairAttempts
		}
		if count >= limit {
			continue
		}
		result, err := Up(cfg, store, UpParams{Identifier: item.name})
		if err == nil && result.Result == "sleeping" {
			continue
		}
		if err != nil {
			if svcErr, ok := err.(*Error); ok && svcErr.Code == ErrChildUpInProgress {
				continue
			}
			if appendErr := appendWakeEvent(store, item.name, "failed", err.Error()); appendErr != nil {
				return appendErr
			}
			if count+1 >= limit {
				if appendErr := appendWakeEvent(store, item.name, "suspended", fmt.Sprintf("automatic wake suspended after %d attempts: %v", limit, err)); appendErr != nil {
					return appendErr
				}
			}
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
