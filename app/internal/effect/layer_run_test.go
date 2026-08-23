package effect

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/lang"
	contract "github.com/kecbigmt/plecture/contracts/state"
)

// scriptedExecutor answers each request from a queue keyed by the rendered
// command, so a multi-layer run can hand every layer its own stdout while the
// recorded requests still show the order the layers ran in.
type scriptedExecutor struct {
	requests []ExecRequest
	scripts  []string
	bindings []string
	// stdout maps a resolved script to the stdout it produces. A script with
	// no entry produces "{}".
	stdout map[string]string
	// failOn makes the named script exit non-zero.
	failOn string
}

func (s *scriptedExecutor) Run(_ context.Context, req ExecRequest) ([]byte, []byte, error) {
	s.requests = append(s.requests, req)
	cmd, bindings := ReadShellRun(req)
	s.scripts = append(s.scripts, cmd)
	s.bindings = append(s.bindings, bindings)
	if s.failOn != "" && cmd == s.failOn {
		return nil, []byte("boom"), errScriptedFailure
	}
	if out, ok := s.stdout[cmd]; ok {
		return []byte(out), nil, nil
	}
	return []byte("{}"), nil, nil
}

type scriptedFailure struct{}

func (scriptedFailure) Error() string { return "exit status 1" }

var errScriptedFailure = scriptedFailure{}

