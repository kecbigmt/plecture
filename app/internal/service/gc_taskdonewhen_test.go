package service

import (
	"testing"

	"github.com/kecbigmt/sennit/app/internal/config"
	"github.com/kecbigmt/sennit/app/internal/domain"
	"github.com/kecbigmt/sennit/app/internal/task"
	contract "github.com/kecbigmt/sennit/contracts/state"
)

func strp(s string) *string { return &s }

// reviewDefs is a fixture task set: `review` carries a done_when
// (checks_status eq SUCCESS), `tmux` is a pure lifecycle-only with none.
func reviewDefs() map[string]config.TaskDefinition {
	return map[string]config.TaskDefinition{
		"review": {ID: "review", Scope: "session", DoneWhen: &config.DoneWhen{
			All: []config.DoneWhenLeaf{{Check: "checks_status", Eq: strp("SUCCESS")}},
		}},
		"tmux": {ID: "tmux", Scope: "run"},
	}
}

func produced(taskID string, outputs map[string]any) *contract.TaskState {
	return &contract.TaskState{TaskID: taskID, Status: contract.TaskStatusProduced, Outputs: outputs}
}

// ADR-003 step 4: completion is the aggregate of the session's done_when-bearing
// task instances — each judged against its own per-instance outputs.
func TestAggregateTaskDoneWhen(t *testing.T) {
	defs := reviewDefs()
	tests := []struct {
		name       string
		tasks      map[string]*contract.TaskState
		wantStatus task.DoneStatus
		wantCount  int
		wantWarn   bool
	}{
		{
			name:       "no done_when task → count 0 (caller falls back)",
			tasks:      map[string]*contract.TaskState{"tmux": produced("tmux", nil)},
			wantStatus: "",
			wantCount:  0,
		},
		{
			name:       "single instance satisfied",
			tasks:      map[string]*contract.TaskState{"review#1": produced("review", map[string]any{"checks_status": "SUCCESS"})},
			wantStatus: task.DoneSatisfied,
			wantCount:  1,
		},
		{
			name: "all instances satisfied; non-done_when task ignored",
			tasks: map[string]*contract.TaskState{
				"review#1": produced("review", map[string]any{"checks_status": "SUCCESS"}),
				"review#2": produced("review", map[string]any{"checks_status": "SUCCESS"}),
				"tmux":     produced("tmux", nil),
			},
			wantStatus: task.DoneSatisfied,
			wantCount:  2,
		},
		{
			name: "one instance pending → whole session pending",
			tasks: map[string]*contract.TaskState{
				"review#1": produced("review", map[string]any{"checks_status": "SUCCESS"}),
				"review#2": produced("review", map[string]any{}), // checks_status not yet observed
			},
			wantStatus: task.DonePending,
			wantCount:  2,
		},
		{
			name: "one instance unsatisfied dominates",
			tasks: map[string]*contract.TaskState{
				"review#1": produced("review", map[string]any{"checks_status": "SUCCESS"}),
				"review#2": produced("review", map[string]any{"checks_status": "FAILURE"}),
			},
			wantStatus: task.DoneUnsatisfied,
			wantCount:  2,
		},
		{
			name: "cleaned instance excluded from the aggregate",
			tasks: map[string]*contract.TaskState{
				"review#1": produced("review", map[string]any{"checks_status": "SUCCESS"}),
				"review#2": {TaskID: "review", Status: contract.TaskStatusCleaned, Outputs: map[string]any{"checks_status": "FAILURE"}},
			},
			wantStatus: task.DoneSatisfied,
			wantCount:  1,
		},
		{
			name:       "static node key == task id (TaskID omitted)",
			tasks:      map[string]*contract.TaskState{"review": produced("", map[string]any{"checks_status": "SUCCESS"})},
			wantStatus: task.DoneSatisfied,
			wantCount:  1,
		},
		{
			name: "failed instance reads pending and pins the session (fail closed)",
			tasks: map[string]*contract.TaskState{
				"review#1": produced("review", map[string]any{"checks_status": "SUCCESS"}),
				"review#2": {TaskID: "review", Status: contract.TaskStatusFailed, Outputs: map[string]any{}},
			},
			wantStatus: task.DonePending,
			wantCount:  2,
		},
		{
			name:       "@workflow pseudo-node is excluded",
			tasks:      map[string]*contract.TaskState{contract.WorkflowPseudoNodeID: produced("", map[string]any{"workdir": "/x"})},
			wantStatus: "",
			wantCount:  0,
		},
		{
			name: "missing def for a dynamic work instance warns and blocks (no fail-open)",
			tasks: map[string]*contract.TaskState{
				"gone#1": {TaskID: "gone", Dynamic: true, Status: contract.TaskStatusProduced, Outputs: map[string]any{}},
			},
			wantStatus: task.DonePending,
			wantCount:  1,
			wantWarn:   true,
		},
		{
			name: "missing def for a static node is silent (legacy fallback path)",
			tasks: map[string]*contract.TaskState{
				"gone": {Status: contract.TaskStatusProduced, Outputs: map[string]any{}},
			},
			wantStatus: "",
			wantCount:  0,
			wantWarn:   false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &domain.Session{Name: "owner/repo-1", Tasks: tt.tasks}
			gotStatus, gotCount, gotWarnings := aggregateTaskDoneWhen(s, defs, map[string]*domain.Session{s.Name: s})
			if gotStatus != tt.wantStatus || gotCount != tt.wantCount {
				t.Errorf("got (%q, %d), want (%q, %d)", gotStatus, gotCount, tt.wantStatus, tt.wantCount)
			}
			if (len(gotWarnings) > 0) != tt.wantWarn {
				t.Errorf("warnings = %v, wantWarn = %v", gotWarnings, tt.wantWarn)
			}
		})
	}
}

