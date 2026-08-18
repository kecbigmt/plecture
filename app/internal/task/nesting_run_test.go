package task

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/kecbigmt/plecture/app/internal/config"
	contract "github.com/kecbigmt/plecture/contracts/state"
)

// scriptedExecutor answers each request from a queue keyed by the rendered
// command, so a multi-layer run can hand every layer its own stdout while the
// recorded requests still show the order the layers ran in.
type scriptedExecutor struct {
	requests []ExecRequest
	// stdout maps a rendered command to the stdout that command produces.
	// A command with no entry produces "{}".
	stdout map[string]string
	// failOn makes the named rendered command exit non-zero.
	failOn string
}

func (s *scriptedExecutor) Run(_ context.Context, req ExecRequest) ([]byte, []byte, error) {
	s.requests = append(s.requests, req)
	cmd := req.Argv[len(req.Argv)-1]
	if s.failOn != "" && cmd == s.failOn {
		return nil, []byte("boom"), errNestedTestFailure
	}
	if out, ok := s.stdout[cmd]; ok {
		return []byte(out), nil, nil
	}
	return []byte("{}"), nil, nil
}

type nestedTestFailure struct{}

func (nestedTestFailure) Error() string { return "exit status 1" }

var errNestedTestFailure = nestedTestFailure{}

func withScriptedExecutor(t *testing.T, e *scriptedExecutor) *scriptedExecutor {
	t.Helper()
	orig := defaultExecutor
	defaultExecutor = e
	t.Cleanup(func() { defaultExecutor = orig })
	return e
}

func (s *scriptedExecutor) commands() []string {
	out := make([]string, len(s.requests))
	for i, r := range s.requests {
		out[i] = r.Argv[len(r.Argv)-1]
	}
	return out
}

// nestedPlan compiles a one-node plan whose task is the outermost of the
// given chain. defs are ordered outermost-first; each is linked to the next
// by `inner`, and the chain is stamped the way config load stamps it.
func nestedPlan(t *testing.T, defs ...config.TaskDefinition) []Resolved {
	t.Helper()
	for i := range defs[:len(defs)-1] {
		defs[i].Inner = defs[i+1].ID
	}
	outer := defs[0]
	outer.InnerChain = defs[1:]
	r, err := ResolveDefinition(outer, outer.ID)
	if err != nil {
		t.Fatalf("ResolveDefinition: %v", err)
	}
	return []Resolved{r}
}

func objectSchema(props map[string]any, required ...string) map[string]any {
	schema := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		req := make([]any, len(required))
		for i, r := range required {
			req[i] = r
		}
		schema["required"] = req
	}
	return schema
}

// TestRunSetup_NestedChainRunsOutsideIn covers the LIFO stack's setup half:
// each layer's setup runs before the layer it wraps, and each layer's own
// emission lands in its own slot — locals for an outer layer, outputs for the
// innermost.
func TestRunSetup_NestedChainRunsOutsideIn(t *testing.T) {
	exec := withScriptedExecutor(t, &scriptedExecutor{stdout: map[string]string{
		"outer-setup":  `{"guard_dir":"/tmp/guard"}`,
		"middle-setup": `{"socket":"/tmp/sock"}`,
		"inner-setup":  `{"pid":"42"}`,
	}})
	ordered := nestedPlan(t,
		config.TaskDefinition{ID: "outer", Scope: "run", Setup: "outer-setup"},
		config.TaskDefinition{ID: "middle", Scope: "run", Setup: "middle-setup"},
		config.TaskDefinition{ID: "inner", Scope: "run", Setup: "inner-setup"},
	)
	tasks := map[string]*contract.TaskState{}
	if err := RunSetup(context.Background(), ordered, SessionVars{Name: "s"}, tasks, nil); err != nil {
		t.Fatalf("RunSetup: %v", err)
	}
	want := []string{"outer-setup", "middle-setup", "inner-setup"}
	if got := exec.commands(); !reflect.DeepEqual(got, want) {
		t.Fatalf("setup order = %v, want %v", got, want)
	}
	st := tasks["outer"]
	if st == nil || st.Status != contract.TaskStatusProduced {
		t.Fatalf("state = %+v, want a produced task", st)
	}
	if len(st.Layers) != 3 {
		t.Fatalf("Layers = %+v, want three", st.Layers)
	}
	if got := st.Layers[0].Locals["guard_dir"]; got != "/tmp/guard" {
		t.Errorf("outer locals = %v, want the outer setup's emission", st.Layers[0].Locals)
	}
	if got := st.Layers[1].Locals["socket"]; got != "/tmp/sock" {
		t.Errorf("middle locals = %v, want the middle setup's emission", st.Layers[1].Locals)
	}
	if got := st.Layers[2].Outputs["pid"]; got != "42" {
		t.Errorf("innermost outputs = %v, want the inner setup's emission", st.Layers[2].Outputs)
	}
	if st.Layers[2].Locals != nil {
		t.Errorf("innermost locals = %v, want none — the innermost layer is not a joint", st.Layers[2].Locals)
	}
}