func withScriptedExecutor(t *testing.T, e *scriptedExecutor) *scriptedExecutor {
	t.Helper()
	restore := UseExecutor(e)
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

// nestedLayers compiles a nesting chain the way ResolveLayers compiles one
// out of a definition, returning the layers a chain walk needs directly
// rather than the Resolved envelope a workflow-level caller wraps them in.
// defs are ordered outermost-first; each is linked to the next by Inner, and
// the chain is stamped the way config load stamps it.
func nestedLayers(t *testing.T, defs ...config.TaskDefinition) []Layer {
	t.Helper()
	for i := range defs[:len(defs)-1] {
		defs[i].Inner = defs[i+1].ID
	}
	outer := defs[0]
	outer.InnerChain = defs[1:]
	layers, err := ResolveLayers(outer)
	if err != nil {
		t.Fatalf("ResolveLayers: %v", err)
	}
	return layers
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

func literalValue(v string) *lang.Value { return &lang.Value{Form: lang.FormLiteral, Literal: v} }
func fromValue(path string) *lang.Value { return &lang.Value{Form: lang.FormFrom, From: path} }
func exprValue(src string) *lang.Value  { return &lang.Value{Form: lang.FormExpr, Expr: src} }

// shellStub is one shell action carrying a literal script, or nil for a
// stub that declares none.
func shellStub(script string) *lang.Action {
	if script == "" {
		return nil
	}
	return &lang.Action{Type: lang.ActionShell, Script: script}
}

// testChainHost builds the ChainHost a test drives a chain through: minimal
// setup/cleanup/inner roots exposing only what these tests bind against
// (inputs, locals, self.outputs, session.name), and zeroCaps for capability
// resolution since none of these actions resolve a bin or terminal
// reference.
func testChainHost(session string) ChainHost {
	return ChainHost{
		SetupRoots: func(_ Layer, inputs map[string]any) lang.Roots {
			return lang.Roots{"inputs": orEmpty(inputs)}
		},
		CleanupRoots: func(_ Layer, self, inputs map[string]any) lang.Roots {
			return lang.Roots{
				"self":   map[string]any{"outputs": orEmpty(self)},
				"inputs": orEmpty(inputs),
			}
		},
		InnerRoots: func(_ Layer, inputs, locals map[string]any) lang.Roots {
			return lang.Roots{
				"session": map[string]any{"name": session},
				"inputs":  orEmpty(inputs),
				"locals":  orEmpty(locals),
			}
		},
		Caps:   zeroCaps,
		OnSkip: func(string) {},
	}
}

func allCleaned(states []contract.LayerState) bool {
	for _, s := range states {
		if s.Status != contract.TaskStatusCleaned {
			return false
		}
	}
	return true
}

// TestRunLayers_ChainRunsOutsideIn covers the LIFO stack's setup half: each
// layer's setup runs before the layer it wraps, and each layer's own
// emission lands in its own slot — locals for an outer layer, outputs for
// the innermost.
func TestRunLayers_ChainRunsOutsideIn(t *testing.T) {
	exec := withScriptedExecutor(t, &scriptedExecutor{stdout: map[string]string{
		"outer-setup":  `{"guard_dir":"/tmp/guard"}`,
		"middle-setup": `{"socket":"/tmp/sock"}`,
		"inner-setup":  `{"pid":"42"}`,
	}})
	layers := nestedLayers(t,
		config.TaskDefinition{ID: "outer", Setup: shellStub("outer-setup")},
		config.TaskDefinition{ID: "middle", Setup: shellStub("middle-setup")},
		config.TaskDefinition{ID: "inner", Setup: shellStub("inner-setup")},
	)
	states, _, err := RunLayers(context.Background(), layers, testChainHost("s"), "", nil)
	if err != nil {
		t.Fatalf("RunLayers: %v", err)
	}
	want := []string{"outer-setup", "middle-setup", "inner-setup"}
	if got := exec.commands(); !reflect.DeepEqual(got, want) {
		t.Fatalf("setup order = %v, want %v", got, want)
	}
	if len(states) != 3 {
		t.Fatalf("states = %+v, want three", states)
	}
	for i, st := range states {
		if st.Status != contract.TaskStatusProduced {
			t.Errorf("state[%d].Status = %q, want %q", i, st.Status, contract.TaskStatusProduced)
		}
	}
	if got := states[0].Locals["guard_dir"]; got != "/tmp/guard" {
		t.Errorf("outer locals = %v, want the outer setup's emission", states[0].Locals)
	}
	if got := states[1].Locals["socket"]; got != "/tmp/sock" {
		t.Errorf("middle locals = %v, want the middle setup's emission", states[1].Locals)
	}
	if got := states[2].Outputs["pid"]; got != "42" {
		t.Errorf("innermost outputs = %v, want the inner setup's emission", states[2].Outputs)
	}
	if states[2].Locals != nil {
		t.Errorf("innermost locals = %v, want none — the innermost layer is not a joint", states[2].Locals)
	}
}

// TestRunLayers_BindInputsRenderInnerInputObject covers the joint's input
// half: the inner task's inputs come from the outer layer's bind.inputs,
// rendered against that layer's own inputs and locals.
func TestRunLayers_BindInputsRenderInnerInputObject(t *testing.T) {
	withScriptedExecutor(t, &scriptedExecutor{stdout: map[string]string{
		"outer-setup": `{"guard_dir":"/tmp/guard"}`,
	}})
	layers := nestedLayers(t,
		config.TaskDefinition{
			ID: "outer", Setup: shellStub("outer-setup"),
			InnerInputs: map[string]*lang.Value{
				"tmux_session": fromValue("inputs.tmux_session"),
				"path_prepend": fromValue("locals.guard_dir"),
			},
		},
		config.TaskDefinition{
			ID: "inner", Setup: shellStub("inner-setup"),
			InputsSchema: objectSchema(map[string]any{
				"tmux_session": map[string]any{"type": "string"},
				"path_prepend": map[string]any{"type": "string"},
			}, "tmux_session", "path_prepend"),
		},
	)
	states, _, err := RunLayers(context.Background(), layers, testChainHost("s"), "", map[string]any{"tmux_session": "sess-1"})
	if err != nil {
		t.Fatalf("RunLayers: %v", err)
	}
	got := states[1].Inputs
	want := map[string]any{"tmux_session": "sess-1", "path_prepend": "/tmp/guard"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("inner inputs = %v, want %v", got, want)
	}
}

// TestRunLayers_BoundInputsValidatedAgainstInnerSchema covers the runtime
// failure rule for bound inputs: the inner task's own schema is what they
// answer to, and a violation stops the chain before the inner setup runs.
func TestRunLayers_BoundInputsValidatedAgainstInnerSchema(t *testing.T) {
	exec := withScriptedExecutor(t, &scriptedExecutor{})
	layers := nestedLayers(t,
		config.TaskDefinition{
			ID: "outer", Setup: shellStub("outer-setup"),
			InnerInputs: map[string]*lang.Value{"model": literalValue("opus")},
		},
		config.TaskDefinition{
			ID: "inner", Setup: shellStub("inner-setup"),
			InputsSchema: objectSchema(map[string]any{
				"model":        map[string]any{"type": "string"},
				"tmux_session": map[string]any{"type": "string"},
			}, "tmux_session"),
		},
	)
	_, _, err := RunLayers(context.Background(), layers, testChainHost("s"), "", nil)
	if err == nil {
		t.Fatal("RunLayers: want an error for bound inputs the inner schema rejects, got nil")
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

// TestRunLayers_BindEnvReachesInnerExecutionsOnly covers the joint's env
// half: an outer layer's bind.env is injected into the executions of the
// layers it wraps, and never into its own.
func TestRunLayers_BindEnvReachesInnerExecutionsOnly(t *testing.T) {
	exec := withScriptedExecutor(t, &scriptedExecutor{stdout: map[string]string{
		"outer-setup": `{"guard_dir":"/tmp/guard"}`,
	}})
	layers := nestedLayers(t,
		config.TaskDefinition{
			ID: "outer", Setup: shellStub("outer-setup"),
			InnerEnv: map[string]*lang.Value{
				"PLECT_TEAM_CONTEXT": fromValue("session.name"),
				"PLECT_GUARD_DIR":    fromValue("locals.guard_dir"),
			},
		},
		config.TaskDefinition{ID: "inner", Setup: shellStub("inner-setup")},
	)
	states, _, err := RunLayers(context.Background(), layers, testChainHost("team/x"), "", nil)
	if err != nil {
		t.Fatalf("RunLayers: %v", err)
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
	if len(states) != 2 {
		t.Fatalf("states = %+v, want two", states)
	}
}

// TestRunLayers_OuterLocalsValidatedAgainstLocalsSchema covers the runtime
// failure rule for locals: the outer setup answers to its own locals_schema
// exactly as a plain task's setup answers to outputs_schema.
func TestRunLayers_OuterLocalsValidatedAgainstLocalsSchema(t *testing.T) {
	withScriptedExecutor(t, &scriptedExecutor{stdout: map[string]string{
		"outer-setup": `{"unexpected":"value"}`,
	}})
	layers := nestedLayers(t,
		config.TaskDefinition{
			ID: "outer", Setup: shellStub("outer-setup"),
			LocalsSchema: objectSchema(map[string]any{
				"guard_dir": map[string]any{"type": "string"},
			}, "guard_dir"),
		},
		config.TaskDefinition{ID: "inner", Setup: shellStub("inner-setup")},
	)
	_, _, err := RunLayers(context.Background(), layers, testChainHost("s"), "", nil)
	if err == nil {
		t.Fatal("RunLayers: want an error for locals the locals schema rejects, got nil")
	}
	if !strings.Contains(err.Error(), "locals") {
		t.Errorf("error = %v, want it to name the locals contract", err)
	}
}

// TestRunLayers_BindingTemplateRenderFailure covers the runtime failure rule
// for an unrenderable binding template.
func TestRunLayers_BindingTemplateRenderFailure(t *testing.T) {
	withScriptedExecutor(t, &scriptedExecutor{})
	layers := nestedLayers(t,
		config.TaskDefinition{
			ID: "outer", Setup: shellStub("outer-setup"),
			InnerInputs: map[string]*lang.Value{"model": fromValue("locals.absent")},
		},
		config.TaskDefinition{ID: "inner", Setup: shellStub("inner-setup")},
	)
	_, _, err := RunLayers(context.Background(), layers, testChainHost("s"), "", nil)
	if err == nil {
		t.Fatal("RunLayers: want an error for a joint value that cannot resolve, got nil")
	}
	if !strings.Contains(err.Error(), "inner.inputs") {
		t.Errorf("error = %v, want it to name the failing binding table", err)
	}
}

// TestRunLayerCleanup_UnwindsInsideOut covers the LIFO stack's cleanup half,
// including each layer reading its own public contract — which is how a
// layer releases what its setup produced as a private local: by projecting
// it outward.
func TestRunLayerCleanup_UnwindsInsideOut(t *testing.T) {
	exec := withScriptedExecutor(t, &scriptedExecutor{stdout: map[string]string{
		"outer-setup": `{"guard_dir":"/tmp/guard"}`,
	}})
	layers := nestedLayers(t,
		config.TaskDefinition{
			ID: "outer", Setup: shellStub("outer-setup"), Cleanup: &lang.Action{
				Type:   lang.ActionShell,
				Script: "rm -rf $guard_dir",
				Bind:   map[string]*lang.Value{"guard_dir": fromValue("self.outputs.guard_dir")},
			},
			OutputsBind: map[string]*lang.Value{"guard_dir": fromValue("locals.guard_dir")},
			InnerEnv:    map[string]*lang.Value{"PLECT_GUARD": literalValue("on")},
		},
		config.TaskDefinition{ID: "inner", Setup: shellStub("inner-setup"), Cleanup: shellStub("inner-cleanup")},
	)
	host := testChainHost("s")
	states, _, err := RunLayers(context.Background(), layers, host, "", nil)
	if err != nil {
		t.Fatalf("RunLayers: %v", err)
	}
	exec.reset()
	if _, err := RunLayerCleanup(context.Background(), layers, states, host, ""); err != nil {
		t.Fatalf("RunLayerCleanup: %v", err)
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
	if !allCleaned(states) {
		t.Errorf("states = %+v, want every layer cleaned", states)
	}
}

// TestRunLayerCleanup_SkipsLayersThatNeverSetUp covers the partial-failure
// unwind: a middle setup that fails after the outermost succeeded leaves the
// innermost layer — which the walk never reached — with nothing to release,
// while the two layers that did run are both offered their cleanup, the way a
// plain task whose setup failed still runs its own.
func TestRunLayerCleanup_SkipsLayersThatNeverSetUp(t *testing.T) {
	exec := withScriptedExecutor(t, &scriptedExecutor{
		stdout: map[string]string{"outer-setup": `{"guard_dir":"/tmp/guard"}`},
		failOn: "middle-setup",
	})
	layers := nestedLayers(t,
		config.TaskDefinition{ID: "outer", Setup: shellStub("outer-setup"), Cleanup: shellStub("outer-cleanup")},
		config.TaskDefinition{ID: "middle", Setup: shellStub("middle-setup"), Cleanup: shellStub("middle-cleanup")},
		config.TaskDefinition{ID: "inner", Setup: shellStub("inner-setup"), Cleanup: shellStub("inner-cleanup")},
	)
	host := testChainHost("s")
	states, _, err := RunLayers(context.Background(), layers, host, "", nil)
	if err == nil {
		t.Fatal("RunLayers: want an error when the middle setup fails, got nil")
	}
	exec.reset()
	if _, err := RunLayerCleanup(context.Background(), layers, states, host, ""); err != nil {
		t.Fatalf("RunLayerCleanup: %v", err)
	}
	want := []string{"middle-cleanup", "outer-cleanup"}
	if got := exec.commands(); !reflect.DeepEqual(got, want) {
		t.Fatalf("cleanup ran %v, want the layers that reached setup: %v", got, want)
	}
}

// TestRunLayerCleanup_RetriesALayerWhoseCleanupFailed covers the retry a
// plain task already gets: a layer that failed to release is still owed one,
// and until it is released the composed task must not read as cleaned.
func TestRunLayerCleanup_RetriesALayerWhoseCleanupFailed(t *testing.T) {
	exec := withScriptedExecutor(t, &scriptedExecutor{failOn: "inner-cleanup"})
	layers := nestedLayers(t,
		config.TaskDefinition{ID: "outer", Setup: shellStub("outer-setup"), Cleanup: shellStub("outer-cleanup")},
		config.TaskDefinition{ID: "inner", Setup: shellStub("inner-setup"), Cleanup: shellStub("inner-cleanup")},
	)
	host := testChainHost("s")
	states, _, err := RunLayers(context.Background(), layers, host, "", nil)
	if err != nil {
		t.Fatalf("RunLayers: %v", err)
	}
	if _, err := RunLayerCleanup(context.Background(), layers, states, host, ""); err == nil {
		t.Fatal("RunLayerCleanup: want the first cleanup to fail")
	}
	if allCleaned(states) {
		t.Fatal("states read as fully cleaned after a failed release")
	}
	exec.failOn = ""
	exec.reset()
	if _, err := RunLayerCleanup(context.Background(), layers, states, host, ""); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if got := exec.commands(); !reflect.DeepEqual(got, []string{"inner-cleanup"}) {
		t.Fatalf("retry ran %v, want the layer whose cleanup failed", got)
	}
	if !allCleaned(states) {
		t.Errorf("states = %+v, want every layer cleaned after a successful retry", states)
	}
}

// TestRunLayerCleanup_KeepsGoingPastAFailedLayer covers the unwind's
// resilience and its first-error contract: an inner layer that refuses to
// release must not strand the outer layers wrapping it.
func TestRunLayerCleanup_KeepsGoingPastAFailedLayer(t *testing.T) {
	exec := withScriptedExecutor(t, &scriptedExecutor{failOn: "inner-cleanup"})
	layers := nestedLayers(t,
		config.TaskDefinition{ID: "outer", Setup: shellStub("outer-setup"), Cleanup: shellStub("outer-cleanup")},
		config.TaskDefinition{ID: "inner", Setup: shellStub("inner-setup"), Cleanup: shellStub("inner-cleanup")},
	)
	host := testChainHost("s")
	states, _, err := RunLayers(context.Background(), layers, host, "", nil)
	if err != nil {
		t.Fatalf("RunLayers: %v", err)
	}
	exec.reset()
	_, err = RunLayerCleanup(context.Background(), layers, states, host, "")
	if err == nil {
		t.Fatal("RunLayerCleanup: want the failing layer's error, got nil")
	}
	if !strings.Contains(err.Error(), "inner") {
		t.Errorf("error = %v, want it to name the layer that failed", err)
	}
	if got := exec.commands(); !reflect.DeepEqual(got, []string{"inner-cleanup", "outer-cleanup"}) {
		t.Fatalf("cleanup ran %v, want the outer layer released despite the inner failure", got)
	}
	if states[0].Status != contract.TaskStatusCleaned || states[1].Status != contract.TaskStatusFailed {
		t.Errorf("layer statuses = %q/%q, want cleaned/failed", states[0].Status, states[1].Status)
	}
}

// TestRunLayerCleanup_CleanupValueFailure covers the other failure branch of
// the unwind: a cleanup whose values cannot be resolved leaves that layer
// owing a release rather than silently counting as one.
func TestRunLayerCleanup_CleanupValueFailure(t *testing.T) {
	withScriptedExecutor(t, &scriptedExecutor{})
	layers := nestedLayers(t,
		config.TaskDefinition{ID: "outer", Setup: shellStub("outer-setup"), Cleanup: shellStub("outer-cleanup")},
		config.TaskDefinition{ID: "inner", Setup: shellStub("inner-setup"), Cleanup: &lang.Action{
			Type:   lang.ActionShell,
			Script: "inner-cleanup",
			Bind:   map[string]*lang.Value{"missing": fromValue("self.outputs.never_produced")},
		}},
	)
	host := testChainHost("s")
	states, _, err := RunLayers(context.Background(), layers, host, "", nil)
	if err != nil {
		t.Fatalf("RunLayers: %v", err)
	}
	_, err = RunLayerCleanup(context.Background(), layers, states, host, "")
	if err == nil {
		t.Fatal("RunLayerCleanup: want an error for a cleanup value that cannot resolve, got nil")
	}
	if !strings.Contains(err.Error(), "never_produced") {
		t.Errorf("error = %v, want it to name the unresolvable value", err)
	}
	if allCleaned(states) {
		t.Error("states read as fully cleaned despite the unresolvable cleanup value")
	}
}

// TestRunLayerCleanup_DefinitionDriftLeavesTheDebtVisible covers a chain
// edited between setup and teardown: running a script from a layer that is
// not the one recorded would release the wrong thing, so nothing runs — and
// the composed task must not read as cleaned while those records stand.
func TestRunLayerCleanup_DefinitionDriftLeavesTheDebtVisible(t *testing.T) {
	exec := withScriptedExecutor(t, &scriptedExecutor{})
	layers := nestedLayers(t,
		config.TaskDefinition{ID: "outer", Setup: shellStub("outer-setup"), Cleanup: shellStub("outer-cleanup")},
		config.TaskDefinition{ID: "inner", Setup: shellStub("inner-setup"), Cleanup: shellStub("inner-cleanup")},
	)
	host := testChainHost("s")
	states, _, err := RunLayers(context.Background(), layers, host, "", nil)
	if err != nil {
		t.Fatalf("RunLayers: %v", err)
	}
	exec.reset()
	// The author removed `inner` from the definition after setup.
	_, err = RunLayerCleanup(context.Background(), nil, states, host, "")
	if err == nil {
		t.Fatal("RunLayerCleanup: want an error while layer records still owe a release, got nil")
	}
	if len(exec.commands()) != 0 {
		t.Errorf("cleanup ran %v, want nothing run against a chain that no longer matches", exec.commands())
	}
	if allCleaned(states) {
		t.Error("the composed task reads as cleaned while its layer records still owe a release")
	}
}

// TestRunLayerCleanup_OuterWithoutCleanupStillUnwindsInner guards the
// interaction with the plain-task shortcut for a cleanup-less task: the
// composed task's cleanup is the chain's, so an outer layer declaring none
// must not read as "the whole chain is already released".
func TestRunLayerCleanup_OuterWithoutCleanupStillUnwindsInner(t *testing.T) {
	exec := withScriptedExecutor(t, &scriptedExecutor{})
	layers := nestedLayers(t,
		config.TaskDefinition{ID: "outer", Setup: shellStub("outer-setup")},
		config.TaskDefinition{ID: "inner", Setup: shellStub("inner-setup"), Cleanup: shellStub("inner-cleanup")},
	)
	host := testChainHost("s")
	states, _, err := RunLayers(context.Background(), layers, host, "", nil)
	if err != nil {
		t.Fatalf("RunLayers: %v", err)
	}
	exec.reset()
	if _, err := RunLayerCleanup(context.Background(), layers, states, host, ""); err != nil {
		t.Fatalf("RunLayerCleanup: %v", err)
	}
	if got := exec.commands(); !reflect.DeepEqual(got, []string{"inner-cleanup"}) {
		t.Fatalf("cleanup ran %v, want the inner layer's cleanup", got)
	}
}

// TestRunLayerCleanup_CarriesTheOutwardJointWithoutSchemas is the teardown
// path's own requirement: a layer's cleanup reads its own public contract,
// which exists only because `[outputs.bind]` projects it. CleanupLayers
// rebuilds the chain without compiling schemas, so the bindings have to
// survive that rebuild or an outer layer can never release what its setup
// produced.
func TestRunLayerCleanup_CarriesTheOutwardJointWithoutSchemas(t *testing.T) {
	exec := withScriptedExecutor(t, &scriptedExecutor{})
	outer := config.TaskDefinition{
		ID: "outer", Cleanup: &lang.Action{
			Type:   lang.ActionShell,
			Script: "rm -rf $guard_dir",
			Bind:   map[string]*lang.Value{"guard_dir": fromValue("self.outputs.guard_dir")},
		},
		OutputsBind: map[string]*lang.Value{"guard_dir": fromValue("locals.guard_dir")},
		Inner:       "inner",
		InnerChain:  []config.TaskDefinition{{ID: "inner", Cleanup: shellStub("inner-cleanup")}},
	}
	layers := CleanupLayers(outer)
	states := []contract.LayerState{
		{EffectID: "outer", Status: contract.TaskStatusProduced, Locals: map[string]any{"guard_dir": "/tmp/guard"}},
		{EffectID: "inner", Status: contract.TaskStatusProduced, Outputs: map[string]any{"pid": "42"}},
	}
	if _, err := RunLayerCleanup(context.Background(), layers, states, testChainHost("s"), ""); err != nil {
		t.Fatalf("RunLayerCleanup: %v", err)
	}
	if len(exec.bindings) != 2 {
		t.Fatalf("executions = %d, want both layers released", len(exec.bindings))
	}
	if !strings.Contains(exec.bindings[1], "/tmp/guard") {
		t.Errorf("outer cleanup bindings = %q, want the projected local", exec.bindings[1])
	}
}
