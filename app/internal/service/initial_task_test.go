package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/eventlog"
	"github.com/kecbigmt/plecture/app/internal/state"
	"github.com/kecbigmt/plecture/contracts/event"
	contract "github.com/kecbigmt/plecture/contracts/state"
)

const ticketResourceDoc = `[ticket]
kind  = "resource"
match = '^ticket://(?P<n>[0-9]+)$'
name  = { expr = "'ticket-' + match.n" }

[ticket.observe]
type   = "shell"
script = "echo '{}'"
`

const triageTaskDoc = `[triage]
kind         = "task"
description  = "Triage a case"
resource     = "case"
instructions = [{ text = "Triage {{ resource.id }}." }]
`

const ticketWorkTaskDoc = `[ticket_work]
kind         = "task"
description  = "Work a ticket"
resource     = "ticket"
instructions = [{ text = "Work {{ resource.id }}." }]
`

// initialTaskFixture declares a `case` entry workflow, a second resource type
// `ticket`, and one task document written for each.
func initialTaskFixture(t *testing.T) *config.Config {
	t.Helper()
	cfg := identityFixture(t)
	writeResourceDoc(t, cfg, "ticket", ticketResourceDoc)
	writeServiceTaskDoc(t, cfg, "triage", triageTaskDoc)
	writeServiceTaskDoc(t, cfg, "ticket_work", ticketWorkTaskDoc)
	return cfg
}

func writeServiceTaskDoc(t *testing.T, cfg *config.Config, id, doc string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(cfg.BaseDir, "tasks", id+".toml"), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
}

func instructionEvents(t *testing.T, store *state.Store, session string) []event.Event {
	t.Helper()
	evs, _, _, err := eventlog.NewStore(store.Dir()).List(session, 0, event.Filter{Types: []string{event.TypeInstruction}})
	if err != nil {
		t.Fatalf("list instructions: %v", err)
	}
	return evs
}

func requireErrorCode(t *testing.T, err error, code string) {
	t.Helper()
	if got := errorCode(err); got != code {
		t.Fatalf("err = %v (code %q), want code %q", err, got, code)
	}
}

func TestUp_SelectedTaskBecomesTheInitialInstanceBoundToTheEntry(t *testing.T) {
	store := testStore(t)
	cfg := initialTaskFixture(t)

	res, err := Up(cfg, store, UpParams{Identifier: identityCaseID, InitialTask: &InitialTask{TaskID: "triage"}})
	if err != nil {
		t.Fatalf("up: %v", err)
	}
	initial := store.Get(res.SessionName).Tasks["initial"]
	if initial == nil || initial.TaskID != "triage" || initial.Resource != identityCaseID || initial.Status != contract.TaskStatusProduced {
		t.Fatalf("initial = %+v, want a produced triage instance bound to %s", initial, identityCaseID)
	}
	if evs := instructionEvents(t, store, res.SessionName); len(evs) != 1 || !strings.Contains(evs[0].Body, "Triage "+identityCaseID) {
		t.Errorf("instructions = %+v, want the one rendered triage instruction", evs)
	}
}

func TestUp_WithoutATaskCreatesAnEnvironmentWithNoInstance(t *testing.T) {
	store := testStore(t)
	cfg := initialTaskFixture(t)

	res, err := Up(cfg, store, UpParams{Identifier: identityCaseID})
	if err != nil {
		t.Fatalf("up: %v", err)
	}
	if tasks := store.Get(res.SessionName).Tasks; len(tasks) != 0 {
		t.Errorf("tasks = %v, want none", tasks)
	}
	if evs := instructionEvents(t, store, res.SessionName); len(evs) != 0 {
		t.Errorf("instructions = %+v, want none", evs)
	}
}