// The aggregate is the primary basis: all instances satisfied → done.
func TestClassifySession_TaskDoneWhenAggregatesToDone(t *testing.T) {
	wtPath := cleanGitWorktree(t)
	s := &domain.Session{
		Name:         "owner/repo-50",
		WorktreePath: wtPath,
		Workflow:     "wf",
		Tasks: map[string]*contract.TaskState{
			"review#1": produced("review", map[string]any{"checks_status": "SUCCESS"}),
			"review#2": produced("review", map[string]any{"checks_status": "SUCCESS"}),
		},
	}

	entry, warn := classifySession(s, reviewDefs(), map[string]*domain.Session{s.Name: s})
	if warn != "" {
		t.Errorf("unexpected warning: %s", warn)
	}
	if entry == nil || entry.Action != GCActionDelete || entry.Reason != GCReasonDone {
		t.Fatalf("all instances satisfied should be done, got %+v", entry)
	}
}

// Per-instance independence: one PR's checks pass, another's are not yet
// observed → the session is not done.
func TestClassifySession_TaskDoneWhenPerInstancePendingBlocks(t *testing.T) {
	wtPath := cleanGitWorktree(t)
	s := &domain.Session{
		Name:         "owner/repo-51",
		WorktreePath: wtPath,
		Workflow:     "wf",
		Tasks: map[string]*contract.TaskState{
			"review#1": produced("review", map[string]any{"checks_status": "SUCCESS"}),
			"review#2": produced("review", map[string]any{}),
		},
	}

	entry, _ := classifySession(s, reviewDefs(), map[string]*domain.Session{s.Name: s})
	if entry != nil && entry.Action == GCActionDelete {
		t.Error("a pending per-instance done_when must block completion (fail closed)")
	}
}

// One PR's checks fail → unsatisfied → session not done.
func TestClassifySession_TaskDoneWhenPerInstanceUnsatisfiedBlocks(t *testing.T) {
	wtPath := cleanGitWorktree(t)
	s := &domain.Session{
		Name:         "owner/repo-53",
		WorktreePath: wtPath,
		Workflow:     "wf",
		Tasks: map[string]*contract.TaskState{
			"review#1": produced("review", map[string]any{"checks_status": "SUCCESS"}),
			"review#2": produced("review", map[string]any{"checks_status": "FAILURE"}),
		},
	}

	entry, _ := classifySession(s, reviewDefs(), map[string]*domain.Session{s.Name: s})
	if entry != nil && entry.Action == GCActionDelete {
		t.Error("an unsatisfied per-instance done_when must block completion")
	}
}

// Fail-open guard: a dynamic work instance whose task def vanished (config
// drift) must not let a session fall through to an auto-delete with its work
// unaccounted for. It blocks and warns instead.
func TestClassifySession_MissingDynamicDefBlocksDelete(t *testing.T) {
	wtPath := cleanGitWorktree(t)
	s := &domain.Session{
		Name:         "owner/repo-54",
		WorktreePath: wtPath,
		Workflow:     "wf",
		Tasks: map[string]*contract.TaskState{
			"gone#1": {TaskID: "gone", Dynamic: true, Status: contract.TaskStatusProduced, Outputs: map[string]any{}},
		},
	}

	entry, warn := classifySession(s, reviewDefs(), map[string]*domain.Session{s.Name: s})
	if warn == "" {
		t.Error("expected a config-drift warning for the missing task def")
	}
	if entry != nil && entry.Action == GCActionDelete {
		t.Error("a vanished work-task def must block auto-delete")
	}
}

// A session with no done_when-bearing task instances has nothing for GC to
// evaluate — it is never auto-deleted on that basis alone.
func TestClassifySession_NoTaskDoneWhenIsNeverDeleted(t *testing.T) {
	wtPath := cleanGitWorktree(t)
	s := &domain.Session{
		Name:         "owner/repo-52",
		WorktreePath: wtPath,
		Workflow:     "wf",
		Tasks: map[string]*contract.TaskState{
			"tmux": produced("tmux", nil),
		},
	}

	entry, warn := classifySession(s, reviewDefs(), map[string]*domain.Session{s.Name: s})
	if warn != "" {
		t.Errorf("unexpected warning: %s", warn)
	}
	if entry != nil {
		t.Fatalf("no done_when task should not be auto-deleted, got %+v", entry)
	}
}
