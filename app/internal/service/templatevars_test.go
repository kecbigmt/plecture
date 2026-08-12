package service

import (
	"testing"

	"github.com/kecbigmt/plecture/app/internal/domain"
	contract "github.com/kecbigmt/plecture/contracts/state"
)

func TestProjectTemplateVars(t *testing.T) {
	s := &domain.Session{
		Name:         "org/repo-42",
		ResourceID:   "https://github.com/org/repo/issues/42",
		WorktreePath: "/wt/org/repo/branch",
		Inputs:       map[string]any{"template": "work"},
		Tasks: map[string]*contract.TaskState{
			contract.WorkflowPseudoNodeID: {Outputs: map[string]any{"title": "Fix bug"}},
		},
	}

	vars := projectTemplateVars(s.Name, s)

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

func TestProjectTemplateVars_IdentitySession(t *testing.T) {
	// Orchestrator-style session: the provider outputs carry the owner.
	s := &domain.Session{
		Name:         "acme/_orchestrator",
		ResourceID:   "owner:acme",
		WorktreePath: "/scratch/acme",
		Tasks: map[string]*contract.TaskState{
			contract.WorkflowPseudoNodeID: {Outputs: map[string]any{"owner": "acme"}},
		},
	}

	vars := projectTemplateVars(s.Name, s)

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