// TestRunSetup_NestedBindInputsRenderInnerInputObject covers the joint's
// input half: the inner task's inputs come from the outer layer's
// bind.inputs, rendered against that layer's own inputs and locals.
func TestRunSetup_NestedBindInputsRenderInnerInputObject(t *testing.T) {
	withScriptedExecutor(t, &scriptedExecutor{stdout: map[string]string{
		"outer-setup": `{"guard_dir":"/tmp/guard"}`,
	}})
	outer := config.TaskDefinition{
		ID: "outer", Scope: "run", Setup: "outer-setup",
		Bind: &config.BindConfig{Inputs: map[string]string{
			"tmux_session": "{{.Inputs.tmux_session}}",
			"path_prepend": "{{.Locals.guard_dir}}",
		}},
	}
	inner := config.TaskDefinition{
		ID: "inner", Scope: "run", Setup: "inner-setup",
		InputsSchema: objectSchema(map[string]any{
			"tmux_session": map[string]any{"type": "string"},
			"path_prepend": map[string]any{"type": "string"},
		}, "tmux_session", "path_prepend"),
	}
	ordered := nestedPlan(t, outer, inner)
	ordered[0].Inputs = map[string]string{"tmux_session": "sess-1"}
	tasks := map[string]*contract.TaskState{}
	if err := RunSetup(context.Background(), ordered, SessionVars{Name: "s"}, tasks, nil); err != nil {
		t.Fatalf("RunSetup: %v", err)
	}
	got := tasks["outer"].Layers[1].Inputs
	want := map[string]any{"tmux_session": "sess-1", "path_prepend": "/tmp/guard"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("inner inputs = %v, want %v", got, want)
	}
}

// TestRunSetup_NestedBoundInputsValidatedAgainstInnerSchema covers the
// runtime failure rule for bound inputs: the inner task's own schema is what
// they answer to, and a violation stops the chain before the inner setup runs.
func TestRunSetup_NestedBoundInputsValidatedAgainstInnerSchema(t *testing.T) {
	exec := withScriptedExecutor(t, &scriptedExecutor{})
	outer := config.TaskDefinition{
		ID: "outer", Scope: "run", Setup: "outer-setup",
		Bind: &config.BindConfig{Inputs: map[string]string{"model": "opus"}},
	}
	inner := config.TaskDefinition{
		ID: "inner", Scope: "run", Setup: "inner-setup",
		InputsSchema: objectSchema(map[string]any{
			"model":        map[string]any{"type": "string"},
			"tmux_session": map[string]any{"type": "string"},
		}, "tmux_session"),
	}
	tasks := map[string]*contract.TaskState{}
	err := RunSetup(context.Background(), nestedPlan(t, outer, inner), SessionVars{Name: "s"}, tasks, nil)
	if err == nil {
		t.Fatal("RunSetup: want an error for bound inputs the inner schema rejects, got nil")
	}
	if !strings.Contains(err.Error(), "inner") {
		t.Errorf("error = %v, want it to name the inner layer", err)
	}
	for _, cmd := range exec.commands() {
		if cmd == "inner-setup" {
			t.Error("the inner setup ran despite its bound inputs failing validation")
		}
	}
}