func TestUp_RejectsAnInitialTaskSelectionBeforeCreatingAnything(t *testing.T) {
	cases := []struct {
		name string
		sel  InitialTask
		want string
	}{
		{"task written for another resource type", InitialTask{TaskID: "ticket_work"}, "ticket"},
		{"binding the task type does not claim", InitialTask{TaskID: "triage", Resource: "ticket://7"}, "ticket://7"},
		{"unknown task", InitialTask{TaskID: "nope"}, "nope"},
		{"effect instead of a task document", InitialTask{TaskID: "noop"}, "noop"},
		{"binding without a task", InitialTask{Resource: "ticket://7"}, "task"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := testStore(t)
			cfg := initialTaskFixture(t)
			sel := tc.sel
			_, err := Up(cfg, store, UpParams{Identifier: identityCaseID, InitialTask: &sel})
			requireErrorCode(t, err, ErrInvalidInput)
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want it to name %q", err, tc.want)
			}
			if all := store.All(); len(all) != 0 {
				t.Errorf("a rejected selection created sessions: %v", all)
			}
		})
	}
}

func TestUp_BindsATaskOfAnotherTypeToTheNamedResource(t *testing.T) {
	store := testStore(t)
	cfg := initialTaskFixture(t)

	res, err := Up(cfg, store, UpParams{Identifier: identityCaseID, InitialTask: &InitialTask{TaskID: "ticket_work", Resource: "ticket://7"}})
	if err != nil {
		t.Fatalf("up: %v", err)
	}
	s := store.Get(res.SessionName)
	if s.ResourceID != identityCaseID {
		t.Errorf("entry resource = %q, want %s unchanged by the task binding", s.ResourceID, identityCaseID)
	}
	initial := s.Tasks["initial"]
	if initial == nil || initial.TaskID != "ticket_work" || initial.Resource != "ticket://7" {
		t.Fatalf("initial = %+v, want ticket_work bound to ticket://7", initial)
	}
}

func TestUp_RepeatedSelectionConvergesAndAConflictingOneIsRefused(t *testing.T) {
	store := testStore(t)
	cfg := initialTaskFixture(t)

	first, err := Up(cfg, store, UpParams{Identifier: identityCaseID, InitialTask: &InitialTask{TaskID: "triage"}})
	if err != nil {
		t.Fatalf("first up: %v", err)
	}
	if _, err := Up(cfg, store, UpParams{Identifier: identityCaseID, InitialTask: &InitialTask{TaskID: "triage"}}); err != nil {
		t.Fatalf("repeated up: %v", err)
	}
	if evs := instructionEvents(t, store, first.SessionName); len(evs) != 1 {
		t.Errorf("instructions = %d after a repeated selection, want 1", len(evs))
	}

	_, err = Up(cfg, store, UpParams{Identifier: identityCaseID, InitialTask: &InitialTask{TaskID: "ticket_work", Resource: "ticket://7"}})
	requireErrorCode(t, err, ErrInvalidInput)
	if !strings.Contains(err.Error(), "plect task cleanup initial") {
		t.Errorf("err = %v, want it to name the cleanup that frees the slot", err)
	}
	if got := store.Get(first.SessionName).Tasks["initial"]; got == nil || got.TaskID != "triage" {
		t.Errorf("initial = %+v after a refused selection, want the triage instance kept", got)
	}
}

func TestUp_RejectsAnInitialTaskWithABareSessionName(t *testing.T) {
	store := testStore(t)
	cfg := initialTaskFixture(t)
	res, err := Up(cfg, store, UpParams{Identifier: identityCaseID})
	if err != nil {
		t.Fatalf("up: %v", err)
	}

	_, err = Up(cfg, store, UpParams{Identifier: res.SessionName, InitialTask: &InitialTask{TaskID: "triage"}})
	requireErrorCode(t, err, ErrInvalidInput)
	if tasks := store.Get(res.SessionName).Tasks; len(tasks) != 0 {
		t.Errorf("tasks = %v, want none", tasks)
	}
}

const escalatingCaseResourceDoc = `[case]
kind  = "resource"
match = '^case://(?P<n>[0-9]+)$'
name  = { expr = "'case-' + match.n" }

[case.observe]
type   = "shell"
script = "echo '{\"ticket_url\":\"ticket://7\"}'"

[case.state_schema]
type = "object"

[case.state_schema.properties]
ticket_url = { type = "string" }
`

