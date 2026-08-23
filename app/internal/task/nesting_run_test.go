package task

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/effect"
	"github.com/kecbigmt/plecture/app/internal/lang"
	contract "github.com/kecbigmt/plecture/contracts/state"
)

// scriptedExecutor answers each request from a queue keyed by the rendered
// command, so a multi-layer run can hand every layer its own stdout while the
// recorded requests still show the order the layers ran in.
type scriptedExecutor struct {
	requests []effect.ExecRequest
	scripts  []string
	bindings []string
	// stdout maps a resolved script to the stdout it produces. A script with
	// no entry produces "{}".
	stdout map[string]string
	// failOn makes the named script exit non-zero.
	failOn string
}

func (s *scriptedExecutor) Run(_ context.Context, req effect.ExecRequest) ([]byte, []byte, error) {
	s.requests = append(s.requests, req)
	cmd, bindings := effect.ReadShellRun(req)
	s.scripts = append(s.scripts, cmd)
	s.bindings = append(s.bindings, bindings)
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
	restore := effect.UseExecutor(e)
	t.Cleanup(restore)
	return e
}

// commands reports what each recorded execution ran, read while its run
// directory still existed.
func (s *scriptedExecutor) commands() []string {
	return s.scripts
}

// reset forgets everything recorded so far, for a case that drives setup and
// then asserts only on what the cleanup ran.
func (s *scriptedExecutor) reset() {
	s.requests = nil
	s.scripts = nil
	s.bindings = nil
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
		config.TaskDefinition{ID: "outer", Scope: "run", Setup: shellStub("outer-setup")},
		config.TaskDefinition{ID: "middle", Scope: "run", Setup: shellStub("middle-setup")},
		config.TaskDefinition{ID: "inner", Scope: "run", Setup: shellStub("inner-setup")},
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
		ID: "outer", Scope: "run", Setup: shellStub("outer-setup"),
		InnerInputs: map[string]*lang.Value{
			"tmux_session": fromValue("inputs.tmux_session"),
			"path_prepend": fromValue("locals.guard_dir"),
		},
	}
	inner := config.TaskDefinition{
		ID: "inner", Scope: "run", Setup: shellStub("inner-setup"),
		InputsSchema: objectSchema(map[string]any{
			"tmux_session": map[string]any{"type": "string"},
			"path_prepend": map[string]any{"type": "string"},
		}, "tmux_session", "path_prepend"),
	}
	ordered := nestedPlan(t, outer, inner)
	ordered[0].Inputs = map[string]*lang.Value{"tmux_session": literalValue("sess-1")}
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
		ID: "outer", Scope: "run", Setup: shellStub("outer-setup"),
		InnerInputs: map[string]*lang.Value{"model": literalValue("opus")},
	}
	inner := config.TaskDefinition{
		ID: "inner", Scope: "run", Setup: shellStub("inner-setup"),
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
		ID: "outer", Scope: "run", Setup: shellStub("outer-setup"),
		InnerEnv: map[string]*lang.Value{
			"PLECT_TEAM_CONTEXT": fromValue("session.name"),
			"PLECT_GUARD_DIR":    fromValue("locals.guard_dir"),
		},
	}
	inner := config.TaskDefinition{ID: "inner", Scope: "run", Setup: shellStub("inner-setup")}
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
		ID: "outer", Scope: "run", Setup: shellStub("outer-setup"),
		LocalsSchema: objectSchema(map[string]any{
			"guard_dir": map[string]any{"type": "string"},
		}, "guard_dir"),
	}
	inner := config.TaskDefinition{ID: "inner", Scope: "run", Setup: shellStub("inner-setup")}
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
		ID: "outer", Scope: "run", Setup: shellStub("outer-setup"),
		InnerInputs: map[string]*lang.Value{"model": fromValue("locals.absent")},
	}
	inner := config.TaskDefinition{ID: "inner", Scope: "run", Setup: shellStub("inner-setup")}
	tasks := map[string]*contract.TaskState{}
	err := RunSetup(context.Background(), nestedPlan(t, outer, inner), SessionVars{Name: "s"}, tasks, nil)
	if err == nil {
		t.Fatal("RunSetup: want an error for a joint value that cannot resolve, got nil")
	}
	if !strings.Contains(err.Error(), "inner.inputs") {
		t.Errorf("error = %v, want it to name the failing binding table", err)
	}
}

