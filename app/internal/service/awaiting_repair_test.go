package service

import (
	"testing"

	"github.com/kecbigmt/plecture/app/internal/domain"
	"github.com/kecbigmt/plecture/app/internal/state"
	contract "github.com/kecbigmt/plecture/contracts/state"
)

func TestAwaitingRepair(t *testing.T) {
	tests := []struct {
		name     string
		alive    string
		attempts int
		prepare  func(t *testing.T, name string, store *state.Store)
		want     bool
	}{
		{name: "observed liveness failure on an opted-in session", alive: "false", attempts: 3, want: true},
		{name: "passing liveness probe", alive: "true", attempts: 3, want: false},
		{name: "session that did not opt in to automatic repair", alive: "false", attempts: 0, want: false},
		{
			name: "suspended repair", alive: "false", attempts: 3, want: false,
			prepare: func(t *testing.T, name string, store *state.Store) {
				if err := appendRepairEvent(store, name, "health", 3, "suspended", "automatic repair suspended after 3 attempts"); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "suspension cleared by a later successful reset", alive: "false", attempts: 3, want: true,
			prepare: func(t *testing.T, name string, store *state.Store) {
				if err := appendRepairEvent(store, name, "health", 3, "suspended", "automatic repair suspended after 3 attempts"); err != nil {
					t.Fatal(err)
				}
				if err := appendRepairEvent(store, name, "manual_up", 0, "succeeded", "human up succeeded"); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "undecidable probe", alive: "false", attempts: 3, want: false,
			prepare: func(t *testing.T, name string, store *state.Store) {
				if err := store.Update(name, func(sess *domain.Session) error {
					sess.Health = &contract.HealthState{UnevaluableCycles: 3, LastState: "unhealthy"}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := testStore(t)
			cfg := repairWorkflowFixture(t, "echo '{}'", tt.alive, tt.attempts)
			const name = "session-1"
			seedRepairSession(t, store, name, map[string]*contract.TaskState{
				"initial": {Scope: contract.TaskScopeRun, TaskID: "runner", Status: contract.TaskStatusProduced},
			})
			if err := store.Update(name, func(s *domain.Session) error {
				s.Status = contract.SessionStatusUp
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if tt.prepare != nil {
				tt.prepare(t, name, store)
			}
			got, err := AwaitingRepair(cfg, store, name)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Fatalf("AwaitingRepair = %t, want %t", got, tt.want)
			}
		})
	}
}

func TestAwaitingRepair_UnknownSessionIsNotAwaiting(t *testing.T) {
	store := testStore(t)
	cfg := repairWorkflowFixture(t, "echo '{}'", "false", 3)
	got, err := AwaitingRepair(cfg, store, "absent")
	if err != nil || got {
		t.Fatalf("AwaitingRepair = (%t, %v), want (false, nil)", got, err)
	}
}
