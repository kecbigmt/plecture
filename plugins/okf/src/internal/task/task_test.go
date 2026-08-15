package task

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestValidateGoalResource(t *testing.T) {
	tests := []struct {
		name       string
		resourceID string
		wantErr    bool
	}{
		{"valid goal resource", "local-okf://acme/goals/ship-it.md", false},
		{"nested goal path", "local-okf://acme/goals/2026/ship-it.md", false},
		{"non-goal concept kind", "local-okf://acme/retrospectives/2026-q3.md", true},
		{"not a goal resource scheme", "https://github.com/acme/repo/issues/1", true},
		{"missing md suffix", "local-okf://acme/goals/ship-it", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateGoalResource(tt.resourceID)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateGoalResource(%q) error = %v, wantErr %v", tt.resourceID, err, tt.wantErr)
			}
		})
	}
}

func TestInstanceName(t *testing.T) {
	if got := InstanceName("stage3-demo"); got != "goal_stage3_demo" {
		t.Errorf("InstanceName(stage3-demo) = %q, want goal_stage3_demo", got)
	}
	if got := InstanceName("flaky-tests"); got != "goal_flaky_tests" {
		t.Errorf("InstanceName(flaky-tests) = %q, want goal_flaky_tests", got)
	}
}

func TestShouldInstantiate(t *testing.T) {
	tests := []struct {
		name           string
		goalAssignees  []string
		inputAssignees []string
		want           bool
	}{
		{"no filter admits everything", nil, nil, true},
		{"unscoped goal admits any filter", nil, []string{"user:alice"}, true},
		{"matching assignee admits", []string{"user:alice"}, []string{"user:alice"}, true},
		{"non-matching assignee excludes", []string{"user:bob"}, []string{"user:alice"}, false},
		{"anyone admits any filter", []string{"anyone"}, []string{"user:alice"}, true},
		{"one of several matches", []string{"user:bob", "team:platform"}, []string{"team:platform"}, true},
		{"scoped goal with active filter and no match excludes", []string{"user:bob"}, []string{"user:alice", "team:platform"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldInstantiate(tt.goalAssignees, tt.inputAssignees); got != tt.want {
				t.Errorf("shouldInstantiate(%v, %v) = %v, want %v", tt.goalAssignees, tt.inputAssignees, got, tt.want)
			}
		})
	}
}

// fakeRunner records SetupPursueGoal calls and answers ExistingInstances
// from a fixed set, without a real `plect` binary.
type fakeRunner struct {
	existing map[string]bool
	created  []string
	failOn   string
}

func (f *fakeRunner) ExistingInstances(session string) (map[string]bool, error) {
	return f.existing, nil
}

func (f *fakeRunner) SetupPursueGoal(session, name, resourceID string) error {
	if name == f.failOn {
		return fmt.Errorf("boom")
	}
	f.created = append(f.created, name)
	return nil
}

func writeGoalFile(t *testing.T, dir, name, status, assigneeYAML string) {
	t.Helper()
	fm := "---\ntype: Goal\nstatus: " + status + "\n"
	if assigneeYAML != "" {
		fm += assigneeYAML + "\n"
	}
	fm += "---\n## Done When\n- [ ] x\n"
	if err := os.WriteFile(filepath.Join(dir, name), []byte(fm), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestBootstrap_createsInstancesForOpenGoalsOnly(t *testing.T) {
	goalsDir := t.TempDir()
	writeGoalFile(t, goalsDir, "open-goal.md", "open", "")
	writeGoalFile(t, goalsDir, "blocked-goal.md", "blocked", "")
	writeGoalFile(t, goalsDir, "completed-goal.md", "completed", "")
	if err := os.WriteFile(filepath.Join(goalsDir, "index.md"), []byte("# Goals\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	runner := &fakeRunner{existing: map[string]bool{}}
	created, err := Bootstrap(runner, goalsDir, "acme", "acme/_orchestrator", nil)
	if err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	if len(created) != 1 || created[0] != "goal_open_goal" {
		t.Errorf("created = %v, want [goal_open_goal]", created)
	}
}

func TestBootstrap_skipsAlreadyExistingInstances(t *testing.T) {
	goalsDir := t.TempDir()
	writeGoalFile(t, goalsDir, "open-goal.md", "open", "")

	runner := &fakeRunner{existing: map[string]bool{"goal_open_goal": true}}
	created, err := Bootstrap(runner, goalsDir, "acme", "acme/_orchestrator", nil)
	if err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	if len(created) != 0 {
		t.Errorf("created = %v, want none", created)
	}
}

func TestBootstrap_appliesTheAssigneeFilter(t *testing.T) {
	goalsDir := t.TempDir()
	writeGoalFile(t, goalsDir, "mine.md", "open", "assignee: user:alice")
	writeGoalFile(t, goalsDir, "theirs.md", "open", "assignee: user:bob")

	runner := &fakeRunner{existing: map[string]bool{}}
	created, err := Bootstrap(runner, goalsDir, "acme", "acme/_orchestrator", []string{"user:alice"})
	if err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	if len(created) != 1 || created[0] != "goal_mine" {
		t.Errorf("created = %v, want [goal_mine]", created)
	}
}

func TestBootstrap_missingGoalsDirIsNotAnError(t *testing.T) {
	runner := &fakeRunner{existing: map[string]bool{}}
	created, err := Bootstrap(runner, filepath.Join(t.TempDir(), "missing"), "acme", "acme/_orchestrator", nil)
	if err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	if created != nil {
		t.Errorf("created = %v, want none", created)
	}
}