// TestRunCleanup_NestedUnwindsInsideOut covers the LIFO stack's cleanup half,
// including each layer reading its own public contract — which is how a
// layer releases what its setup produced as a private local: by projecting
// it outward.
func TestRunCleanup_NestedUnwindsInsideOut(t *testing.T) {
	exec := withScriptedExecutor(t, &scriptedExecutor{stdout: map[string]string{
		"outer-setup": `{"guard_dir":"/tmp/guard"}`,
	}})
	outer := config.TaskDefinition{
		ID: "outer", Scope: "run", Setup: shellStub("outer-setup"), Cleanup: &lang.Action{
			Type:   lang.ActionShell,
			Script: "rm -rf $guard_dir",
			Bind:   map[string]*lang.Value{"guard_dir": fromValue("self.outputs.guard_dir")},
		},
		OutputsBind: map[string]*lang.Value{"guard_dir": fromValue("locals.guard_dir")},
		InnerEnv:    map[string]*lang.Value{"PLECT_GUARD": literalValue("on")},
	}
	inner := config.TaskDefinition{ID: "inner", Scope: "run", Setup: shellStub("inner-setup"), Cleanup: shellStub("inner-cleanup")}
	ordered := nestedPlan(t, outer, inner)
	tasks := map[string]*contract.TaskState{}
	if err := RunSetup(context.Background(), ordered, SessionVars{Name: "s"}, tasks, nil); err != nil {
		t.Fatalf("RunSetup: %v", err)
	}
	exec.reset()
	if err := RunCleanup(context.Background(), ordered, SessionVars{Name: "s"}, tasks, nil); err != nil {
		t.Fatalf("RunCleanup: %v", err)
	}
	want := []string{"inner-cleanup", "rm -rf $guard_dir"}
	if got := exec.commands(); !reflect.DeepEqual(got, want) {
		t.Fatalf("cleanup order = %v, want %v", got, want)
	}
	if !strings.Contains(exec.bindings[1], "/tmp/guard") {
		t.Errorf("outer cleanup bindings = %q, want the projected local", exec.bindings[1])
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
// unwind: a middle setup that fails after the outermost succeeded leaves the
// innermost layer — which the walk never reached — with nothing to release,
// while the two layers that did run are both offered their cleanup, the way a
// plain task whose setup failed still runs its own.
func TestRunCleanup_NestedSkipsLayersThatNeverSetUp(t *testing.T) {
	exec := withScriptedExecutor(t, &scriptedExecutor{
		stdout: map[string]string{"outer-setup": `{"guard_dir":"/tmp/guard"}`},
		failOn: "middle-setup",
	})
	outer := config.TaskDefinition{ID: "outer", Scope: "run", Setup: shellStub("outer-setup"), Cleanup: shellStub("outer-cleanup")}
	middle := config.TaskDefinition{ID: "middle", Scope: "run", Setup: shellStub("middle-setup"), Cleanup: shellStub("middle-cleanup")}
	inner := config.TaskDefinition{ID: "inner", Scope: "run", Setup: shellStub("inner-setup"), Cleanup: shellStub("inner-cleanup")}
	ordered := nestedPlan(t, outer, middle, inner)
	tasks := map[string]*contract.TaskState{}
	if err := RunSetup(context.Background(), ordered, SessionVars{Name: "s"}, tasks, nil); err == nil {
		t.Fatal("RunSetup: want an error when the middle setup fails, got nil")
	}
	exec.reset()
	if err := RunCleanup(context.Background(), ordered, SessionVars{Name: "s"}, tasks, nil); err != nil {
		t.Fatalf("RunCleanup: %v", err)
	}
	want := []string{"middle-cleanup", "outer-cleanup"}
	if got := exec.commands(); !reflect.DeepEqual(got, want) {
		t.Fatalf("cleanup ran %v, want the layers that reached setup: %v", got, want)
	}
}

// TestRunCleanup_NestedRetriesALayerWhoseCleanupFailed covers the retry a
// plain task already gets: a layer that failed to release is still owed one,
// and until it is released the composed task must not read as cleaned.
func TestRunCleanup_NestedRetriesALayerWhoseCleanupFailed(t *testing.T) {
	exec := withScriptedExecutor(t, &scriptedExecutor{failOn: "inner-cleanup"})
	outer := config.TaskDefinition{ID: "outer", Scope: "run", Setup: shellStub("outer-setup"), Cleanup: shellStub("outer-cleanup")}
	inner := config.TaskDefinition{ID: "inner", Scope: "run", Setup: shellStub("inner-setup"), Cleanup: shellStub("inner-cleanup")}
	ordered := nestedPlan(t, outer, inner)
	tasks := map[string]*contract.TaskState{}
	if err := RunSetup(context.Background(), ordered, SessionVars{Name: "s"}, tasks, nil); err != nil {
		t.Fatalf("RunSetup: %v", err)
	}
	if err := RunCleanup(context.Background(), ordered, SessionVars{Name: "s"}, tasks, nil); err == nil {
		t.Fatal("RunCleanup: want the first cleanup to fail")
	}
	if got := tasks["outer"].Status; got != contract.TaskStatusFailed {
		t.Fatalf("status after a failed release = %q, want %q", got, contract.TaskStatusFailed)
	}
	exec.failOn = ""
	exec.reset()
	if err := RunCleanup(context.Background(), ordered, SessionVars{Name: "s"}, tasks, nil); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if got := exec.commands(); !reflect.DeepEqual(got, []string{"inner-cleanup"}) {
		t.Fatalf("retry ran %v, want the layer whose cleanup failed", got)
	}
	if got := tasks["outer"].Status; got != contract.TaskStatusCleaned {
		t.Errorf("status after a successful retry = %q, want %q", got, contract.TaskStatusCleaned)
	}
}

// TestRunCleanup_NestedKeepsGoingPastAFailedLayer covers the unwind's
// resilience and its first-error contract: an inner layer that refuses to
// release must not strand the outer layers wrapping it.
func TestRunCleanup_NestedKeepsGoingPastAFailedLayer(t *testing.T) {
	exec := withScriptedExecutor(t, &scriptedExecutor{failOn: "inner-cleanup"})
	outer := config.TaskDefinition{ID: "outer", Scope: "run", Setup: shellStub("outer-setup"), Cleanup: shellStub("outer-cleanup")}
	inner := config.TaskDefinition{ID: "inner", Scope: "run", Setup: shellStub("inner-setup"), Cleanup: shellStub("inner-cleanup")}
	ordered := nestedPlan(t, outer, inner)
	tasks := map[string]*contract.TaskState{}
	if err := RunSetup(context.Background(), ordered, SessionVars{Name: "s"}, tasks, nil); err != nil {
		t.Fatalf("RunSetup: %v", err)
	}
	exec.reset()
	err := RunCleanup(context.Background(), ordered, SessionVars{Name: "s"}, tasks, nil)
	if err == nil {
		t.Fatal("RunCleanup: want the failing layer's error, got nil")
	}
	if !strings.Contains(err.Error(), "inner") {
		t.Errorf("error = %v, want it to name the layer that failed", err)
	}
	if got := exec.commands(); !reflect.DeepEqual(got, []string{"inner-cleanup", "outer-cleanup"}) {
		t.Fatalf("cleanup ran %v, want the outer layer released despite the inner failure", got)
	}
	layers := tasks["outer"].Layers
	if layers[0].Status != contract.TaskStatusCleaned || layers[1].Status != contract.TaskStatusFailed {
		t.Errorf("layer statuses = %q/%q, want cleaned/failed", layers[0].Status, layers[1].Status)
	}
}

// TestRunCleanup_NestedCleanupValueFailure covers the other failure branch of
// the unwind: a cleanup whose values cannot be resolved leaves that layer
// owing a release rather than silently counting as one.
func TestRunCleanup_NestedCleanupValueFailure(t *testing.T) {
	withScriptedExecutor(t, &scriptedExecutor{})
	outer := config.TaskDefinition{ID: "outer", Scope: "run", Setup: shellStub("outer-setup"), Cleanup: shellStub("outer-cleanup")}
	inner := config.TaskDefinition{ID: "inner", Scope: "run", Setup: shellStub("inner-setup"), Cleanup: &lang.Action{
		Type:   lang.ActionShell,
		Script: "inner-cleanup",
		Bind:   map[string]*lang.Value{"missing": fromValue("self.outputs.never_produced")},
	}}
	ordered := nestedPlan(t, outer, inner)
	tasks := map[string]*contract.TaskState{}
	if err := RunSetup(context.Background(), ordered, SessionVars{Name: "s"}, tasks, nil); err != nil {
		t.Fatalf("RunSetup: %v", err)
	}
	err := RunCleanup(context.Background(), ordered, SessionVars{Name: "s"}, tasks, nil)
	if err == nil {
		t.Fatal("RunCleanup: want an error for a cleanup value that cannot resolve, got nil")
	}
	if !strings.Contains(err.Error(), "never_produced") {
		t.Errorf("error = %v, want it to name the unresolvable value", err)
	}
	if got := tasks["outer"].Status; got != contract.TaskStatusFailed {
		t.Errorf("status = %q, want %q", got, contract.TaskStatusFailed)
	}
}

// TestRunCleanup_NestedDefinitionDriftLeavesTheDebtVisible covers a chain
// edited between setup and teardown: running a script from a layer that is
// not the one recorded would release the wrong thing, so nothing runs — and
// the composed task must not read as cleaned while those records stand.
func TestRunCleanup_NestedDefinitionDriftLeavesTheDebtVisible(t *testing.T) {
	exec := withScriptedExecutor(t, &scriptedExecutor{})
	outer := config.TaskDefinition{ID: "outer", Scope: "run", Setup: shellStub("outer-setup"), Cleanup: shellStub("outer-cleanup")}
	inner := config.TaskDefinition{ID: "inner", Scope: "run", Setup: shellStub("inner-setup"), Cleanup: shellStub("inner-cleanup")}
	ordered := nestedPlan(t, outer, inner)
	tasks := map[string]*contract.TaskState{}
	if err := RunSetup(context.Background(), ordered, SessionVars{Name: "s"}, tasks, nil); err != nil {
		t.Fatalf("RunSetup: %v", err)
	}
	exec.reset()
	// The author removed `inner` from the definition after setup.
	drifted := ordered[0]
	drifted.Layers = nil
	err := RunCleanup(context.Background(), []Resolved{drifted}, SessionVars{Name: "s"}, tasks, nil)
	if err == nil {
		t.Fatal("RunCleanup: want an error while layer records still owe a release, got nil")
	}
	if len(exec.commands()) != 0 {
		t.Errorf("cleanup ran %v, want nothing run against a chain that no longer matches", exec.commands())
	}
	if got := tasks["outer"].Status; got == contract.TaskStatusCleaned {
		t.Error("the composed task reads as cleaned while its layer records still owe a release")
	}
}

// TestRunCleanup_NestedOuterWithoutCleanupStillUnwindsInner guards the
// interaction with the plain-task shortcut for a cleanup-less task: the
// composed task's cleanup is the chain's, so an outer layer declaring none
// must not read as "the whole chain is already released".
func TestRunCleanup_NestedOuterWithoutCleanupStillUnwindsInner(t *testing.T) {
	exec := withScriptedExecutor(t, &scriptedExecutor{})
	outer := config.TaskDefinition{ID: "outer", Scope: "run", Setup: shellStub("outer-setup")}
	inner := config.TaskDefinition{ID: "inner", Scope: "run", Setup: shellStub("inner-setup"), Cleanup: shellStub("inner-cleanup")}
	ordered := nestedPlan(t, outer, inner)
	tasks := map[string]*contract.TaskState{}
	if err := RunSetup(context.Background(), ordered, SessionVars{Name: "s"}, tasks, nil); err != nil {
		t.Fatalf("RunSetup: %v", err)
	}
	exec.reset()
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
		config.TaskDefinition{ID: "outer", Scope: "run", Setup: shellStub("outer-setup")},
		config.TaskDefinition{ID: "inner", Scope: "run", Setup: shellStub("inner-setup")},
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
		config.TaskDefinition{ID: "outer", Scope: "run", Setup: shellStub("outer-setup")},
		config.TaskDefinition{ID: "inner", Scope: "run", Setup: shellStub("inner-setup")},
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
	inner := config.TaskDefinition{ID: "claude", Scope: "run", Setup: shellStub("inner-setup")}
	outer := config.TaskDefinition{ID: "team_claude", Scope: "run", Setup: shellStub("outer-setup"), Inner: "claude", InnerChain: []config.TaskDefinition{inner}}
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

// TestCleanupLayers_CarryTheOutwardJoint is the teardown path's own
// requirement: a layer's cleanup reads its own public contract, which exists
// only because `[outputs.bind]` projects it. Teardown rebuilds the chain
// without compiling schemas, so the bindings have to survive that rebuild or
// an outer layer can never release what its setup produced.
func TestCleanupLayers_CarryTheOutwardJoint(t *testing.T) {
	exec := withScriptedExecutor(t, &scriptedExecutor{})
	outer := config.TaskDefinition{
		ID: "outer", Scope: "run", Cleanup: &lang.Action{
			Type:   lang.ActionShell,
			Script: "rm -rf $guard_dir",
			Bind:   map[string]*lang.Value{"guard_dir": fromValue("self.outputs.guard_dir")},
		},
		OutputsBind: map[string]*lang.Value{"guard_dir": fromValue("locals.guard_dir")},
		InnerChain:  []config.TaskDefinition{{ID: "inner", Scope: "run", Cleanup: shellStub("inner-cleanup")}},
	}
	outer.Inner = "inner"
	r := Resolved{NodeID: "outer", Scope: config.TaskScopeRun, Cleanup: outer.Cleanup, Layers: CleanupLayers(outer)}
	tasks := map[string]*contract.TaskState{"outer": {
		Scope:  config.TaskScopeRun,
		Status: contract.TaskStatusProduced,
		Layers: []contract.TaskLayerState{
			{TaskID: "outer", Status: contract.TaskStatusProduced, Locals: map[string]any{"guard_dir": "/tmp/guard"}},
			{TaskID: "inner", Status: contract.TaskStatusProduced, Outputs: map[string]any{"pid": "42"}},
		},
	}}
	if err := RunCleanup(context.Background(), []Resolved{r}, SessionVars{Name: "s"}, tasks, nil); err != nil {
		t.Fatalf("RunCleanup: %v", err)
	}
	if len(exec.bindings) != 2 {
		t.Fatalf("executions = %d, want both layers released", len(exec.bindings))
	}
	if !strings.Contains(exec.bindings[1], "/tmp/guard") {
		t.Errorf("outer cleanup bindings = %q, want the projected local", exec.bindings[1])
	}
}
