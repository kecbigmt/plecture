package task

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/lang"
	contract "github.com/kecbigmt/plecture/contracts/state"
)

// TestRunSetup_NestedProjectsThePublicContract covers the composed task's
// public face: only what `[bind.outputs]` names, under the names it gives —
// re-export, rename, a value computed from an inner output, and one bound
// from the joint's own local.
func TestRunSetup_NestedProjectsThePublicContract(t *testing.T) {
	withScriptedExecutor(t, &scriptedExecutor{stdout: map[string]string{
		"outer-setup": `{"guard_dir":"/tmp/guard"}`,
		"inner-setup": `{"pid":42,"socket_path":"/tmp/sock","session_id":"abc"}`,
	}})
	outer := config.TaskDefinition{
		ID: "outer", Scope: "run", Setup: shellStub("outer-setup"),
		OutputsBind: map[string]*lang.Value{
			"pid":           fromValue("inner.outputs.pid"),
			"agent_session": fromValue("inner.outputs.session_id"),
			"socket_label":  exprValue(`'socket:' + string(inner.outputs.socket_path)`),
			"guard_dir":     fromValue("locals.guard_dir"),
		},
	}
	inner := config.TaskDefinition{ID: "inner", Scope: "run", Setup: shellStub("inner-setup")}
	tasks := map[string]*contract.TaskState{}
	if err := RunSetup(context.Background(), nestedPlan(t, outer, inner), SessionVars{Name: "s"}, tasks, nil); err != nil {
		t.Fatalf("RunSetup: %v", err)
	}
	got := tasks["outer"].Outputs
	want := map[string]any{
		"pid":           float64(42),
		"agent_session": "abc",
		"socket_label":  "socket:/tmp/sock",
		"guard_dir":     "/tmp/guard",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("public outputs = %#v, want %#v", got, want)
	}
	if _, leaked := got["socket_path"]; leaked {
		t.Error("an inner output the joint never bound must not appear in the public contract")
	}
}

// TestRunSetup_NestedDirectBindingKeepsTheInnerNativeType covers the
// difference the direct/computed split buys: a direct binding projects the
// inner value as it is, while a computed one is a rendered string.
func TestRunSetup_NestedDirectBindingKeepsTheInnerNativeType(t *testing.T) {
	withScriptedExecutor(t, &scriptedExecutor{stdout: map[string]string{
		"inner-setup": `{"pid":42,"ready":true}`,
	}})
	outer := config.TaskDefinition{
		ID: "outer", Scope: "run", Setup: shellStub(""),
		OutputsBind: map[string]*lang.Value{
			"pid":      fromValue("inner.outputs.pid"),
			"ready":    fromValue("inner.outputs.ready"),
			"pid_text": exprValue(`string(inner.outputs.pid) + '!'`),
		},
	}
	inner := config.TaskDefinition{ID: "inner", Scope: "run", Setup: shellStub("inner-setup")}
	tasks := map[string]*contract.TaskState{}
	if err := RunSetup(context.Background(), nestedPlan(t, outer, inner), SessionVars{Name: "s"}, tasks, nil); err != nil {
		t.Fatalf("RunSetup: %v", err)
	}
	got := tasks["outer"].Outputs
	if _, isString := got["pid"].(string); isString {
		t.Errorf("pid = %#v, want the inner output's own numeric value", got["pid"])
	}
	if got["ready"] != true {
		t.Errorf("ready = %#v, want the inner boolean", got["ready"])
	}
	if got["pid_text"] != "42!" {
		t.Errorf("pid_text = %#v, want the rendered string", got["pid_text"])
	}
}

// TestRunSetup_NestedProjectionOmitsUnavailableInnerOutputs covers a bound
// key whose inner source has not been produced yet: the public contract
// simply does not carry it, exactly as a plain task's outputs would not,
// rather than carrying an empty string that reads as a value.
func TestRunSetup_NestedProjectionOmitsUnavailableInnerOutputs(t *testing.T) {
	withScriptedExecutor(t, &scriptedExecutor{stdout: map[string]string{
		"inner-setup": `{"pid":"1"}`,
	}})
	outer := config.TaskDefinition{
		ID: "outer", Scope: "run",
		OutputsBind: map[string]*lang.Value{
			"pid":            fromValue("inner.outputs.pid"),
			"review":         fromValue("inner.outputs.review_decision"),
			"review_display": exprValue(`'decision: ' + string(inner.outputs.review_decision)`),
		},
	}
	inner := config.TaskDefinition{ID: "inner", Scope: "run", Setup: shellStub("inner-setup")}
	tasks := map[string]*contract.TaskState{}
	if err := RunSetup(context.Background(), nestedPlan(t, outer, inner), SessionVars{Name: "s"}, tasks, nil); err != nil {
		t.Fatalf("RunSetup: %v", err)
	}
	got := tasks["outer"].Outputs
	if got["pid"] != "1" {
		t.Errorf("pid = %#v, want the produced inner output", got["pid"])
	}
	for _, key := range []string{"review", "review_display"} {
		if _, present := got[key]; present {
			t.Errorf("%s = %#v, want it absent until its inner source is produced", key, got[key])
		}
	}
}

