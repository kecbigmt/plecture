package task

import (
	"context"
	"reflect"
	"testing"

	"github.com/kecbigmt/plect/app/internal/config"
	contract "github.com/kecbigmt/plect/contracts/state"
)

// TestHostEnvironment_ExplicitMatchesUnspecified is the golden test for the
// invariant that a workflow explicitly declaring `environment = "host"`
// must behave identically to one that omits Environment entirely — same
// compiled Plan, same stdout JSON, same resulting TaskState. Nothing in
// CompileWorkflow or RunSetup reads WorkflowFile.Environment (wiring a
// non-host Environment into the Executor is a later PR), so this test locks
// that invariant in rather than asserting it once by inspection.
func TestHostEnvironment_ExplicitMatchesUnspecified(t *testing.T) {
	defs := map[string]config.TaskDefinition{
		"a": {ID: "a", Scope: "run", Setup: `echo '{"value":"x"}'`},
	}
	nodes := []config.WorkflowNode{{ID: "a"}}

	unspecified := config.WorkflowFile{ID: "test", Nodes: nodes}
	explicitHost := config.WorkflowFile{ID: "test", Environment: "host", Nodes: nodes}

	planUnspecified, err := CompileWorkflow(unspecified, defs)
	if err != nil {
		t.Fatalf("compile unspecified: %v", err)
	}
	planExplicitHost, err := CompileWorkflow(explicitHost, defs)
	if err != nil {
		t.Fatalf("compile explicit host: %v", err)
	}
	if !reflect.DeepEqual(planUnspecified, planExplicitHost) {
		t.Fatalf("compiled plans differ:\nunspecified:   %+v\nexplicit host: %+v", planUnspecified, planExplicitHost)
	}

	session := SessionVars{Name: "x", WorkdirPath: t.TempDir()}
	tasksUnspecified := map[string]*contract.TaskState{}
	tasksExplicitHost := map[string]*contract.TaskState{}
	if err := RunSetup(context.Background(), planUnspecified.Run, session, tasksUnspecified, nil); err != nil {
		t.Fatalf("setup unspecified: %v", err)
	}
	if err := RunSetup(context.Background(), planExplicitHost.Run, session, tasksExplicitHost, nil); err != nil {
		t.Fatalf("setup explicit host: %v", err)
	}

	// SetupAt is wall-clock and expected to differ between the two runs;
	// zero it before comparing everything else byte-for-byte.
	for _, tasks := range []map[string]*contract.TaskState{tasksUnspecified, tasksExplicitHost} {
		for _, st := range tasks {
			st.SetupAt = contract.TaskState{}.SetupAt
		}
	}
	if !reflect.DeepEqual(tasksUnspecified, tasksExplicitHost) {
		t.Fatalf("resulting TaskState differs:\nunspecified:   %+v\nexplicit host: %+v", tasksUnspecified, tasksExplicitHost)
	}

	// Host degeneration: no new state node (e.g. a hypothetical "@environment"
	// pseudo-node) is ever introduced, regardless of the Environment value.
	if len(tasksUnspecified) != 1 || len(tasksExplicitHost) != 1 {
		t.Fatalf("expected exactly the 1 declared node, got unspecified=%d explicit_host=%d", len(tasksUnspecified), len(tasksExplicitHost))
	}
}
