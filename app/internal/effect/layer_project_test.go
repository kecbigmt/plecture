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

// TestProjectPublicOutputs_ProjectsTheComposedContract covers the composed
// task's public face: only what `[bind.outputs]` names, under the names it
// gives — re-export, rename, a value computed from an inner output, and one
// bound from the joint's own local.
func TestProjectPublicOutputs_ProjectsTheComposedContract(t *testing.T) {
	withScriptedExecutor(t, &scriptedExecutor{stdout: map[string]string{
		"outer-setup": `{"guard_dir":"/tmp/guard"}`,
		"inner-setup": `{"pid":42,"socket_path":"/tmp/sock","session_id":"abc"}`,
	}})
	layers := nestedLayers(t,
		config.TaskDefinition{
			ID: "outer", Setup: shellStub("outer-setup"),
			OutputsBind: map[string]*lang.Value{
				"pid":           fromValue("inner.outputs.pid"),
				"agent_session": fromValue("inner.outputs.session_id"),
				"socket_label":  exprValue(`'socket:' + string(inner.outputs.socket_path)`),
				"guard_dir":     fromValue("locals.guard_dir"),
			},
		},
		config.TaskDefinition{ID: "inner", Setup: shellStub("inner-setup")},
	)
	states, _, err := RunLayers(context.Background(), layers, testChainHost("s"), "", nil)
	if err != nil {
		t.Fatalf("RunLayers: %v", err)
	}
	got, err := ProjectPublicOutputs(layers, states, zeroCaps)
	if err != nil {
		t.Fatalf("ProjectPublicOutputs: %v", err)
	}
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

// TestProjectPublicOutputs_DirectBindingKeepsTheInnerNativeType covers the
// difference the direct/computed split buys: a direct binding projects the
// inner value as it is, while a computed one is a rendered string.
func TestProjectPublicOutputs_DirectBindingKeepsTheInnerNativeType(t *testing.T) {
	withScriptedExecutor(t, &scriptedExecutor{stdout: map[string]string{
		"inner-setup": `{"pid":42,"ready":true}`,
	}})
	layers := nestedLayers(t,
		config.TaskDefinition{
			ID: "outer",
			OutputsBind: map[string]*lang.Value{
				"pid":      fromValue("inner.outputs.pid"),
				"ready":    fromValue("inner.outputs.ready"),
				"pid_text": exprValue(`string(inner.outputs.pid) + '!'`),
			},
		},
		config.TaskDefinition{ID: "inner", Setup: shellStub("inner-setup")},
	)
	states, _, err := RunLayers(context.Background(), layers, testChainHost("s"), "", nil)
	if err != nil {
		t.Fatalf("RunLayers: %v", err)
	}
	got, err := ProjectPublicOutputs(layers, states, zeroCaps)
	if err != nil {
		t.Fatalf("ProjectPublicOutputs: %v", err)
	}
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

// TestProjectPublicOutputs_OmitsUnavailableInnerOutputs covers a bound key
// whose inner source has not been produced yet: the public contract simply
// does not carry it, exactly as a plain task's outputs would not, rather
// than carrying an empty string that reads as a value.
func TestProjectPublicOutputs_OmitsUnavailableInnerOutputs(t *testing.T) {
	withScriptedExecutor(t, &scriptedExecutor{stdout: map[string]string{
		"inner-setup": `{"pid":"1"}`,
	}})
	layers := nestedLayers(t,
		config.TaskDefinition{
			ID: "outer",
			OutputsBind: map[string]*lang.Value{
				"pid":            fromValue("inner.outputs.pid"),
				"review":         fromValue("inner.outputs.review_decision"),
				"review_display": exprValue(`'decision: ' + string(inner.outputs.review_decision)`),
			},
		},
		config.TaskDefinition{ID: "inner", Setup: shellStub("inner-setup")},
	)
	states, _, err := RunLayers(context.Background(), layers, testChainHost("s"), "", nil)
	if err != nil {
		t.Fatalf("RunLayers: %v", err)
	}
	got, err := ProjectPublicOutputs(layers, states, zeroCaps)
	if err != nil {
		t.Fatalf("ProjectPublicOutputs: %v", err)
	}
	if got["pid"] != "1" {
		t.Errorf("pid = %#v, want the produced inner output", got["pid"])
	}
	for _, key := range []string{"review", "review_display"} {
		if _, present := got[key]; present {
			t.Errorf("%s = %#v, want it absent until its inner source is produced", key, got[key])
		}
	}
}

// TestProjectPublicOutputs_BindingResolutionFailure covers the other half of
// the runtime failure rule for a public binding: a binding that cannot be
// resolved at all.
func TestProjectPublicOutputs_BindingResolutionFailure(t *testing.T) {
	withScriptedExecutor(t, &scriptedExecutor{stdout: map[string]string{
		"outer-setup": `{"guard_dir":"/tmp/guard"}`,
	}})
	layers := nestedLayers(t,
		config.TaskDefinition{
			ID: "outer", Setup: shellStub("outer-setup"),
			OutputsBind: map[string]*lang.Value{"guard": exprValue(`string(locals.absent) + '/x'`)},
		},
		config.TaskDefinition{ID: "inner", Setup: shellStub("inner-setup")},
	)
	states, _, err := RunLayers(context.Background(), layers, testChainHost("s"), "", nil)
	if err != nil {
		t.Fatalf("RunLayers: %v", err)
	}
	_, err = ProjectPublicOutputs(layers, states, zeroCaps)
	if err == nil {
		t.Fatal("ProjectPublicOutputs: want an error for a public binding that cannot resolve, got nil")
	}
	if !strings.Contains(err.Error(), "outputs.bind") {
		t.Errorf("error = %v, want it to name the failing binding table", err)
	}
}

// TestProjectPublicOutputs_TraversesEveryLayer covers a three-layer chain:
// each layer projects the layer inside it, so the outermost contract is what
// the outermost layer named, whatever the middle layer renamed.
func TestProjectPublicOutputs_TraversesEveryLayer(t *testing.T) {
	withScriptedExecutor(t, &scriptedExecutor{stdout: map[string]string{
		"inner-setup": `{"pid":7}`,
	}})
	layers := nestedLayers(t,
		config.TaskDefinition{
			ID:          "outer",
			OutputsBind: map[string]*lang.Value{"agent_pid": fromValue("inner.outputs.runtime_pid")},
		},
		config.TaskDefinition{
			ID:          "middle",
			OutputsBind: map[string]*lang.Value{"runtime_pid": fromValue("inner.outputs.pid")},
		},
		config.TaskDefinition{ID: "inner", Setup: shellStub("inner-setup")},
	)
	states, _, err := RunLayers(context.Background(), layers, testChainHost("s"), "", nil)
	if err != nil {
		t.Fatalf("RunLayers: %v", err)
	}
	got, err := ProjectPublicOutputs(layers, states, zeroCaps)
	if err != nil {
		t.Fatalf("ProjectPublicOutputs: %v", err)
	}
	if len(got) != 1 || got["agent_pid"] != float64(7) {
		t.Errorf("public outputs = %#v, want the outermost name carrying the innermost value", got)
	}
}

// TestApplyMutableOutputs_RoutesThroughDirectBindings covers the write half
// of the contract: a mutable public key addresses the inner output its
// direct binding chain names, and the projection re-reads from there.
func TestApplyMutableOutputs_RoutesThroughDirectBindings(t *testing.T) {
	withScriptedExecutor(t, &scriptedExecutor{stdout: map[string]string{
		"inner-setup": `{"pid":"1"}`,
	}})
	layers := nestedLayers(t,
		config.TaskDefinition{
			ID:          "outer",
			OutputsBind: map[string]*lang.Value{"agent_pid": fromValue("inner.outputs.runtime_pid")},
		},
		config.TaskDefinition{
			ID:          "middle",
			OutputsBind: map[string]*lang.Value{"runtime_pid": fromValue("inner.outputs.pid")},
		},
		config.TaskDefinition{ID: "inner", Setup: shellStub("inner-setup")},
	)
	states, _, err := RunLayers(context.Background(), layers, testChainHost("s"), "", nil)
	if err != nil {
		t.Fatalf("RunLayers: %v", err)
	}
	outputs, err := ProjectPublicOutputs(layers, states, zeroCaps)
	if err != nil {
		t.Fatalf("ProjectPublicOutputs: %v", err)
	}
	st := &contract.TaskState{Layers: states, Outputs: outputs}
	if err := ApplyMutableOutputs(layers, st, map[string]any{"agent_pid": "99"}); err != nil {
		t.Fatalf("ApplyMutableOutputs: %v", err)
	}
	if got := st.Layers[2].Outputs["pid"]; got != "99" {
		t.Errorf("innermost pid = %#v, want the routed write", got)
	}
	if got := st.Outputs["agent_pid"]; got != "99" {
		t.Errorf("public agent_pid = %#v, want the re-read projection", got)
	}
}

// TestApplyMutableOutputs_RefusesComputedBindings covers the read-only half:
// a computed binding is a rendering, so there is no inner output a write
// could address.
func TestApplyMutableOutputs_RefusesComputedBindings(t *testing.T) {
	withScriptedExecutor(t, &scriptedExecutor{stdout: map[string]string{
		"outer-setup": `{"guard_dir":"/tmp/guard"}`,
		"inner-setup": `{"pid":"1"}`,
	}})
	layers := nestedLayers(t,
		config.TaskDefinition{
			ID: "outer", Setup: shellStub("outer-setup"),
			OutputsBind: map[string]*lang.Value{
				"label":     exprValue(`'pid-' + string(inner.outputs.pid)`),
				"guard_dir": fromValue("locals.guard_dir"),
			},
		},
		config.TaskDefinition{ID: "inner", Setup: shellStub("inner-setup")},
	)
	states, _, err := RunLayers(context.Background(), layers, testChainHost("s"), "", nil)
	if err != nil {
		t.Fatalf("RunLayers: %v", err)
	}
	outputs, err := ProjectPublicOutputs(layers, states, zeroCaps)
	if err != nil {
		t.Fatalf("ProjectPublicOutputs: %v", err)
	}
	st := &contract.TaskState{Layers: states, Outputs: outputs}
	for _, key := range []string{"label", "guard_dir"} {
		err := ApplyMutableOutputs(layers, st, map[string]any{key: "x"})
		if err == nil {
			t.Fatalf("ApplyMutableOutputs(%s): want an error for a computed binding, got nil", key)
		}
		if !strings.Contains(err.Error(), key) {
			t.Errorf("error = %v, want it to name the key %q", err, key)
		}
	}
}
