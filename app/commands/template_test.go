package commands

import (
	"reflect"
	"testing"

	"github.com/kecbigmt/sennit/app/internal/domain"
	contractstate "github.com/kecbigmt/sennit/contracts/state"
)

func TestParseTemplateVars(t *testing.T) {
	got, err := parseTemplateVars([]string{"a=1", "b=x=y"})
	if err != nil {
		t.Fatalf("parseTemplateVars() error: %v", err)
	}
	want := map[string]any{"a": "1", "b": "x=y"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parseTemplateVars() = %v, want %v", got, want)
	}

	for _, bad := range []string{"noeq", "=novalue"} {
		if _, err := parseTemplateVars([]string{bad}); err == nil {
			t.Errorf("parseTemplateVars(%q) expected error, got nil", bad)
		}
	}
}

func TestTemplateVarsFromSession(t *testing.T) {
	s := &domain.Session{
		Name:         "org/repo-42",
		ResourceID:   "https://github.com/org/repo/issues/42",
		WorktreePath: "/wt/org/repo/branch",
		Inputs:       map[string]any{"template": "work"},
		Tasks: map[string]*contractstate.TaskState{
			contractstate.WorkflowPseudoNodeID: {Outputs: map[string]any{"title": "Fix bug"}},
		},
	}

	vars := templateVarsFromSession(s.Name, s)

	if vars.SessionName != "org/repo-42" || vars.WorktreePath != "/wt/org/repo/branch" {
		t.Errorf("unexpected session fields: %+v", vars)
	}
	if vars.ResourceID != "https://github.com/org/repo/issues/42" {
		t.Errorf("resource id not carried: %+v", vars)
	}
	if vars.Workflow["title"] != "Fix bug" {
		t.Errorf("workflow outputs missing: %+v", vars.Workflow)
	}
	if vars.SessionInputs["template"] != "work" {
		t.Errorf("session inputs missing: %+v", vars.SessionInputs)
	}
}

func TestTemplateVarsFromSession_IdentitySession(t *testing.T) {
	// Orchestrator-style session: the provider outputs carry the owner.
	s := &domain.Session{
		Name:         "acme/_orchestrator",
		ResourceID:   "owner:acme",
		WorktreePath: "/scratch/acme",
		Tasks: map[string]*contractstate.TaskState{
			contractstate.WorkflowPseudoNodeID: {Outputs: map[string]any{"owner": "acme"}},
		},
	}

	vars := templateVarsFromSession(s.Name, s)

	if vars.ResourceID != "owner:acme" {
		t.Errorf("resource id not carried: %+v", vars)
	}
	if vars.Workflow["owner"] != "acme" {
		t.Errorf("workflow outputs missing owner: %+v", vars.Workflow)
	}
	if vars.SessionInputs == nil {
		t.Error("SessionInputs should be non-nil even with no session inputs")
	}
}
