package service

import (
	"strings"
	"testing"

	"github.com/kecbigmt/plecture/app/internal/config"
)

func githubLikeProvider() config.WorkspaceProviderConfig {
	return config.WorkspaceProviderConfig{
		ID: "github",
		InputsSchema: map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties": map[string]any{
				"workspace_layout_root": map[string]any{"type": "string"},
				"delete_branch_default": map[string]any{"type": "string", "enum": []any{"true", "false"}},
			},
		},
	}
}

func TestResolveWorkspaceProviderInputs_AcceptsDeclaredKeys(t *testing.T) {
	wf := config.WorkflowFile{ID: "issue", WorkspaceProviderInputs: map[string]string{
		"workspace_layout_root": "~/worktrees",
		"delete_branch_default": "true",
	}}
	got, err := resolveWorkspaceProviderInputs(githubLikeProvider(), wf)
	if err != nil {
		t.Fatalf("resolveWorkspaceProviderInputs: %v", err)
	}
	if got["workspace_layout_root"] != "~/worktrees" || got["delete_branch_default"] != "true" {
		t.Errorf("inputs = %v", got)
	}
}

func TestResolveWorkspaceProviderInputs_RejectsUndeclaredKey(t *testing.T) {
	wf := config.WorkflowFile{ID: "issue", WorkspaceProviderInputs: map[string]string{"layout_root": "~/worktrees"}}
	_, err := resolveWorkspaceProviderInputs(githubLikeProvider(), wf)
	if err == nil {
		t.Fatal("expected an undeclared parameter to be rejected")
	}
}

func TestResolveWorkspaceProviderInputs_RejectsValueOutsideDeclaredRange(t *testing.T) {
	wf := config.WorkflowFile{ID: "issue", WorkspaceProviderInputs: map[string]string{"delete_branch_default": "yes"}}
	_, err := resolveWorkspaceProviderInputs(githubLikeProvider(), wf)
	if err == nil {
		t.Fatal("expected a value outside the declared enum to be rejected")
	}
}

// A provider with no declared parameters has no variation point, so wiring one
// is an author error rather than a value silently dropped on the floor.
func TestResolveWorkspaceProviderInputs_RejectsInputsWithoutSchema(t *testing.T) {
	prov := config.WorkspaceProviderConfig{ID: "plain"}
	wf := config.WorkflowFile{ID: "issue", WorkspaceProviderInputs: map[string]string{"anything": "x"}}
	_, err := resolveWorkspaceProviderInputs(prov, wf)
	if err == nil {
		t.Fatal("expected inputs against a schema-less provider to be rejected")
	}
	if !strings.Contains(err.Error(), "inputs_schema") {
		t.Errorf("error = %v, want it to name inputs_schema", err)
	}
}

func TestResolveWorkspaceProviderInputs_SchemaLessProviderWithNoInputs(t *testing.T) {
	got, err := resolveWorkspaceProviderInputs(config.WorkspaceProviderConfig{ID: "plain"}, config.WorkflowFile{ID: "issue"})
	if err != nil {
		t.Fatalf("resolveWorkspaceProviderInputs: %v", err)
	}
	if got != nil {
		t.Errorf("inputs = %v, want nil", got)
	}
}