// TestRunSetup_NestedPublicOutputsValidatedAgainstOuterSchema covers the
// runtime failure rule for the projection: the composed contract answers to
// the outer schema the way a plain task's setup answers to its own.
func TestRunSetup_NestedPublicOutputsValidatedAgainstOuterSchema(t *testing.T) {
	withScriptedExecutor(t, &scriptedExecutor{stdout: map[string]string{
		"inner-setup": `{"pid":42}`,
	}})
	outer := config.TaskDefinition{
		ID: "outer", Scope: "run",
		OutputsBind:   map[string]*lang.Value{"pid": fromValue("inner.outputs.pid")},
		OutputsSchema: objectSchema(map[string]any{"pid": map[string]any{"type": "string"}}, "pid"),
	}
	inner := config.TaskDefinition{ID: "inner", Scope: "run", Setup: shellStub("inner-setup")}
	tasks := map[string]*contract.TaskState{}
	err := RunSetup(context.Background(), nestedPlan(t, outer, inner), SessionVars{Name: "s"}, tasks, nil)
	if err == nil {
		t.Fatal("RunSetup: want an error for a projection the outer schema rejects, got nil")
	}
	if !strings.Contains(err.Error(), "outputs schema") {
		t.Errorf("error = %v, want it to name the public outputs contract", err)
	}
}

// TestRunSetup_NestedPublicBindingResolutionFailure covers the other half of
// that rule: a binding that cannot be resolved at all.
func TestRunSetup_NestedPublicBindingResolutionFailure(t *testing.T) {
	withScriptedExecutor(t, &scriptedExecutor{stdout: map[string]string{
		"outer-setup": `{"guard_dir":"/tmp/guard"}`,
	}})
	outer := config.TaskDefinition{
		ID: "outer", Scope: "run", Setup: shellStub("outer-setup"),
		OutputsBind: map[string]*lang.Value{"guard": exprValue(`string(locals.absent) + '/x'`)},
	}
	inner := config.TaskDefinition{ID: "inner", Scope: "run", Setup: shellStub("inner-setup")}
	tasks := map[string]*contract.TaskState{}
	err := RunSetup(context.Background(), nestedPlan(t, outer, inner), SessionVars{Name: "s"}, tasks, nil)
	if err == nil {
		t.Fatal("RunSetup: want an error for a public binding that cannot resolve, got nil")
	}
	if !strings.Contains(err.Error(), "outputs.bind") {
		t.Errorf("error = %v, want it to name the failing binding table", err)
	}
}

// TestRunSetup_NestedProjectionTraversesEveryLayer covers a three-layer
// chain: each layer projects the layer inside it, so the outermost contract
// is what the outermost layer named, whatever the middle layer renamed.
func TestRunSetup_NestedProjectionTraversesEveryLayer(t *testing.T) {
	withScriptedExecutor(t, &scriptedExecutor{stdout: map[string]string{
		"inner-setup": `{"pid":7}`,
	}})
	outer := config.TaskDefinition{
		ID: "outer", Scope: "run",
		OutputsBind: map[string]*lang.Value{"agent_pid": fromValue("inner.outputs.runtime_pid")},
	}
	middle := config.TaskDefinition{
		ID: "middle", Scope: "run",
		OutputsBind: map[string]*lang.Value{"runtime_pid": fromValue("inner.outputs.pid")},
	}
	inner := config.TaskDefinition{ID: "inner", Scope: "run", Setup: shellStub("inner-setup")}
	tasks := map[string]*contract.TaskState{}
	if err := RunSetup(context.Background(), nestedPlan(t, outer, middle, inner), SessionVars{Name: "s"}, tasks, nil); err != nil {
		t.Fatalf("RunSetup: %v", err)
	}
	got := tasks["outer"].Outputs
	if len(got) != 1 || got["agent_pid"] != float64(7) {
		t.Errorf("public outputs = %#v, want the outermost name carrying the innermost value", got)
	}
}

