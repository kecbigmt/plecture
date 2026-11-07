package publicoutput

import (
	"reflect"
	"strings"
	"testing"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/domain"
	"github.com/kecbigmt/plecture/app/internal/lang"
	contract "github.com/kecbigmt/plecture/contracts/state"
)

func from(path string) *lang.Value { return &lang.Value{Form: lang.FormFrom, From: path} }

func produced(outputs map[string]any) *contract.TaskState {
	return &contract.TaskState{Status: contract.TaskStatusProduced, Outputs: outputs}
}

func TestEvaluate_BindsNodeOutputsAndSessionInputs(t *testing.T) {
	bindings := map[string]*lang.Value{
		"endpoint": from("nodes.orchestrator.outputs.endpoint"),
		"number":   from("session.inputs.number"),
		"label":    {Form: lang.FormExpr, Expr: "'review-' + nodes.agent.outputs.name"},
	}
	nodes := map[string]*contract.TaskState{
		"orchestrator": produced(map[string]any{"endpoint": "unix:///run/o.sock"}),
		"agent":        produced(map[string]any{"name": "a1"}),
	}
	got, err := Evaluate(Declaration{Bindings: bindings}, nodes, map[string]any{"number": "42"}, ProducedOnly)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	want := map[string]any{"endpoint": "unix:///run/o.sock", "number": "42", "label": "review-a1"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("outputs = %v, want %v", got, want)
	}
}

func TestEvaluate_WaitsForEverySourceOfABinding(t *testing.T) {
	bindings := map[string]*lang.Value{
		"endpoint": from("nodes.orchestrator.outputs.endpoint"),
		"label":    {Form: lang.FormExpr, Expr: "nodes.agent.outputs.name + nodes.orchestrator.outputs.endpoint"},
	}
	nodes := map[string]*contract.TaskState{
		"orchestrator": produced(map[string]any{"endpoint": "e"}),
		"agent":        {Status: contract.TaskStatusFailed, Outputs: map[string]any{"name": "stale"}},
	}
	got, err := Evaluate(Declaration{Bindings: bindings}, nodes, nil, ProducedOnly)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if !reflect.DeepEqual(got, map[string]any{"endpoint": "e"}) {
		t.Fatalf("outputs = %v, want only the binding whose sources are all produced", got)
	}
}

func TestEvaluate_ReleasedNodeInvalidatesItsBindings(t *testing.T) {
	bindings := map[string]*lang.Value{"endpoint": from("nodes.orchestrator.outputs.endpoint")}
	nodes := map[string]*contract.TaskState{
		"orchestrator": {Status: contract.TaskStatusCleaned, Outputs: map[string]any{"endpoint": "old"}},
	}
	got, err := Evaluate(Declaration{Bindings: bindings}, nodes, nil, ProducedOnly)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("outputs = %v, want none: a cleaned node's retained outputs are not a source", got)
	}
}

func TestEvaluate_ProducedNodeWithoutTheOutputLeavesTheKeyUnsetAndIsReported(t *testing.T) {
	bindings := map[string]*lang.Value{"endpoint": from("nodes.orchestrator.outputs.endpoint")}
	nodes := map[string]*contract.TaskState{"orchestrator": produced(map[string]any{"other": "x"})}
	got, err := Evaluate(Declaration{Bindings: bindings}, nodes, nil, ProducedOnly)
	if _, set := got["endpoint"]; set {
		t.Fatalf("outputs = %v, want endpoint unset", got)
	}
	if err == nil || !strings.Contains(err.Error(), `"endpoint"`) {
		t.Fatalf("error = %v, want it to name the binding", err)
	}
}

func TestEvaluate_CompleteRecordMustSatisfyItsSchema(t *testing.T) {
	bindings := map[string]*lang.Value{"count": from("nodes.worker.outputs.count")}
	schema := map[string]any{
		"type":       "object",
		"properties": map[string]any{"count": map[string]any{"type": "string"}},
	}
	nodes := map[string]*contract.TaskState{"worker": produced(map[string]any{"count": float64(3)})}
	got, err := Evaluate(Declaration{Bindings: bindings, Schema: schema}, nodes, nil, ProducedOnly)
	if err == nil || !strings.Contains(err.Error(), "outputs_schema") {
		t.Fatalf("Evaluate = %v, %v, want a schema violation", got, err)
	}
	if len(got) != 0 {
		t.Fatalf("outputs = %v, want none published when the record violates its schema", got)
	}
}

func TestEvaluate_PartialRecordIsNotCheckedAgainstRequired(t *testing.T) {
	bindings := map[string]*lang.Value{
		"a": from("nodes.first.outputs.a"),
		"b": from("nodes.second.outputs.b"),
	}
	schema := map[string]any{"type": "object", "required": []any{"a", "b"}}
	nodes := map[string]*contract.TaskState{"first": produced(map[string]any{"a": "1"})}
	got, err := Evaluate(Declaration{Bindings: bindings, Schema: schema}, nodes, nil, ProducedOnly)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if !reflect.DeepEqual(got, map[string]any{"a": "1"}) {
		t.Fatalf("outputs = %v, want the evaluable binding published", got)
	}
}

func sessionWithRevision(t *testing.T, wf config.WorkflowFile) *domain.Session {
	t.Helper()
	wf.Address = wf.ID
	body, digest, err := config.WorkflowRevision{Workflow: wf}.CanonicalBody()
	if err != nil {
		t.Fatal(err)
	}
	return &domain.Session{
		Name:                 "s",
		Workflow:             wf.ID,
		WorkflowRevision:     digest,
		WorkflowRevisionBody: string(body),
		Inputs:               map[string]any{"number": "7"},
	}
}

