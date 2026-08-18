package task

import (
	"strings"
	"testing"
)

func TestRenderWorkflowHook_ExposesWorkspaceProviderInputs(t *testing.T) {
	vars := WorkflowHookVars{
		ResourceID:  "owner:test",
		SessionName: "test-session",
		Inputs:      map[string]any{"workspace_layout_root": "~/worktrees"},
	}
	got, err := renderWorkflowHook(`echo {{.Inputs.workspace_layout_root}}`, vars, nil, nil, "missingkey=error")
	if err != nil {
		t.Fatalf("renderWorkflowHook: %v", err)
	}
	if strings.TrimSpace(got) != "echo ~/worktrees" {
		t.Errorf("rendered = %q", got)
	}
}

// A parameter the workflow left unset renders through `get`'s default rather
// than failing the hook, so the executable behind it keeps owning the default.
func TestRenderWorkflowHook_UnsetInputFallsBackThroughGet(t *testing.T) {
	vars := WorkflowHookVars{ResourceID: "owner:test", SessionName: "test-session"}
	got, err := renderWorkflowHook(`echo {{get .Inputs "workspace_layout_root" "none"}}`, vars, nil, nil, "missingkey=error")
	if err != nil {
		t.Fatalf("renderWorkflowHook: %v", err)
	}
	if strings.TrimSpace(got) != "echo none" {
		t.Errorf("rendered = %q", got)
	}
}

func TestRenderWorkflowHook_UnsetInputReferencedDirectlyIsAnError(t *testing.T) {
	vars := WorkflowHookVars{ResourceID: "owner:test", SessionName: "test-session"}
	if _, err := renderWorkflowHook(`echo {{.Inputs.workspace_layout_root}}`, vars, nil, nil, "missingkey=error"); err == nil {
		t.Fatal("expected a direct reference to an unset parameter to fail")
	}
}