// TestApplyMutableOutputs_NestedRoutesThroughDirectBindings covers the write
// half of the contract: a mutable public key addresses the inner output its
// direct binding chain names, and the projection re-reads from there.
func TestApplyMutableOutputs_NestedRoutesThroughDirectBindings(t *testing.T) {
	withScriptedExecutor(t, &scriptedExecutor{stdout: map[string]string{
		"inner-setup": `{"pid":"1"}`,
	}})
	outer := config.TaskDefinition{
		ID: "outer", Scope: "run",
		OutputsBind: map[string]*lang.Value{"agent_pid": fromValue("inner.outputs.runtime_pid")},
	}
	middle := config.TaskDefinition{
		ID: "middle", Scope: "run",
		OutputsBind: map[string]*lang.Value{"runtime_pid": fromValue("inner.outputs.pid")},
	}
	inner := config.TaskDefinition{ID: "inner", Scope: "run", Setup: shellStub("inner-setup")}
	ordered := nestedPlan(t, outer, middle, inner)
	tasks := map[string]*contract.TaskState{}
	if err := RunSetup(context.Background(), ordered, SessionVars{Name: "s"}, tasks, nil); err != nil {
		t.Fatalf("RunSetup: %v", err)
	}
	st := tasks["outer"]
	if err := ApplyMutableOutputs(ordered[0].Layers, st, map[string]any{"agent_pid": "99"}); err != nil {
		t.Fatalf("ApplyMutableOutputs: %v", err)
	}
	if got := st.Layers[2].Outputs["pid"]; got != "99" {
		t.Errorf("innermost pid = %#v, want the routed write", got)
	}
	if got := st.Outputs["agent_pid"]; got != "99" {
		t.Errorf("public agent_pid = %#v, want the re-read projection", got)
	}
}

// TestApplyMutableOutputs_NestedRefusesComputedBindings covers the read-only
// half: a computed binding is a rendering, so there is no inner output a
// write could address.
func TestApplyMutableOutputs_NestedRefusesComputedBindings(t *testing.T) {
	withScriptedExecutor(t, &scriptedExecutor{stdout: map[string]string{
		"outer-setup": `{"guard_dir":"/tmp/guard"}`,
		"inner-setup": `{"pid":"1"}`,
	}})
	outer := config.TaskDefinition{
		ID: "outer", Scope: "run", Setup: shellStub("outer-setup"),
		OutputsBind: map[string]*lang.Value{
			"label":     exprValue(`'pid-' + string(inner.outputs.pid)`),
			"guard_dir": fromValue("locals.guard_dir"),
		},
	}
	inner := config.TaskDefinition{ID: "inner", Scope: "run", Setup: shellStub("inner-setup")}
	ordered := nestedPlan(t, outer, inner)
	tasks := map[string]*contract.TaskState{}
	if err := RunSetup(context.Background(), ordered, SessionVars{Name: "s"}, tasks, nil); err != nil {
		t.Fatalf("RunSetup: %v", err)
	}
	for _, key := range []string{"label", "guard_dir"} {
		err := ApplyMutableOutputs(ordered[0].Layers, tasks["outer"], map[string]any{key: "x"})
		if err == nil {
			t.Fatalf("ApplyMutableOutputs(%s): want an error for a computed binding, got nil", key)
		}
		if !strings.Contains(err.Error(), key) {
			t.Errorf("error = %v, want it to name the key %q", err, key)
		}
	}
}

// TestRunSetup_DownstreamNodeReadsTheComposedContract covers what makes the
// projection worth having: from the outside a nested task is exactly a task,
// so a downstream node wires to its public key with no nesting-aware
// vocabulary, and reaches nothing the joint did not bind.
func TestRunSetup_DownstreamNodeReadsTheComposedContract(t *testing.T) {
	withScriptedExecutor(t, &scriptedExecutor{stdout: map[string]string{
		"inner-setup": `{"pid":"11","socket_path":"/tmp/sock"}`,
	}})
	inner := config.TaskDefinition{ID: "inner", Scope: "run", Setup: shellStub("inner-setup")}
	outer := config.TaskDefinition{
		ID: "runtime", Scope: "run", Inner: "inner", InnerChain: []config.TaskDefinition{inner},
		OutputsBind:   map[string]*lang.Value{"agent_pid": fromValue("inner.outputs.pid")},
		OutputsSchema: objectSchema(map[string]any{"agent_pid": map[string]any{"type": "string"}}),
	}
	consumer := config.TaskDefinition{ID: "consumer", Scope: "run", Setup: shellStub("consumer-setup")}
	plan, err := CompileWorkflow(
		config.WorkflowFile{ID: "test", Nodes: []config.WorkflowNode{
			{ID: "runtime"},
			{ID: "consumer", Inputs: map[string]string{"pid": "{{.Nodes.runtime.outputs.agent_pid}}"}},
		}},
		map[string]config.TaskDefinition{"runtime": outer, "inner": inner, "consumer": consumer},
	)
	if err != nil {
		t.Fatalf("CompileWorkflow: %v", err)
	}
	tasks := map[string]*contract.TaskState{}
	if err := RunSetup(context.Background(), plan.Run, SessionVars{Name: "s"}, tasks, nil); err != nil {
		t.Fatalf("RunSetup: %v", err)
	}
	if got := tasks["consumer"].Inputs["pid"]; got != "11" {
		t.Errorf("downstream input = %#v, want the composed task's public value", got)
	}
	if _, leaked := tasks["runtime"].Outputs["socket_path"]; leaked {
		t.Error("an inner output the joint never bound must not be reachable downstream")
	}
}
