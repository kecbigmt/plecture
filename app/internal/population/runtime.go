package population

import (
	"context"
	"fmt"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/domain"
	"github.com/kecbigmt/plecture/app/internal/service"
	"github.com/kecbigmt/plecture/app/internal/state"
	contract "github.com/kecbigmt/plecture/contracts/state"
)

type populationConflictError struct {
	session string
	reason  string
}

func (e *populationConflictError) Error() string { return e.reason }

func serviceHooks(cfg func() *config.Config, store *state.Store, def Definition, coordinator *capacityCoordinator) Hooks {
	provenance := &contract.PopulationProvenance{Workflow: def.Workflow.Address, Name: def.Population.Name}
	return Hooks{
		Up: func(_ context.Context, resource string, inputs map[string]any) (UpOutcome, error) {
			if coordinator != nil {
				return coordinator.up(context.Background(), def, resource, inputs)
			}
			return upPopulation(cfg, store, def, provenance, resource, inputs)
		},
		Destroy: func(_ context.Context, session, resource string, force bool) error {
			current, err := store.GetE(session)
			if err != nil {
				return fmt.Errorf("read session %q state: %w", session, err)
			}
			if current == nil || current.Population == nil || *current.Population != *provenance || domain.Locator(current.Resource) != resource {
				return &populationConflictError{session: session, reason: fmt.Sprintf("session %q no longer has matching workflow-population provenance", session)}
			}
			_, err = service.Destroy(cfg(), store, service.DestroyParams{Identifier: session, Force: force})
			return err
		},
		EnsureInitial: func(_ context.Context, session, taskID, resource string) error {
			return service.EnsureInitialTask(cfg(), store, session, service.InitialTask{TaskID: taskID, Resource: resource})
		},
		Blockers: func(_ context.Context, session string) ([]string, error) {
			return service.PopulationTaskBlockers(cfg(), store, session)
		},
	}
}

func upPopulation(cfg func() *config.Config, store *state.Store, def Definition, provenance *contract.PopulationProvenance, resource string, inputs map[string]any) (UpOutcome, error) {
	name, current, collision, err := service.ResolvePopulationSession(cfg(), store, def.Workflow.Address, resource)
	if collision != nil {
		return UpOutcome{}, &populationConflictError{session: name, reason: collision.Message}
	}
	if err != nil {
		return UpOutcome{}, err
	}
	if current != nil {
		if current.Population == nil || *current.Population != *provenance || domain.Locator(current.Resource) != resource {
			return UpOutcome{}, &populationConflictError{session: name, reason: fmt.Sprintf("session %q is owned by another lifecycle authority", name)}
		}
		alreadyUp := cfg().RunScopeUp(current)
		result, err := service.Up(cfg(), store, service.UpParams{Identifier: name})
		if err != nil {
			return UpOutcome{}, err
		}
		return UpOutcome{SessionName: result.SessionName, AlreadyUp: alreadyUp, Sleeping: result.Result == "sleeping", Woke: result.Woke}, nil
	}
	result, err := service.Up(cfg(), store, service.UpParams{
		Identifier: resource,
		Workflow:   def.Workflow.Address,
		Inputs:     inputs,
		Population: provenance,
	})
	if err != nil {
		return UpOutcome{}, err
	}
	return UpOutcome{SessionName: result.SessionName, Sleeping: result.Result == "sleeping"}, nil
}