func TestOf_FollowsTheNodesTheSessionHoldsNow(t *testing.T) {
	s := sessionWithRevision(t, config.WorkflowFile{ID: "review", Outputs: map[string]*lang.Value{
		"endpoint": from("nodes.orchestrator.outputs.endpoint"),
		"number":   from("session.inputs.number"),
	}})
	s.Nodes = map[string]*contract.TaskState{"orchestrator": produced(map[string]any{"endpoint": "e1"})}
	got, err := Of(s, ProducedOnly)
	if err != nil {
		t.Fatalf("Of: %v", err)
	}
	if want := map[string]any{"endpoint": "e1", "number": "7"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("outputs = %v, want %v", got, want)
	}

	s.Nodes["orchestrator"] = produced(map[string]any{"endpoint": "e2"})
	if got, _ := Of(s, ProducedOnly); got["endpoint"] != "e2" {
		t.Fatalf("outputs = %v, want the recreated node's output", got)
	}

	s.Nodes["orchestrator"] = &contract.TaskState{Status: contract.TaskStatusCleaned, Outputs: map[string]any{"endpoint": "e2"}}
	got, _ = Of(s, ProducedOnly)
	if _, stale := got["endpoint"]; stale {
		t.Fatalf("outputs = %v, want the released node's binding unset", got)
	}
	if got["number"] != "7" {
		t.Fatalf("outputs = %v, want the session-input binding kept", got)
	}
}

func TestOf_SessionWithoutRevisionHasNoOutputs(t *testing.T) {
	for _, workflow := range []string{"", "named-before-revisions-existed"} {
		got, err := Of(&domain.Session{Name: "bare", Workflow: workflow}, ProducedOnly)
		if err != nil || len(got) != 0 {
			t.Fatalf("workflow %q: Of = %v, %v, want nothing", workflow, got, err)
		}
	}
}

func TestOf_UndecodableRevisionIsAnError(t *testing.T) {
	s := &domain.Session{Name: "broken", Workflow: "w", WorkflowRevision: "digest", WorkflowRevisionBody: "not json"}
	if _, err := Of(s, ProducedOnly); err == nil {
		t.Fatal("Of accepted an undecodable revision")
	}
}

func TestEvaluate_LastRecordedCountsANodeWhateverItsStatus(t *testing.T) {
	bindings := map[string]*lang.Value{"k": from("nodes.y.outputs.k")}
	for _, status := range []string{contract.TaskStatusProduced, contract.TaskStatusFailed, contract.TaskStatusCleaned} {
		nodes := map[string]*contract.TaskState{"y": {Status: status, Outputs: map[string]any{"k": "v1"}}}
		got, err := Evaluate(Declaration{Bindings: bindings}, nodes, nil, LastRecorded)
		if err != nil || got["k"] != "v1" {
			t.Fatalf("status %s: outputs = %v, %v, want the recorded value", status, got, err)
		}
	}
	got, _ := Evaluate(Declaration{Bindings: bindings}, map[string]*contract.TaskState{}, nil, LastRecorded)
	if len(got) != 0 {
		t.Fatalf("outputs = %v, want none for a node with no record", got)
	}
}

func TestEvaluate_NamesABindingThatFailsAlthoughItsSourcesExist(t *testing.T) {
	bindings := map[string]*lang.Value{
		"good": from("nodes.y.outputs.k"),
		"bad":  from("nodes.y.outputs.missing"),
	}
	nodes := map[string]*contract.TaskState{"y": produced(map[string]any{"k": "v1"})}
	got, err := Evaluate(Declaration{Bindings: bindings}, nodes, nil, ProducedOnly)
	if err == nil || !strings.Contains(err.Error(), `"bad"`) || strings.Contains(err.Error(), `"good"`) {
		t.Fatalf("error = %v, want it to name only the failing binding", err)
	}
	if got["good"] != "v1" {
		t.Fatalf("outputs = %v, want the evaluable binding kept", got)
	}
}

func TestEvaluate_OptionalOutputTheNodeOmittedIsNotAComplaint(t *testing.T) {
	bindings := map[string]*lang.Value{"k": {Form: lang.FormFrom, From: "nodes.y.outputs.k", Optional: true}}
	nodes := map[string]*contract.TaskState{"y": produced(map[string]any{})}
	got, err := Evaluate(Declaration{Bindings: bindings}, nodes, nil, ProducedOnly)
	if err != nil || len(got) != 0 {
		t.Fatalf("Evaluate = %v, %v, want an unset key and no error", got, err)
	}
}

func TestDeclare_ReadsTheFrozenWorkflowsDeclaration(t *testing.T) {
	s := sessionWithRevision(t, config.WorkflowFile{
		ID:            "review",
		Outputs:       map[string]*lang.Value{"endpoint": from("nodes.orchestrator.outputs.endpoint")},
		OutputsSchema: map[string]any{"type": "object"},
	})
	decl, err := Declare(s)
	if err != nil {
		t.Fatalf("Declare: %v", err)
	}
	if decl.Bindings["endpoint"] == nil || decl.Schema["type"] != "object" {
		t.Fatalf("declaration = %+v", decl)
	}
}

func TestDeclare_SessionWithoutRevisionDeclaresNothing(t *testing.T) {
	decl, err := Declare(&domain.Session{Name: "bare"})
	if err != nil || len(decl.Bindings) != 0 {
		t.Fatalf("Declare = %+v, %v, want an empty declaration", decl, err)
	}
}
