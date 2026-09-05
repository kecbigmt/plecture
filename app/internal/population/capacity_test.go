package population

import (
	"testing"
	"time"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/eventlog"
	"github.com/kecbigmt/plecture/app/internal/state"
	"github.com/kecbigmt/plecture/contracts/event"
	contract "github.com/kecbigmt/plecture/contracts/state"
)

func capacityFixture(t *testing.T) (*capacityCoordinator, Definition, *state.Store, *eventlog.Store, time.Time) {
	t.Helper()
	store := state.NewStore(t.TempDir())
	logStore := eventlog.NewStore(store.Dir())
	def := Definition{
		Workflow:   config.WorkflowFile{Address: "agent"},
		Population: config.WorkflowPopulation{Name: "dispatch", AutoDown: true},
	}
	coordinator := newCapacityCoordinator(func() *config.Config { return &config.Config{} }, store, logStore)
	coordinator.setDefinitions([]Definition{def})
	return coordinator, def, store, logStore, time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC)
}

func addCapacityMember(t *testing.T, def Definition, store *state.Store, logStore *eventlog.Store, name, resource string, created, cleared time.Time) {
	t.Helper()
	if err := store.Put(&contract.Session{
		Name:       name,
		ResourceID: resource,
		Population: &contract.PopulationProvenance{Workflow: def.Workflow.Address, Name: def.Population.Name},
		Tasks: map[string]*contract.TaskState{
			"runtime": {Scope: contract.TaskScopeRun, Status: contract.TaskStatusProduced},
		},
		CreatedAt: created,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdatePopulation(populationKey(def), func(population *state.PopulationState) error {
		population.Workflow = def.Workflow.Address
		population.Name = def.Population.Name
		population.Members[resource] = &state.PopulationMember{ResourceID: resource, SessionName: name, Generation: 1}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !cleared.IsZero() {
		if _, _, _, err := logStore.Append(event.Event{
			SessionName: name,
			Time:        cleared,
			Type:        event.TypeStatusMessage,
			Metadata:    map[string]string{"cleared": "true"},
		}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCapacityCandidatesRequireExplicitIdleAndUseOldestActivity(t *testing.T) {
	coordinator, def, store, logStore, base := capacityFixture(t)
	addCapacityMember(t, def, store, logStore, "newer", "urn:case:newer", base, base.Add(3*time.Minute))
	addCapacityMember(t, def, store, logStore, "older", "urn:case:older", base, base.Add(time.Minute))
	addCapacityMember(t, def, store, logStore, "never-reported", "urn:case:never", base, time.Time{})

	candidates := coordinator.idleCandidates()
	if len(candidates) != 2 || candidates[0].session != "older" || candidates[1].session != "newer" {
		t.Fatalf("candidates = %+v, want explicitly idle members oldest first", candidates)
	}
}

func TestCapacityCandidateIdleEvidenceIsInvalidatedByInboundEvent(t *testing.T) {
	coordinator, def, store, logStore, base := capacityFixture(t)
	addCapacityMember(t, def, store, logStore, "member", "urn:case:member", base, base.Add(time.Minute))
	if _, _, _, err := logStore.Append(event.Event{
		SessionName: "member",
		Time:        base.Add(2 * time.Minute),
		Type:        "external.message",
		Direction:   event.Inbound,
	}); err != nil {
		t.Fatal(err)
	}

	if candidates := coordinator.idleCandidates(); len(candidates) != 0 {
		t.Fatalf("candidates = %+v, want inbound activity to invalidate earlier idle evidence", candidates)
	}
}

func TestCapacityCandidatesExcludeAutoDownFalse(t *testing.T) {
	coordinator, def, store, logStore, base := capacityFixture(t)
	def.Population.AutoDown = false
	coordinator.setDefinitions([]Definition{def})
	addCapacityMember(t, def, store, logStore, "member", "urn:case:member", base, base.Add(time.Minute))

	if candidates := coordinator.idleCandidates(); len(candidates) != 0 {
		t.Fatalf("candidates = %+v, want auto_down=false excluded", candidates)
	}
}
