package population

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kecbigmt/plecture/app/internal/admitstatus"
	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/eventlog"
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
				Scope: contract.TaskScopeSession, Status: contract.TaskStatusFailed,
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

// seedPopulationRow upserts an empty population row so a test can put a
// session referencing it directly (sessions.population_workflow/name FK)
// without going through the engine's own admission flow.
func seedPopulationRow(t *testing.T, store *state.Store, def Definition) {
	t.Helper()
	if err := store.UpdatePopulation(populationKey(def), func(population *state.PopulationState) error {
		population.Workflow = def.Workflow.Address
		population.Name = def.Population.Name
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestPopulationCannotAdoptSameProvenanceSessionForAnotherResource(t *testing.T) {
	cfg := populationConfig(t, `[source.query.poll]
type = "exec"
command = "true"
`, "uses = [\"poll\"]\npoll_every = \"1m\"", `resource = { from = "resource.id" }`)
	definitions, err := Load(cfg)
	if err != nil {
		t.Fatal(err)
	}
	definition := definitions[0]
	store := state.NewStore(t.TempDir())
	seedPopulationRow(t, store, definition)
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

func TestUpPopulationReportsRunStateFromBeforeTheIdempotentUpCall(t *testing.T) {
	// The lifecycle guards read the ambient session from the environment, so
	// an inherited one would decide this test's parenting and its down step.
	t.Setenv("PLECT_SESSION_NAME", "")
	cfg := populationConfig(t, `[source.query.poll]
type = "exec"
command = "true"
`, "uses = [\"poll\"]\npoll_every = \"1m\"", `resource = { from = "resource.id" }`)
	definitions, err := Load(cfg)
	if err != nil {
		t.Fatal(err)
	}
	definition := definitions[0]
	store := state.NewStore(t.TempDir())
	seedPopulationRow(t, store, definition)
	provenance := &contract.PopulationProvenance{Workflow: definition.Workflow.Address, Name: definition.Population.Name}
	up := func() UpOutcome {
		t.Helper()
		outcome, upErr := upPopulation(func() *config.Config { return cfg }, store, definition, provenance,
			"urn:case:a", map[string]any{"resource": "urn:case:a"})
		if upErr != nil {
			t.Fatalf("upPopulation: %v", upErr)
		}
		return outcome
	}

	first := up()
	if first.SessionName == "" || first.AlreadyUp {
		t.Fatalf("first admission = %+v, want a new session reported as a transition", first)
	}
	if second := up(); !second.AlreadyUp {
		t.Fatalf("re-admission of a member that never went down = %+v, want no transition", second)
	}
	if _, err := service.Down(cfg, store, service.DownParams{Identifier: first.SessionName}); err != nil {
		t.Fatal(err)
	}
	if resumed := up(); resumed.AlreadyUp {
		t.Fatalf("resuming a down member = %+v, want a transition", resumed)
	}
}

func TestPopulationCannotAdoptSameProvenanceSessionRecordedForAnotherEntry(t *testing.T) {
	t.Setenv("PLECT_SESSION_NAME", "")
	cfg := populationConfig(t, `[source.query.poll]
type = "exec"
command = "true"
`, "uses = [\"poll\"]\npoll_every = \"1m\"", `resource = { from = "resource.id" }`)
	definitions, err := Load(cfg)
	if err != nil {
		t.Fatal(err)
	}
	definition := definitions[0]
	provenance := &contract.PopulationProvenance{Workflow: definition.Workflow.Address, Name: definition.Population.Name}
	name, err := service.ResolvePopulationSessionName(cfg, definition.Workflow.Address, "urn:case:a")
	if err != nil {
		t.Fatal(err)
	}
	for _, recorded := range []struct{ label, resourceType string }{{"another type", "other"}, {"no recorded entry", ""}} {
		t.Run(recorded.label, func(t *testing.T) {
			store := state.NewStore(t.TempDir())
			seedPopulationRow(t, store, definition)
			if err := store.Put(&contract.Session{Name: name, ResourceID: "urn:case:a", ResourceType: recorded.resourceType, Workflow: definition.Workflow.Address, Population: provenance}); err != nil {
				t.Fatal(err)
			}

			_, err := upPopulation(func() *config.Config { return cfg }, store, definition, provenance, "urn:case:a", nil)
			var conflict *populationConflictError
			if !errors.As(err, &conflict) {
				t.Fatalf("error = %v, want a population identity conflict", err)
			}
		})
	}
}

func TestPopulationKeepsQueryInputsApartFromResourceActionInputs(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "actions.log")
	writeDefinition(t, dir, "observer", fmt.Sprintf(`[source]
kind = "resource"
match = "^urn:case:(?P<id>[A-Za-z0-9]+)$"
name = { from = "match.id" }

[source.observe]
type = "exec"
command = "true"

[source.subscribe]
type   = "shell"
script = '''
printf 'subscribe scope=%%s\n' "$scope" >> %[1]q
'''

[source.subscribe.bind]
scope = { from = "resource.inputs.scope", default = "absent" }

[source.inputs_schema]
type = "object"
additionalProperties = false

[source.inputs_schema.properties.scope]
type = "string"

[source.query.inputs_schema]
type = "object"
required = ["scope"]
additionalProperties = false

[source.query.inputs_schema.properties.scope]
type = "string"

[source.query.item_schema]
type = "object"
required = ["resource"]
additionalProperties = false

[source.query.item_schema.properties.resource]
type = "string"

[source.query.poll]
type   = "shell"
script = '''
printf 'poll scope=%%s\n' "$scope" >> %[1]q
echo '[{"resource":"urn:case:a"}]'
'''

[source.query.poll.bind]
scope = { from = "inputs.scope" }
`, log))
	writeDefinition(t, dir, "workflow", `[agent]
kind = "workflow"
resource = "source"

[[agent.nodes]]
uses = "noop"

[[agent.populations]]
name = "dispatch"
uses = ["poll"]
poll_every = "1m"

[agent.populations.query]
scope = "all"
`)
	writeDefinition(t, dir, "noop", `[noop]
kind = "effect"
scope = "session"

[noop.setup]
type = "exec"
command = "printf"
args = ["{}"]

[noop.health.alive]
type = "noop"
`)
	cfg := &config.Config{BaseDir: dir}
	definitions, err := Load(cfg)
	if err != nil {
		t.Fatal(err)
	}
	definition := definitions[0]
	store := state.NewStore(t.TempDir())
	logStore := eventlog.NewStore(store.Dir())
	engine := NewEngine(definition, store, logStore, admitstatus.NewCache(logStore), serviceHooks(func() *config.Config { return cfg }, store, definition, nil))

	items, err := actionRunner{cfg: cfg}.Poll(context.Background(), definition)
	if err != nil {
		t.Fatalf("poll: %v", err)
	}
	for _, item := range items {
		if err := engine.ApplyAppearance(context.Background(), item); err != nil {
			t.Fatalf("admit %v: %v", item, err)
		}
	}

	raw, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Split(strings.TrimSpace(string(raw)), "\n")
	want := []string{"poll scope=all", "subscribe scope=absent"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("action log = %q, want the query to see its own inputs and subscribe only the empty action object %q", got, want)
	}
}