// TestRunSetup_NestedBindEnvReachesInnerExecutionsOnly covers the joint's env
// half: an outer layer's bind.env is injected into the executions of the
// layers it wraps, and never into its own.
func TestRunSetup_NestedBindEnvReachesInnerExecutionsOnly(t *testing.T) {
	exec := withScriptedExecutor(t, &scriptedExecutor{stdout: map[string]string{
		"outer-setup": `{"guard_dir":"/tmp/guard"}`,
	}})
	outer := config.TaskDefinition{
		ID: "outer", Scope: "run", Setup: "outer-setup",
		Bind: &config.BindConfig{Env: map[string]string{
			"PLECT_TEAM_CONTEXT": "{{.SessionName}}",
			"PLECT_GUARD_DIR":    "{{.Locals.guard_dir}}",
		}},
	}
	inner := config.TaskDefinition{ID: "inner", Scope: "run", Setup: "inner-setup"}
	tasks := map[string]*contract.TaskState{}
	if err := RunSetup(context.Background(), nestedPlan(t, outer, inner), SessionVars{Name: "team/x"}, tasks, nil); err != nil {
		t.Fatalf("RunSetup: %v", err)
	}
	if len(exec.requests) != 2 {
		t.Fatalf("requests = %d, want one per layer", len(exec.requests))
	}
	if len(exec.requests[0].Env) != 0 {
		t.Errorf("outer setup env = %v, want none — a layer's own bind.env is not its own", exec.requests[0].Env)
	}
	want := []string{"PLECT_GUARD_DIR=/tmp/guard", "PLECT_TEAM_CONTEXT=team/x"}
	if !reflect.DeepEqual(exec.requests[1].Env, want) {
		t.Errorf("inner setup env = %v, want %v", exec.requests[1].Env, want)
	}
}

// TestRunSetup_NestedOuterLocalsValidatedAgainstLocalsSchema covers the
// runtime failure rule for locals: the outer setup answers to its own
// locals_schema exactly as a plain task's setup answers to outputs_schema.
func TestRunSetup_NestedOuterLocalsValidatedAgainstLocalsSchema(t *testing.T) {
	withScriptedExecutor(t, &scriptedExecutor{stdout: map[string]string{
		"outer-setup": `{"unexpected":"value"}`,
	}})
	outer := config.TaskDefinition{
		ID: "outer", Scope: "run", Setup: "outer-setup",
		LocalsSchema: objectSchema(map[string]any{
			"guard_dir": map[string]any{"type": "string"},
		}, "guard_dir"),
	}
	inner := config.TaskDefinition{ID: "inner", Scope: "run", Setup: "inner-setup"}
	tasks := map[string]*contract.TaskState{}
	err := RunSetup(context.Background(), nestedPlan(t, outer, inner), SessionVars{Name: "s"}, tasks, nil)
	if err == nil {
		t.Fatal("RunSetup: want an error for locals the locals schema rejects, got nil")
	}
	if !strings.Contains(err.Error(), "locals") {
		t.Errorf("error = %v, want it to name the locals contract", err)
	}
}

// TestRunSetup_NestedBindingTemplateRenderFailure covers the runtime failure
// rule for an unrenderable binding template.
func TestRunSetup_NestedBindingTemplateRenderFailure(t *testing.T) {
	withScriptedExecutor(t, &scriptedExecutor{})
	outer := config.TaskDefinition{
		ID: "outer", Scope: "run", Setup: "outer-setup",
		Bind: &config.BindConfig{Inputs: map[string]string{"model": "{{.Locals.absent}}"}},
	}
	inner := config.TaskDefinition{ID: "inner", Scope: "run", Setup: "inner-setup"}
	tasks := map[string]*contract.TaskState{}
	err := RunSetup(context.Background(), nestedPlan(t, outer, inner), SessionVars{Name: "s"}, tasks, nil)
	if err == nil {
		t.Fatal("RunSetup: want an error for a binding template that cannot render, got nil")
	}
	if !strings.Contains(err.Error(), "bind.inputs") {
		t.Errorf("error = %v, want it to name the failing binding table", err)
	}
}

