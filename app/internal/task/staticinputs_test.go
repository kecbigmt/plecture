package task

import (
	"testing"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/lang"
)

// compileSingleNodeInputs compiles a one-node, one-task workflow so
// ValidateInputsStatic sees exactly what CompileWorkflow (and so
// WorkflowShow / plect up) would produce for it.
func compileSingleNodeInputs(t *testing.T, inputsSchema map[string]any, nodeInputs map[string]*lang.Value) Resolved {
	t.Helper()
	wf := config.WorkflowFile{
		ID: "wf",
		Nodes: []config.WorkflowNode{
			{ID: "n", Uses: "task", Inputs: nodeInputs},
		},
	}
	defs := map[string]config.TaskDefinition{
		"task": {ID: "task", Scope: "run", Setup: shellStub("true"), InputsSchema: inputsSchema},
	}
	plan, err := CompileWorkflow(wf, defs)
	if err != nil {
		t.Fatalf("CompileWorkflow: %v", err)
	}
	return plan.Run[0]
}

func TestValidateInputsStatic_NoSchemaIsSilent(t *testing.T) {
	r := compileSingleNodeInputs(t, nil, map[string]*lang.Value{
		"anything": literalValue("x"),
	})
	if issues := ValidateInputsStatic(r); issues != nil {
		t.Fatalf("issues = %+v, want nil", issues)
	}
}

func TestValidateInputsStatic_UnknownKeyUnderAdditionalPropertiesFalse(t *testing.T) {
	schema := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"template": map[string]any{"type": "string"},
		},
	}
	// This is the incident shape: a node still wires a key (tmux_session) a
	// task's inputs_schema no longer declares.
	r := compileSingleNodeInputs(t, schema, map[string]*lang.Value{
		"tmux_session": literalValue("main"),
	})
	issues := ValidateInputsStatic(r)
	if len(issues) != 1 || issues[0].Key != "tmux_session" {
		t.Fatalf("issues = %+v, want one issue for key %q", issues, "tmux_session")
	}
}

func TestValidateInputsStatic_MissingRequiredKey(t *testing.T) {
	schema := map[string]any{
		"type":     "object",
		"required": []any{"template"},
		"properties": map[string]any{
			"template": map[string]any{"type": "string"},
		},
	}
	r := compileSingleNodeInputs(t, schema, map[string]*lang.Value{})
	issues := ValidateInputsStatic(r)
	if len(issues) != 1 || issues[0].Key != "template" {
		t.Fatalf("issues = %+v, want one issue for key %q", issues, "template")
	}
}

func TestValidateInputsStatic_LiteralTypeMismatchDetected(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"count": map[string]any{"type": "integer"},
		},
	}
	r := compileSingleNodeInputs(t, schema, map[string]*lang.Value{
		"count": {Form: lang.FormLiteral, Literal: "not-a-number"},
	})
	issues := ValidateInputsStatic(r)
	if len(issues) != 1 || issues[0].Key != "count" {
		t.Fatalf("issues = %+v, want one issue for key %q", issues, "count")
	}
}

func TestValidateInputsStatic_FromDefaultTypeMismatchDetected(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"count": map[string]any{"type": "integer"},
		},
	}
	r := compileSingleNodeInputs(t, schema, map[string]*lang.Value{
		"count": {Form: lang.FormFrom, From: "session.inputs.count", HasDefault: true, Default: "not-a-number"},
	})
	issues := ValidateInputsStatic(r)
	if len(issues) != 1 || issues[0].Key != "count" {
		t.Fatalf("issues = %+v, want one issue for key %q", issues, "count")
	}
}

func TestValidateInputsStatic_FromWithoutDefaultIsNotFlagged(t *testing.T) {
	schema := map[string]any{
		"type":     "object",
		"required": []any{"template"},
		"properties": map[string]any{
			"template": map[string]any{"type": "string"},
		},
	}
	// A bare `{ from = ... }` binding has no value until dispatch resolves
	// it: this must satisfy `required` (the key is declared) and must not
	// be scored against `template`'s type from an unresolved placeholder.
	r := compileSingleNodeInputs(t, schema, map[string]*lang.Value{
		"template": fromValue("session.inputs.template"),
	})
	if issues := ValidateInputsStatic(r); issues != nil {
		t.Fatalf("issues = %+v, want nil", issues)
	}
}

func TestValidateInputsStatic_ValidNodePassesCleanly(t *testing.T) {
	schema := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []any{"template"},
		"properties": map[string]any{
			"template": map[string]any{"type": "string"},
		},
	}
	r := compileSingleNodeInputs(t, schema, map[string]*lang.Value{
		"template": literalValue("coding"),
	})
	if issues := ValidateInputsStatic(r); issues != nil {
		t.Fatalf("issues = %+v, want nil", issues)
	}
}

func TestValidateInputsStatic_ReportsEveryDecidableIssue(t *testing.T) {
	schema := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []any{"template"},
		"properties": map[string]any{
			"template": map[string]any{"type": "string"},
		},
	}
	r := compileSingleNodeInputs(t, schema, map[string]*lang.Value{
		"tmux_session": literalValue("main"),
	})
	issues := ValidateInputsStatic(r)
	if len(issues) != 2 {
		t.Fatalf("issues = %+v, want 2 (unknown key + missing required)", issues)
	}
	if issues[0].Key != "template" || issues[1].Key != "tmux_session" {
		t.Errorf("issues = %+v, want sorted [template, tmux_session]", issues)
	}
}
