package commands

import (
	"reflect"
	"testing"

	"github.com/kecbigmt/plect/app/internal/domain"
	"github.com/kecbigmt/plect/app/internal/template"
	contractstate "github.com/kecbigmt/plect/contracts/state"
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

func TestTemplateVarsFromSession_GitHub(t *testing.T) {
	s := &domain.Session{
		Name:         "org/repo-42",
		ResourceID:   "https://github.com/org/repo/issues/42",
		WorktreePath: "/wt/org/repo/branch",
		URL:          "https://github.com/org/repo/issues/42",
		Number:       42,
		OwnerRepo:    "org/repo",
		Inputs:       map[string]any{"template": "work"},
		Tasks: map[string]*contractstate.TaskState{
			contractstate.WorkflowPseudoNodeID: {Outputs: map[string]any{"title": "Fix bug"}},
		},
	}

	vars := templateVarsFromSession(s.Name, s)

	if vars.SessionName != "org/repo-42" || vars.WorktreePath != "/wt/org/repo/branch" {
		t.Errorf("unexpected session fields: %+v", vars)
	}
	if vars.Number != 42 || vars.OwnerRepo != "org/repo" || vars.Repo != "repo" || vars.URL == "" {
		t.Errorf("GitHub compat fields not backfilled: %+v", vars)
	}
	if vars.Workflow["title"] != "Fix bug" {
		t.Errorf("workflow outputs missing: %+v", vars.Workflow)
	}
	if vars.SessionInputs["template"] != "work" {
		t.Errorf("session inputs missing: %+v", vars.SessionInputs)
	}
}

func TestApplyResourceOverride(t *testing.T) {
	// Session tracks the issue; the effect's resource is a PR.
	base := template.Vars{
		SessionName:   "org/repo-42",
		WorktreePath:  "/wt/org/repo/branch",
		URL:           "https://github.com/org/repo/issues/42",
		Number:        42,
		Repo:          "repo",
		OwnerRepo:     "org/repo",
		SessionInputs: map[string]any{"template": "review"},
	}

	got, err := applyResourceOverride(base, "https://github.com/org/repo/pull/446")
	if err != nil {
		t.Fatalf("applyResourceOverride() error: %v", err)
	}
	if got.URL != "https://github.com/org/repo/pull/446" || got.Number != 446 || got.Repo != "repo" || got.OwnerRepo != "org/repo" {
		t.Errorf("resource vars not rebound: %+v", got)
	}
	// Session-supplied context must survive the override.
	if got.SessionName != "org/repo-42" || got.WorktreePath != "/wt/org/repo/branch" || got.SessionInputs["template"] != "review" {
		t.Errorf("session context clobbered: %+v", got)
	}

	noop, err := applyResourceOverride(base, "")
	if err != nil {
		t.Fatalf("applyResourceOverride(\"\") error: %v", err)
	}
	if !reflect.DeepEqual(noop, base) {
		t.Errorf("empty resource should be a no-op: got %+v want %+v", noop, base)
	}

	if _, err := applyResourceOverride(base, "not-a-url"); err == nil {
		t.Error("applyResourceOverride(invalid) expected error, got nil")
	}
}

func TestTemplateVarsFromSession_NonGitHub(t *testing.T) {
	// Orchestrator-style session: no GitHub fields, provider outputs carry owner.
	s := &domain.Session{
		Name:         "acme/_orchestrator",
		ResourceID:   "owner:acme",
		WorktreePath: "/scratch/acme",
		Tasks: map[string]*contractstate.TaskState{
			contractstate.WorkflowPseudoNodeID: {Outputs: map[string]any{"owner": "acme"}},
		},
	}

	vars := templateVarsFromSession(s.Name, s)

	if vars.URL != "" || vars.Number != 0 || vars.OwnerRepo != "" || vars.Repo != "" {
		t.Errorf("GitHub fields should be empty for non-GitHub session: %+v", vars)
	}
	if vars.Workflow["owner"] != "acme" {
		t.Errorf("workflow outputs missing owner: %+v", vars.Workflow)
	}
	if vars.SessionInputs == nil {
		t.Error("SessionInputs should be non-nil even with no session inputs")
	}
}