// TestRunCleanup_NestedUnwindsInsideOut covers the LIFO stack's cleanup half,
// including each layer reading its own locals.
func TestRunCleanup_NestedUnwindsInsideOut(t *testing.T) {
	exec := withScriptedExecutor(t, &scriptedExecutor{stdout: map[string]string{
		"outer-setup": `{"guard_dir":"/tmp/guard"}`,
	}})
	outer := config.TaskDefinition{
		ID: "outer", Scope: "run", Setup: "outer-setup", Cleanup: "rm -rf {{.Locals.guard_dir}}",
		Bind: &config.BindConfig{Env: map[string]string{"PLECT_GUARD": "on"}},
	}
	inner := config.TaskDefinition{ID: "inner", Scope: "run", Setup: "inner-setup", Cleanup: "inner-cleanup"}
	ordered := nestedPlan(t, outer, inner)
	tasks := map[string]*contract.TaskState{}
	if err := RunSetup(context.Background(), ordered, SessionVars{Name: "s"}, tasks, nil); err != nil {
		t.Fatalf("RunSetup: %v", err)
	}
	exec.requests = nil
	if err := RunCleanup(context.Background(), ordered, SessionVars{Name: "s"}, tasks, nil); err != nil {
		t.Fatalf("RunCleanup: %v", err)
	}
	want := []string{"inner-cleanup", "rm -rf /tmp/guard"}
	if got := exec.commands(); !reflect.DeepEqual(got, want) {
		t.Fatalf("cleanup order = %v, want %v", got, want)
	}
	if len(exec.requests[0].Env) != 1 || exec.requests[0].Env[0] != "PLECT_GUARD=on" {
		t.Errorf("inner cleanup env = %v, want the outer layer's bind.env", exec.requests[0].Env)
	}
	if len(exec.requests[1].Env) != 0 {
		t.Errorf("outer cleanup env = %v, want none", exec.requests[1].Env)
	}
	if st := tasks["outer"]; st.Status != contract.TaskStatusCleaned {
		t.Errorf("status = %q, want %q", st.Status, contract.TaskStatusCleaned)
	}
	for i, layer := range tasks["outer"].Layers {
		if layer.Status != contract.TaskStatusCleaned {
			t.Errorf("layer[%d] status = %q, want %q", i, layer.Status, contract.TaskStatusCleaned)
		}
	}
}

// TestRunCleanup_NestedSkipsLayersThatNeverSetUp covers the partial-failure
// unwind: an inner setup that fails after an outer setup succeeded leaves the
// produced outer layer to clean and nothing else.
func TestRunCleanup_NestedSkipsLayersThatNeverSetUp(t *testing.T) {
	exec := withScriptedExecutor(t, &scriptedExecutor{
		stdout: map[string]string{"outer-setup": `{"guard_dir":"/tmp/guard"}`},
		failOn: "inner-setup",
	})
	outer := config.TaskDefinition{ID: "outer", Scope: "run", Setup: "outer-setup", Cleanup: "outer-cleanup"}
	inner := config.TaskDefinition{ID: "inner", Scope: "run", Setup: "inner-setup", Cleanup: "inner-cleanup"}
	ordered := nestedPlan(t, outer, inner)
	tasks := map[string]*contract.TaskState{}
	if err := RunSetup(context.Background(), ordered, SessionVars{Name: "s"}, tasks, nil); err == nil {
		t.Fatal("RunSetup: want an error when the inner setup fails, got nil")
	}
	exec.requests = nil
	if err := RunCleanup(context.Background(), ordered, SessionVars{Name: "s"}, tasks, nil); err != nil {
		t.Fatalf("RunCleanup: %v", err)
	}
	want := []string{"outer-cleanup"}
	if got := exec.commands(); !reflect.DeepEqual(got, want) {
		t.Fatalf("cleanup ran %v, want only the layer that reached setup: %v", got, want)
	}
}

// TestRunCleanup_NestedOuterWithoutCleanupStillUnwindsInner guards the
// interaction with the plain-task shortcut for a cleanup-less task: the
// composed task's cleanup is the chain's, so an outer layer declaring none
// must not read as "the whole chain is already released".
func TestRunCleanup_NestedOuterWithoutCleanupStillUnwindsInner(t *testing.T) {
	exec := withScriptedExecutor(t, &scriptedExecutor{})
	outer := config.TaskDefinition{ID: "outer", Scope: "run", Setup: "outer-setup"}
	inner := config.TaskDefinition{ID: "inner", Scope: "run", Setup: "inner-setup", Cleanup: "inner-cleanup"}
	ordered := nestedPlan(t, outer, inner)
	tasks := map[string]*contract.TaskState{}
	if err := RunSetup(context.Background(), ordered, SessionVars{Name: "s"}, tasks, nil); err != nil {
		t.Fatalf("RunSetup: %v", err)
	}
	exec.requests = nil
	if err := RunCleanup(context.Background(), ordered, SessionVars{Name: "s"}, tasks, nil); err != nil {
		t.Fatalf("RunCleanup: %v", err)
	}
	if got := exec.commands(); !reflect.DeepEqual(got, []string{"inner-cleanup"}) {
		t.Fatalf("cleanup ran %v, want the inner layer's cleanup", got)
	}
}

