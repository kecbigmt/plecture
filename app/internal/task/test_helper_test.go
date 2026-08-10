package task

import (
	"testing"

	"github.com/kecbigmt/sennit/app/internal/config"
)

// taskStub is a terse spec for one task definition used by buildPlan.
// Tests should keep it minimal — only the fields they care about. Scope
// defaults to "run" so most callers can omit it.
type taskStub struct {
	id                string
	scope             string
	setup             string
	cleanup           string
	healthcheck       string
	attach            string
	capture           string
	primary           bool
	idleAfter         config.Duration
	outputsSchema     map[string]any
	outputsSchemaFile string
	inputsSchema      map[string]any
	inputsSchemaFile  string
	baseDir           string
	execution         string
}

// nodeStub mirrors config.WorkflowNode but uses positional convenience so
// tests read like "build a workflow with these nodes" rather than threading
// `uses` defaults by hand. Empty `uses` defaults to `id`.
type nodeStub struct {
	id     string
	uses   string
	inputs map[string]string
}

// buildPlan compiles a workflow with the given nodes against in-memory task
// definitions and asserts the compile succeeded. Replaces the old
// `Validate([]TaskConfig)` ergonomic for tests that exercise the planner
// directly. `nodes` is the per-workflow node list; each taskStub yields one
// definition (id is shared with the matching node when `uses` is omitted).
func buildPlan(t *testing.T, defs []taskStub, nodes []nodeStub) *Plan {
	t.Helper()
	plan, err := tryBuildPlan(defs, nodes)
	if err != nil {
		t.Fatalf("buildPlan: %v", err)
	}
	return plan
}

func tryBuildPlan(defs []taskStub, nodes []nodeStub) (*Plan, error) {
	return tryBuildPlanWithEnvironment(defs, nodes, "")
}

// buildPlanWithEnvironment is buildPlan with the workflow's `environment`
// field set, for tests exercising execution-plane resolution
// (ResolveExecution) against a non-host environment.
func buildPlanWithEnvironment(t *testing.T, defs []taskStub, nodes []nodeStub, environment string) *Plan {
	t.Helper()
	plan, err := tryBuildPlanWithEnvironment(defs, nodes, environment)
	if err != nil {
		t.Fatalf("buildPlanWithEnvironment: %v", err)
	}
	return plan
}

func tryBuildPlanWithEnvironment(defs []taskStub, nodes []nodeStub, environment string) (*Plan, error) {
	defMap := make(map[string]config.TaskDefinition, len(defs))
	for _, s := range defs {
		defMap[s.id] = config.TaskDefinition{
			ID:                s.id,
			Scope:             s.scope,
			Setup:             s.setup,
			Cleanup:           s.cleanup,
			Healthcheck:       s.healthcheck,
			Primary:           s.primary,
			Attach:            s.attach,
			Capture:           s.capture,
			IdleAfter:         s.idleAfter,
			OutputsSchema:     s.outputsSchema,
			OutputsSchemaFile: s.outputsSchemaFile,
			InputsSchema:      s.inputsSchema,
			InputsSchemaFile:  s.inputsSchemaFile,
			BaseDir:           s.baseDir,
			Execution:         s.execution,
		}
	}
	wfNodes := make([]config.WorkflowNode, 0, len(nodes))
	for _, n := range nodes {
		wfNodes = append(wfNodes, config.WorkflowNode{ID: n.id, Uses: n.uses, Inputs: n.inputs})
	}
	wf := config.WorkflowFile{ID: "test", Nodes: wfNodes, Environment: environment}
	return CompileWorkflow(wf, defMap)
}
