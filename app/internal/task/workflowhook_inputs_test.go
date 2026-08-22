package task

import (
	"strings"
	"testing"

	"github.com/kecbigmt/plecture/app/internal/lang"
)

// resolveProviderValue resolves one value against a provider hook's
// environment, which is how a hook reaches its author-declared parameters.
func resolveProviderValue(t *testing.T, vars WorkflowHookVars, value *lang.Value) (string, error) {
	t.Helper()
	eval := providerEval(providerRoots(vars, nil, nil, false), nil, "", lang.Ownership{})
	resolved, absent, err := eval.Argument(value)
	if absent {
		return "", nil
	}
	return resolved, err
}

func TestProviderHook_ExposesWorkspaceProviderInputs(t *testing.T) {
	vars := WorkflowHookVars{
		ResourceID:  "example://acme/widget/1",
		SessionName: "test-session",
		Inputs:      map[string]any{"workspace_layout_root": "~/worktrees"},
	}
	got, err := resolveProviderValue(t, vars, &lang.Value{Form: lang.FormFrom, From: "inputs.workspace_layout_root"})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if strings.TrimSpace(got) != "~/worktrees" {
		t.Errorf("resolved = %q", got)
	}
}

// A parameter the workflow left unset resolves through the value's own
// default rather than failing the hook, so the executable behind it keeps
// owning what unset means.
func TestProviderHook_UnsetInputFallsBackToItsDefault(t *testing.T) {
	vars := WorkflowHookVars{ResourceID: "example://acme/widget/1", SessionName: "test-session"}
	got, err := resolveProviderValue(t, vars,
		&lang.Value{Form: lang.FormFrom, From: "inputs.workspace_layout_root", Default: "none", HasDefault: true})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got != "none" {
		t.Errorf("resolved = %q, want the declared default", got)
	}
}

func TestProviderHook_UnsetInputWithNoDefaultIsAnError(t *testing.T) {
	vars := WorkflowHookVars{ResourceID: "example://acme/widget/1", SessionName: "test-session"}
	if _, err := resolveProviderValue(t, vars,
		&lang.Value{Form: lang.FormFrom, From: "inputs.workspace_layout_root"}); err == nil {
		t.Fatal("expected a reference to an unset parameter with no default to fail")
	}
}

// A setup hook observes no self.outputs root: it is producing those outputs,
// so a projection of one would read a value that does not exist yet.
func TestProviderHook_SetupObservesNoSelfOutputs(t *testing.T) {
	vars := WorkflowHookVars{ResourceID: "example://acme/widget/1", SessionName: "test-session"}
	if _, err := resolveProviderValue(t, vars,
		&lang.Value{Form: lang.FormFrom, From: "self.outputs.workspace_dir"}); err == nil {
		t.Fatal("expected setup to observe no self.outputs root")
	}
}