const escalateTaskDoc = `[escalate]
kind         = "task"
description  = "Escalate a case to a ticket"
resource     = "case"
instructions = [{ text = "Escalate {{ resource.id }}." }]

[[escalate.chains]]
id       = "ticket"
workflow = "ticket_flow"
resource = { from = "resource.state.ticket_url" }
task     = "ticket_work"

[escalate.chains.when]
all = [{ check = "resource.state.ticket_url", in = ["ticket://7"] }]
`

func TestTick_ChainSpawnStartsWithItsSelectedTaskBoundToTheSpawnedEntry(t *testing.T) {
	store := testStore(t)
	cfg := initialTaskFixture(t)
	writeResourceDoc(t, cfg, "case", escalatingCaseResourceDoc)
	addFixtureWorkflow(t, cfg, "ticket_flow", "ticket", "noop", "")
	writeServiceTaskDoc(t, cfg, "escalate", escalateTaskDoc)

	work, err := Up(cfg, store, UpParams{Identifier: identityCaseID, InitialTask: &InitialTask{TaskID: "escalate"}})
	if err != nil {
		t.Fatalf("up: %v", err)
	}
	res, err := TickSession(cfg, store, TickParams{SessionName: work.SessionName, SkipRefresh: true})
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	var spawn *ChainSpawn
	for i := range res.Chains {
		if res.Chains[i].ChainID == "ticket" {
			spawn = &res.Chains[i]
		}
	}
	if spawn == nil || !spawn.Spawned || spawn.InitialTask != "ticket_work" {
		t.Fatalf("chain = %+v, want a spawn that starts with ticket_work", res.Chains)
	}
	spawned := store.Get(spawn.TargetSession)
	if spawned == nil || spawned.ResourceID != "ticket://7" {
		t.Fatalf("spawned = %+v, want a session for ticket://7", spawned)
	}
	initial := spawned.Tasks["initial"]
	if initial == nil || initial.TaskID != "ticket_work" || initial.Resource != "ticket://7" {
		t.Errorf("spawned initial = %+v, want ticket_work bound to ticket://7", initial)
	}
	if _, ok := spawned.Inputs["task"]; ok {
		t.Errorf("spawned inputs = %v, want the selection kept out of them", spawned.Inputs)
	}
}

func TestLoad_ChainTaskForAnotherResourceTypeIsRefused(t *testing.T) {
	cfg := initialTaskFixture(t)
	writeResourceDoc(t, cfg, "case", escalatingCaseResourceDoc)
	addFixtureWorkflow(t, cfg, "ticket_flow", "ticket", "noop", "")
	writeServiceTaskDoc(t, cfg, "escalate", strings.Replace(escalateTaskDoc, `task     = "ticket_work"`, `task     = "triage"`, 1))

	store := testStore(t)
	_, err := Up(cfg, store, UpParams{Identifier: identityCaseID, InitialTask: &InitialTask{TaskID: "escalate"}})
	if err == nil || !strings.Contains(err.Error(), "PLECTURE-CFG-RESOURCE-OBSERVER-MISMATCH") {
		t.Fatalf("err = %v, want the chain's task mismatch reported at load", err)
	}
}

func TestUp_RepeatedSelectionResolvesAgainstTheSessionsRevisionNotTodaysConfig(t *testing.T) {
	store := testStore(t)
	cfg := initialTaskFixture(t)
	if _, err := Up(cfg, store, UpParams{Identifier: identityCaseID, InitialTask: &InitialTask{TaskID: "triage"}}); err != nil {
		t.Fatalf("first up: %v", err)
	}
	if err := os.Remove(filepath.Join(cfg.BaseDir, "tasks", "triage.toml")); err != nil {
		t.Fatal(err)
	}

	if _, err := Up(cfg, store, UpParams{Identifier: identityCaseID, InitialTask: &InitialTask{TaskID: "triage"}}); err != nil {
		t.Fatalf("repeated up after the document left today's config: %v", err)
	}
}
