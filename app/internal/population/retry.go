package population

import (
	"fmt"

	"github.com/kecbigmt/plecture/app/internal/eventlog"
	"github.com/kecbigmt/plecture/app/internal/state"
	"github.com/kecbigmt/plecture/contracts/event"
	contract "github.com/kecbigmt/plecture/contracts/state"
)

// RetryPopulationMember clears an operator-approved admission suspension.
func RetryPopulationMember(store *state.Store, log *eventlog.Store, key contract.PopulationProvenance, resource string, initialTaskID func() (string, error)) error {
	label := populationLabel(key)
	population, err := store.Population(key)
	if err != nil {
		return fmt.Errorf("read population %q: %w", label, err)
	}
	memberKey, member := findMember(population, resource)
	if member == nil {
		return fmt.Errorf("population member %q not found in %q", resource, label)
	}
	if member.Tombstoned {
		return fmt.Errorf("population member %q in %q is tombstoned", resource, label)
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
				if initial.Name != "initial" || initial.TaskID != taskID || !sameMemberResource(initial.Resource, member.Resource) || initial.Status != contract.TaskStatusProduced {
					return fmt.Errorf("session %q still has initial task state; run `plect task cleanup initial --session %s` successfully before retrying", member.SessionName, member.SessionName)
				}
			}
		}
	}

	session := member.SessionName
	if err := store.UpdatePopulation(key, func(population *state.PopulationState) error {
		current := population.Members[memberKey]
		if current == nil {
			return fmt.Errorf("population member %q not found in %q", resource, label)
		}
		if current.Tombstoned {
			return fmt.Errorf("population member %q in %q is tombstoned", resource, label)
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
			"workflow":   key.Workflow,
			"population": key.Name,
			"resource":   member.Resource.Locator,
			"reason":     "operator_retry",
		},
	})
	return nil
}

// findMember accepts a member's resource name or its locator, the two forms an
// operator sees.
func findMember(population *state.PopulationState, ref string) (string, *state.PopulationMember) {
	if population == nil {
		return "", nil
	}
	if member := population.Members[ref]; member != nil {
		return ref, member
	}
	for name, member := range population.Members {
		if member != nil && member.Resource.Locator == ref {
			return name, member
		}
	}
	return "", nil
}

func sameMemberResource(bound *contract.Resource, member contract.Resource) bool {
	return bound != nil && bound.Name == member.Name
}
