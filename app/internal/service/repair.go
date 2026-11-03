package service

import (
	"fmt"
	"strconv"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/domain"
	"github.com/kecbigmt/plecture/app/internal/eventlog"
	"github.com/kecbigmt/plecture/app/internal/state"
	"github.com/kecbigmt/plecture/contracts/event"
	contract "github.com/kecbigmt/plecture/contracts/state"
)

type RepairGate struct {
	slot chan struct{}
}

func NewRepairGate() *RepairGate {
	return &RepairGate{slot: make(chan struct{}, 1)}
}

func (g *RepairGate) acquire() bool {
	select {
	case g.slot <- struct{}{}:
		return true
	default:
		return false
	}
}

func (g *RepairGate) release() {
	<-g.slot
}

func RepairSession(cfg *config.Config, store *state.Store, name string, report *HealthReport, gate *RepairGate) (bool, error) {
	if gate == nil {
		return false, fmt.Errorf("repair gate is required")
	}
	before, err := store.GetE(name)
	if err != nil || before == nil {
		return false, err
	}
	limit, err := workflowRepairLimit(cfg, before)
	if err != nil {
		return false, err
	}
	run := sessionRunState(cfg, before)
	if trigger, _ := repairCandidate(before, limit, run, report); trigger == "" {
		return false, nil
	}
	if !gate.acquire() {
		return false, nil
	}
	defer gate.release()
	attempted := false
	err = withLifecycleLock(store, name, func() error {
		latest, err := store.GetE(name)
		if err != nil || latest == nil {
			return err
		}
		limit, err := workflowRepairLimit(cfg, latest)
		if err != nil {
			return err
		}
		run := sessionRunState(cfg, latest)
		var freshReport *HealthReport
		if run == domain.RunUp && latest.Status == contract.SessionStatusUp && limit > 0 {
			observed, err := EvaluateHealth(cfg, store, name)
			if err != nil {
				return err
			}
			freshReport = &observed
			if observed.ProbeUnevaluable {
				return appendRepairEvent(store, name, "health", 0, "skipped_probe_error", observed.Reason)
			}
		}
		trigger, reason := repairCandidate(latest, limit, run, freshReport)
		if trigger == "" {
			return nil
		}
		attempts, suspended, err := repairAttemptState(store, name)
		if err != nil {
			return err
		}
		if attempts >= limit {
			suspendReason := fmt.Sprintf("%s; automatic repair suspended after %d attempts", reason, limit)
			if !suspended {
				if err := appendRepairEvent(store, name, trigger, attempts, "suspended", suspendReason); err != nil {
					return err
				}
			}
			return nil
		}
		if err := appendRepairEvent(store, name, trigger, attempts+1, "started", reason); err != nil {
			return err
		}
		attempted = true
		_, upErr := upUnlocked(cfg, store, UpParams{Identifier: name})
		result := "succeeded"
		if upErr != nil {
			result = "failed"
		}
		if err := appendRepairEvent(store, name, trigger, attempts+1, result, reason); err != nil {
			return err
		}
		return upErr
	})
	return attempted, err
}

func workflowRepairLimit(cfg *config.Config, s *domain.Session) (int, error) {
	if s == nil || s.Status != contract.SessionStatusUp || s.Workflow == "" {
		return 0, nil
	}
	wf, err := cfg.ResolveSessionWorkflow(s)
	if err != nil {
		return 0, err
	}
	if wf == nil {
		return 0, nil
	}
	return wf.Health.AutoRepairAttempts, nil
}

func repairAttemptState(store *state.Store, name string) (int, bool, error) {
	attempts, suspended, _, err := repairAttemptObservation(store, name)
	return attempts, suspended, err
}

func repairAttemptObservation(store *state.Store, name string) (int, bool, string, error) {
	events, _, _, err := eventlog.NewStore(store.Dir()).List(name, 0, event.Filter{Types: []string{event.TypeSessionRepair}})
	if err != nil {
		return 0, false, "", err
	}
	attempts := 0
	suspended := false
	reason := ""
	for _, ev := range events {
		// Keep these state changes aligned with events_repair_state_change_idx in persistence/schema.sql.
		switch {
		case ev.Metadata["result"] == "succeeded" && ev.Metadata["attempt"] == "0":
			attempts = 0
			suspended = false
			reason = ""
		case ev.Metadata["result"] == "started":
			attempts++
			suspended = false
			reason = ""
		case ev.Metadata["result"] == "suspended":
			suspended = true
			reason = ev.Metadata["reason"]
		}
	}
	return attempts, suspended, reason, nil
}

func resetRepairStreak(store *state.Store, name, trigger string) error {
	attempts, suspended, err := repairAttemptState(store, name)
	if err != nil {
		return err
	}
	if attempts == 0 && !suspended {
		return nil
	}
	reason := "healthy state observed"
	if trigger == "manual_up" {
		reason = "human up succeeded"
	}
	if err := appendRepairEvent(store, name, trigger, 0, "succeeded", reason); err != nil {
		return err
	}
	return nil
}

func repairCandidate(s *domain.Session, limit int, run domain.RunState, report *HealthReport) (string, string) {
	if s == nil || s.Status != contract.SessionStatusUp || !s.DestroyedAt.IsZero() || limit <= 0 {
		return "", ""
	}
	if initial := domain.TaskState(s, "initial"); initial != nil && !initial.FinalizedAt.IsZero() {
		return "", ""
	}
	if run == domain.RunDown {
		return "run_down", "run is down while session status is up"
	}
	if report != nil && report.Repairable && !report.ProbeUnevaluable {
		return "health", report.Reason
	}
	return "", ""
}

func appendRepairEvent(store *state.Store, name, trigger string, attempt int, result, reason string) error {
	_, _, _, err := eventlog.NewStore(store.Dir()).Append(event.Event{
		SessionName: name,
		Type:        event.TypeSessionRepair,
		Source:      event.SourcePlect,
		Direction:   event.Internal,
		Summary:     fmt.Sprintf("repair attempt %d %s", attempt, result),
		Metadata: map[string]string{
			"trigger": trigger,
			"attempt": strconv.Itoa(attempt),
			"result":  result,
			"reason":  reason,
		},
	})
	return err
}