// TestExecuteTaskSetup_NestedRunsTheWholeChain covers the dynamic
// instantiation path: `plect task setup` on a nested task runs every layer,
// so the two setup entry points agree on what instantiating one task means.
func TestExecuteTaskSetup_NestedRunsTheWholeChain(t *testing.T) {
	exec := withScriptedExecutor(t, &scriptedExecutor{stdout: map[string]string{
		"outer-setup": `{"guard_dir":"/tmp/guard"}`,
		"inner-setup": `{"pid":"7"}`,
	}})
	ordered := nestedPlan(t,
		config.TaskDefinition{ID: "outer", Scope: "run", Setup: "outer-setup"},
		config.TaskDefinition{ID: "inner", Scope: "run", Setup: "inner-setup"},
	)
	result, err := ExecuteTaskSetup(context.Background(), ordered[0], nil, SessionVars{Name: "s"}, nil)
	if err != nil {
		t.Fatalf("ExecuteTaskSetup: %v", err)
	}
	if got := exec.commands(); !reflect.DeepEqual(got, []string{"outer-setup", "inner-setup"}) {
		t.Fatalf("setup order = %v, want the whole chain outside-in", got)
	}
	if len(result.Layers) != 2 || result.Layers[1].Outputs["pid"] != "7" {
		t.Errorf("Layers = %+v, want a record per layer with the innermost's outputs", result.Layers)
	}
}

// TestExecuteTaskSetup_NestedFailureKeepsProducedLayers covers the
// partial-failure contract of the dynamic path: the caller persists what
// produced so the next cleanup can unwind it.
func TestExecuteTaskSetup_NestedFailureKeepsProducedLayers(t *testing.T) {
	withScriptedExecutor(t, &scriptedExecutor{
		stdout: map[string]string{"outer-setup": `{"guard_dir":"/tmp/guard"}`},
		failOn: "inner-setup",
	})
	ordered := nestedPlan(t,
		config.TaskDefinition{ID: "outer", Scope: "run", Setup: "outer-setup"},
		config.TaskDefinition{ID: "inner", Scope: "run", Setup: "inner-setup"},
	)
	result, err := ExecuteTaskSetup(context.Background(), ordered[0], nil, SessionVars{Name: "s"}, nil)
	if err == nil {
		t.Fatal("ExecuteTaskSetup: want an error when the inner setup fails, got nil")
	}
	if len(result.Layers) != 2 {
		t.Fatalf("Layers = %+v, want a record for the produced layer and the failed one", result.Layers)
	}
	if result.Layers[0].Status != contract.TaskStatusProduced {
		t.Errorf("outer layer status = %q, want %q", result.Layers[0].Status, contract.TaskStatusProduced)
	}
	if result.Layers[1].Status != contract.TaskStatusFailed {
		t.Errorf("inner layer status = %q, want %q", result.Layers[1].Status, contract.TaskStatusFailed)
	}
}

// TestCompileWorkflow_NestedNodeCarriesItsLayers covers the workflow-facing
// half of "from the outside a nested task is exactly a task": a node names
// one and the compiled plan carries the whole chain, so `plect up` runs every
// layer rather than the outermost script alone.
func TestCompileWorkflow_NestedNodeCarriesItsLayers(t *testing.T) {
	inner := config.TaskDefinition{ID: "claude", Scope: "run", Setup: "inner-setup"}
	outer := config.TaskDefinition{ID: "team_claude", Scope: "run", Setup: "outer-setup", Inner: "claude", InnerChain: []config.TaskDefinition{inner}}
	plan, err := CompileWorkflow(
		config.WorkflowFile{ID: "test", Nodes: []config.WorkflowNode{{ID: "runtime", Uses: "team_claude"}}},
		map[string]config.TaskDefinition{"team_claude": outer, "claude": inner},
	)
	if err != nil {
		t.Fatalf("CompileWorkflow: %v", err)
	}
	if len(plan.Run) != 1 {
		t.Fatalf("plan.Run = %+v, want one node", plan.Run)
	}
	got := make([]string, 0, len(plan.Run[0].Layers))
	for _, l := range plan.Run[0].Layers {
		got = append(got, l.TaskID)
	}
	if !reflect.DeepEqual(got, []string{"team_claude", "claude"}) {
		t.Errorf("node layers = %v, want the whole chain outermost first", got)
	}
}
