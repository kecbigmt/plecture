package population

import (
	"context"
	"errors"
	"testing"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/service"
	"github.com/kecbigmt/plecture/app/internal/state"
	contract "github.com/kecbigmt/plecture/contracts/state"
)

func TestFailedInitialTaskIsNotAcceptedAsSuccessfullyInstalled(t *testing.T) {
	store := state.NewStore(t.TempDir())
	if err := store.Put(&contract.Session{
		Name: "member",
		Tasks: map[string]*contract.TaskState{
			"initial": {
				Name: "initial", TaskID: "work", Resource: "urn:case:a",
				Dynamic: true, Status: contract.TaskStatusFailed,
			},
		},
	}); err != nil {
		t.Fatal(err)
	}
	hooks := serviceHooks(func() *config.Config { return nil }, store, Definition{}, nil)
	if err := hooks.EnsureInitial(context.Background(), "member", "work", "urn:case:a"); err == nil {
		t.Fatal("failed initial task was accepted as installed")
	}
}

func TestPopulationCannotAdoptSameProvenanceSessionForAnotherResource(t *testing.T) {
	cfg := populationConfig(t, `[source.query.poll]
type = "exec"
command = "true"
`, `poll_every = "1m"`, `resource = { from = "resource.id" }`)
	definitions, err := Load(cfg)
	if err != nil {
		t.Fatal(err)
	}
	definition := definitions[0]
	store := state.NewStore(t.TempDir())
	provenance := &contract.PopulationProvenance{Workflow: definition.Workflow.Address, Name: definition.Population.Name}
	name, err := service.ResolvePopulationSessionName(cfg, definition.Workflow.Address, "urn:case:a")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Put(&contract.Session{Name: name, ResourceID: "urn:case:b", Workflow: definition.Workflow.Address, Population: provenance}); err != nil {
		t.Fatal(err)
	}

	_, err = upPopulation(func() *config.Config { return cfg }, store, definition, provenance, "urn:case:a", nil)
	var conflict *populationConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("error = %v, want population identity conflict", err)
	}
}
