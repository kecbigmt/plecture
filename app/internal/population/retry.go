package population

import (
	"fmt"

	"github.com/kecbigmt/plecture/app/internal/eventlog"
	"github.com/kecbigmt/plecture/app/internal/state"
	"github.com/kecbigmt/plecture/contracts/event"
	contract "github.com/kecbigmt/plecture/contracts/state"
)

// RetryPopulationMember clears an operator-approved admission suspension.
func RetryPopulationMember(store *state.Store, log *eventlog.Store, workflow, name, resource string, initialTaskID func() (string, error)) error {
	key := workflow + "/" + name
	population, err := store.Population(key)
	if err != nil {
		return fmt.Errorf("read population %q: %w", key, err)
	}
	if population == nil || population.Members[resource] == nil {
		return fmt.Errorf("population member %q not found in %q", resource, key)
	}
	member := population.Members[resource]
	if member.Tombstoned {
		return fmt.Errorf("population member %q in %q is tombstoned", resource, key)
	}
	if member.SessionName != "" {
		session, err := store.GetE(member.SessionName)
		if err != nil {
			return fmt.Errorf("read session %q: %w", member.SessionName, err)
		}
		if session != nil {
			if initial := session.Tasks["initial"]; initial != nil {
				taskID, err := initialTaskID()
				if err != nil {
					return err
				}
				if initial.Name != "initial" || initial.TaskID != taskID || initial.Resource != resource || initial.Status != contract.TaskStatusProduced {
					return fmt.Errorf("session %q still has initial task state; run `plect task cleanup initial --session %s` successfully before retrying", member.SessionName, member.SessionName)
				}
			}
		}
	}

	session := member.SessionName
	if err := store.UpdatePopulation(key, func(population *state.PopulationState) error {
		current := population.Members[resource]
		if current == nil {
			return fmt.Errorf("population member %q not found in %q", resource, key)
		}
		if current.Tombstoned {
			return fmt.Errorf("population member %q in %q is tombstoned", resource, key)
		}
		clearAdmitRetry(current)
		return nil
	}); err != nil {
		return err
	}
	if session == "" {
		return nil
	}
	_, _, _, _ = log.Append(event.Event{
		SessionName: session,
		Type:        event.TypeWorkflowPopulationRetry,
		Source:      event.SourcePlect,
		Direction:   event.Internal,
		Summary:     "population member retry requested",
		Metadata: map[string]string{
			"workflow":   workflow,
			"population": name,
			"resource":   resource,
			"reason":     "operator_retry",
		},
	})
	return nil
}
